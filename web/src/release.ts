/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
import { reactive } from 'vue'

import { apiClient } from './api/client'
import type { DiffResponse, PublishCheck, Release, WorkspaceDetail } from './api/types'
import { sessionStore, type SessionStore } from './session'

export interface ReleaseClient {
  createPublishCheck: (
    workspaceId: string,
    etag: string,
    csrfToken: string,
    signal?: AbortSignal,
  ) => Promise<PublishCheck>
  getPublishCheck: (id: string, signal?: AbortSignal) => Promise<PublishCheck>
  createRelease: (
    workspaceId: string,
    checkId: string,
    confirmName: string,
    etag: string,
    csrfToken: string,
  ) => Promise<Release>
  getRelease: (id: string, signal?: AbortSignal) => Promise<Release>
}

export interface ReleaseEventStream {
  addEventListener: (type: string, listener: EventListener) => void
  close: () => void
}

export interface ReleaseStateModel {
  phase: 'idle' | 'checking' | 'checked' | 'queuing' | 'tracking'
  stream: 'closed' | 'connecting' | 'live' | 'reconnecting'
  check: PublishCheck | null
  release: Release | null
  error: ReleaseErrorCode
}

export type ReleaseErrorCode =
  | ''
  | 'session_expired'
  | 'check_failed'
  | 'queue_failed'
  | 'refresh_failed'

export interface ReleaseStore {
  readonly state: ReleaseStateModel
  check: (workspace: WorkspaceDetail, diff: DiffResponse | null, hasUnsavedChanges: boolean) => Promise<PublishCheck>
  queue: (workspace: WorkspaceDetail, confirmName: string) => Promise<Release>
  resume: (releaseId: string) => Promise<Release>
  refresh: () => Promise<Release | null>
  reset: () => void
  dispose: () => void
}

type EventStreamFactory = (url: string) => ReleaseEventStream

export function createReleaseStore(
  client: ReleaseClient,
  sessions: SessionStore,
  createStream: EventStreamFactory = (url) => new EventSource(url, { withCredentials: true }),
): ReleaseStore {
  const state = reactive<ReleaseStateModel>({
    phase: 'idle',
    stream: 'closed',
    check: null,
    release: null,
    error: '',
  })
  let stream: ReleaseEventStream | null = null
  let checkPromise: Promise<PublishCheck> | null = null
  let queuePromise: Promise<Release> | null = null
  let refreshPromise: Promise<Release | null> | null = null

  const removeExpiryListener = sessions.onExpired(() => {
    closeStream()
    state.error = 'session_expired'
  })

  function csrfToken(): string {
    const token = sessions.state.session?.csrf_token
    if (sessions.state.phase !== 'authenticated' || token === undefined) {
      throw new Error('an authenticated session is required')
    }
    return token
  }

  async function check(
    workspace: WorkspaceDetail,
    diff: DiffResponse | null,
    hasUnsavedChanges: boolean,
  ): Promise<PublishCheck> {
    if (checkPromise !== null) return checkPromise
    if (workspace.state !== 'ready') throw new Error('a ready workspace is required')
    if (hasUnsavedChanges) throw new Error('unsaved documents must be saved before publication checks')
    if (diff === null || !diff.complete) throw new Error('a complete diff is required before publication checks')
    if (!diff.files.some(({ status }) => status !== 'unchanged')) {
      throw new Error('the workspace has no publishable changes')
    }
    const token = csrfToken()
    closeStream()
    state.phase = 'checking'
    state.check = null
    state.release = null
    state.error = ''
    checkPromise = client
      .createPublishCheck(workspace.id, workspace.draft_etag, token)
      .then((result) => {
        state.check = result
        state.phase = 'checked'
        return result
      })
      .catch((error: unknown) => {
        if (!sessions.handleAPIError(error)) state.error = 'check_failed'
        state.phase = 'idle'
        throw error
      })
      .finally(() => {
        checkPromise = null
      })
    return checkPromise
  }

  async function queue(workspace: WorkspaceDetail, confirmName: string): Promise<Release> {
    if (queuePromise !== null) return queuePromise
    const candidate = state.check
    if (
      candidate === null ||
      candidate.state !== 'valid' ||
      candidate.workspace_id !== workspace.id ||
      Date.parse(candidate.expires_at) <= Date.now()
    ) {
      throw new Error('a current valid publication check is required')
    }
    if (workspace.state !== 'ready' || confirmName !== workspace.name) {
      throw new Error('the named workspace confirmation does not match')
    }
    const token = csrfToken()
    state.phase = 'queuing'
    state.error = ''
    queuePromise = client
      .createRelease(workspace.id, candidate.id, confirmName, workspace.draft_etag, token)
      .then((release) => {
        state.release = release
        state.phase = 'tracking'
        connect(release.id)
        return release
      })
      .catch((error: unknown) => {
        if (!sessions.handleAPIError(error)) state.error = 'queue_failed'
        state.phase = 'checked'
        throw error
      })
      .finally(() => {
        queuePromise = null
      })
    return queuePromise
  }

  async function resume(releaseId: string): Promise<Release> {
    state.error = ''
    const release = await client.getRelease(releaseId)
    state.release = release
    state.phase = 'tracking'
    if (isTerminalRelease(release)) closeStream()
    else connect(release.id)
    return release
  }

  function refresh(): Promise<Release | null> {
    if (refreshPromise !== null) return refreshPromise
    const releaseId = state.release?.id
    if (releaseId === undefined) return Promise.resolve(null)
    refreshPromise = client
      .getRelease(releaseId)
      .then((release) => {
        state.release = release
        state.phase = 'tracking'
        if (isTerminalRelease(release)) closeStream()
        return release
      })
      .catch((error: unknown) => {
        if (!sessions.handleAPIError(error)) state.error = 'refresh_failed'
        throw error
      })
      .finally(() => {
        refreshPromise = null
      })
    return refreshPromise
  }

  function connect(releaseId: string): void {
    closeStream()
    state.stream = 'connecting'
    const current = createStream(`/api/v1/config/releases/${releaseId}/events`)
    stream = current
    current.addEventListener('open', () => {
      if (stream === current) state.stream = 'live'
    })
    for (const event of ['snapshot', 'stage', 'terminal']) {
      current.addEventListener(event, () => {
        if (stream === current) void refresh()
      })
    }
    current.addEventListener('error', () => {
      if (stream === current && !isTerminalRelease(state.release)) state.stream = 'reconnecting'
    })
  }

  function closeStream(): void {
    stream?.close()
    stream = null
    state.stream = 'closed'
  }

  function reset(): void {
    closeStream()
    state.phase = 'idle'
    state.check = null
    state.release = null
    state.error = ''
  }

  function dispose(): void {
    closeStream()
    removeExpiryListener()
  }

  return { state, check, queue, resume, refresh, reset, dispose }
}

export function isTerminalRelease(release: Release | null): boolean {
  return (
    release !== null &&
    ['succeeded', 'failed', 'rolled_back', 'needs_attention', 'cancelled'].includes(release.state)
  )
}

export const releaseStore = createReleaseStore(apiClient, sessionStore)

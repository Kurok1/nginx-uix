/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */
import { reactive } from 'vue'

import { apiClient } from './api/client'
import {
  isTerminalRouteRun,
  type RouteAnalysis,
  type RouteHistoryPage,
  type RouteHistoryQuery,
  type RouteTestRequest,
  type RouteTestRun,
} from './api/route_lab'
import type { WorkspaceDetail } from './api/types'
import { sessionStore, type SessionStore } from './session'

export const ROUTE_SIDE_EFFECT_CONFIRMATION = 'RUN SIDE-EFFECTING REQUEST'

export interface RouteLabClient {
  analyzeRoute: (
    workspaceId: string,
    input: RouteTestRequest,
    etag: string,
    csrfToken: string,
    signal?: AbortSignal,
  ) => Promise<RouteAnalysis>
  createRouteTest: (
    workspaceId: string,
    input: RouteTestRequest,
    etag: string,
    csrfToken: string,
    signal?: AbortSignal,
  ) => Promise<RouteTestRun>
  getRouteTest: (id: string, signal?: AbortSignal) => Promise<RouteTestRun>
  listRouteTests: (query?: RouteHistoryQuery, signal?: AbortSignal) => Promise<RouteHistoryPage>
  cancelRouteTest: (
    id: string,
    csrfToken: string,
    signal?: AbortSignal,
  ) => Promise<RouteTestRun>
}

export interface RouteLabEventStream {
  addEventListener: (type: string, listener: EventListener) => void
  close: () => void
}

export type RouteLabErrorCode =
  | ''
  | 'session_expired'
  | 'analysis_failed'
  | 'queue_failed'
  | 'run_failed'
  | 'progress_failed'
  | 'cancellation_failed'
  | 'history_failed'

export interface RouteLabState {
  phase: 'idle' | 'analyzing' | 'ready' | 'queuing' | 'tracking'
  stream: 'closed' | 'connecting' | 'live' | 'reconnecting'
  analysis: RouteAnalysis | null
  analysisWorkspaceId: string
  analysisETag: string
  activeRun: RouteTestRun | null
  history: RouteTestRun[]
  historyCursor: string
  historyLoading: boolean
  historyWorkspaceId: string
  error: RouteLabErrorCode
  historyError: RouteLabErrorCode
}

export interface RouteLabStore {
  readonly state: RouteLabState
  analyze: (workspace: WorkspaceDetail, input: RouteTestRequest) => Promise<RouteAnalysis>
  queue: (
    workspace: WorkspaceDetail,
    input: RouteTestRequest,
    confirmation: string,
  ) => Promise<RouteTestRun>
  resume: (id: string) => Promise<RouteTestRun>
  refresh: () => Promise<RouteTestRun | null>
  cancel: () => Promise<RouteTestRun | null>
  loadHistory: (workspaceId?: string, append?: boolean) => Promise<RouteHistoryPage>
  clearAnalysis: () => void
  dispose: () => void
}

type EventStreamFactory = (url: string) => RouteLabEventStream

export function createRouteLabStore(
  client: RouteLabClient,
  sessions: SessionStore,
  createStream: EventStreamFactory = (url) => new EventSource(url, { withCredentials: true }),
): RouteLabStore {
  const state = reactive<RouteLabState>({
    phase: 'idle',
    stream: 'closed',
    analysis: null,
    analysisWorkspaceId: '',
    analysisETag: '',
    activeRun: null,
    history: [],
    historyCursor: '',
    historyLoading: false,
    historyWorkspaceId: '',
    error: '',
    historyError: '',
  })
  let stream: RouteLabEventStream | null = null
  let analysisPromise: Promise<RouteAnalysis> | null = null
  let queuePromise: Promise<RouteTestRun> | null = null
  let refreshPromise: Promise<RouteTestRun | null> | null = null
  let cancellationPromise: Promise<RouteTestRun | null> | null = null
  let historyPromise: Promise<RouteHistoryPage> | null = null

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

  function requireReadyWorkspace(workspace: WorkspaceDetail): void {
    if (workspace.state !== 'ready') {
      throw new Error('a ready workspace is required')
    }
  }

  function analyze(workspace: WorkspaceDetail, input: RouteTestRequest): Promise<RouteAnalysis> {
    if (analysisPromise !== null) return analysisPromise
    requireReadyWorkspace(workspace)
    state.phase = 'analyzing'
    state.error = ''
    const request = cloneRequest(input, '')
    analysisPromise = client
      .analyzeRoute(workspace.id, request, workspace.draft_etag, csrfToken())
      .then((result) => {
        state.analysis = result
        state.analysisWorkspaceId = workspace.id
        state.analysisETag = workspace.draft_etag
        state.phase = state.activeRun === null ? 'ready' : 'tracking'
        return result
      })
      .catch((error: unknown) => {
        if (!sessions.handleAPIError(error)) {
          state.error = 'analysis_failed'
        }
        state.phase = state.activeRun === null ? 'idle' : 'tracking'
        throw error
      })
      .finally(() => {
        analysisPromise = null
      })
    return analysisPromise
  }

  function queue(
    workspace: WorkspaceDetail,
    input: RouteTestRequest,
    confirmation: string,
  ): Promise<RouteTestRun> {
    if (queuePromise !== null) return queuePromise
    requireReadyWorkspace(workspace)
    if (requiresRouteConfirmation(input) && confirmation !== ROUTE_SIDE_EFFECT_CONFIRMATION) {
      return Promise.reject(new Error('the exact confirmation is required for this request'))
    }
    state.phase = 'queuing'
    state.error = ''
    const request = cloneRequest(input, confirmation)
    queuePromise = client
      .createRouteTest(workspace.id, request, workspace.draft_etag, csrfToken())
      .then((run) => {
        state.activeRun = run
        state.analysis = run.static_analysis
        state.analysisWorkspaceId = run.workspace_id
        state.analysisETag = run.workspace_etag
        state.phase = 'tracking'
        upsertHistory(run)
        connect(run.id)
        return run
      })
      .catch((error: unknown) => {
        if (!sessions.handleAPIError(error)) {
          state.error = 'queue_failed'
        }
        state.phase = state.analysis === null ? 'idle' : 'ready'
        throw error
      })
      .finally(() => {
        queuePromise = null
      })
    return queuePromise
  }

  async function resume(id: string): Promise<RouteTestRun> {
    state.error = ''
    try {
      const run = await client.getRouteTest(id)
      state.activeRun = run
      state.analysis = run.static_analysis
      state.analysisWorkspaceId = run.workspace_id
      state.analysisETag = run.workspace_etag
      state.phase = 'tracking'
      upsertHistory(run)
      if (isTerminalRouteRun(run)) closeStream()
      else connect(run.id)
      return run
    } catch (error: unknown) {
      if (!sessions.handleAPIError(error)) state.error = 'run_failed'
      throw error
    }
  }

  function refresh(): Promise<RouteTestRun | null> {
    if (refreshPromise !== null) return refreshPromise
    const id = state.activeRun?.id
    if (id === undefined) return Promise.resolve(null)
    refreshPromise = client
      .getRouteTest(id)
      .then((run) => {
        state.activeRun = run
        state.analysis = run.static_analysis
        state.analysisWorkspaceId = run.workspace_id
        state.analysisETag = run.workspace_etag
        state.phase = 'tracking'
        upsertHistory(run)
        if (isTerminalRouteRun(run)) closeStream()
        return run
      })
      .catch((error: unknown) => {
        if (!sessions.handleAPIError(error)) {
          state.error = 'progress_failed'
        }
        throw error
      })
      .finally(() => {
        refreshPromise = null
      })
    return refreshPromise
  }

  function cancel(): Promise<RouteTestRun | null> {
    if (cancellationPromise !== null) return cancellationPromise
    const run = state.activeRun
    if (run === null || isTerminalRouteRun(run)) return Promise.resolve(run)
    state.error = ''
    cancellationPromise = client
      .cancelRouteTest(run.id, csrfToken())
      .then((updated) => {
        state.activeRun = updated
        upsertHistory(updated)
        if (isTerminalRouteRun(updated)) closeStream()
        return updated
      })
      .catch((error: unknown) => {
        if (!sessions.handleAPIError(error)) {
          state.error = 'cancellation_failed'
        }
        throw error
      })
      .finally(() => {
        cancellationPromise = null
      })
    return cancellationPromise
  }

  function loadHistory(workspaceId = '', append = false): Promise<RouteHistoryPage> {
    if (historyPromise !== null) return historyPromise
    const cursor = append && state.historyWorkspaceId === workspaceId ? state.historyCursor : ''
    state.historyLoading = true
    state.historyError = ''
    historyPromise = client
      .listRouteTests({
        ...(workspaceId === '' ? {} : { workspace_id: workspaceId }),
        ...(cursor === '' ? {} : { cursor }),
        limit: 20,
      })
      .then((page) => {
        state.history = append && state.historyWorkspaceId === workspaceId
          ? mergeHistory(state.history, page.runs)
          : [...page.runs]
        state.historyCursor = page.next_cursor ?? ''
        state.historyWorkspaceId = workspaceId
        return page
      })
      .catch((error: unknown) => {
        if (!sessions.handleAPIError(error)) {
          state.historyError = 'history_failed'
        }
        throw error
      })
      .finally(() => {
        state.historyLoading = false
        historyPromise = null
      })
    return historyPromise
  }

  function connect(id: string): void {
    closeStream()
    state.stream = 'connecting'
    const current = createStream(`/api/v1/route-tests/${id}/events`)
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
      if (stream === current && !isTerminalRouteRun(state.activeRun)) {
        state.stream = 'reconnecting'
      }
    })
  }

  function closeStream(): void {
    stream?.close()
    stream = null
    state.stream = 'closed'
  }

  function upsertHistory(run: RouteTestRun): void {
    const index = state.history.findIndex(({ id }) => id === run.id)
    if (index === -1) {
      state.history = [run, ...state.history]
      return
    }
    state.history[index] = run
  }

  function clearAnalysis(): void {
    state.analysis = null
    state.analysisWorkspaceId = ''
    state.analysisETag = ''
    state.error = ''
    if (state.activeRun === null) state.phase = 'idle'
  }

  function dispose(): void {
    closeStream()
    removeExpiryListener()
  }

  return {
    state,
    analyze,
    queue,
    resume,
    refresh,
    cancel,
    loadHistory,
    clearAnalysis,
    dispose,
  }
}

export function requiresRouteConfirmation(input: RouteTestRequest): boolean {
  return input.body !== '' || !['GET', 'HEAD', 'OPTIONS'].includes(input.method)
}

function cloneRequest(input: RouteTestRequest, confirmation: string): RouteTestRequest {
  return {
    ...input,
    headers: input.headers.map((header) => ({ ...header })),
    assertions: { ...input.assertions },
    confirmation,
  }
}

function mergeHistory(existing: readonly RouteTestRun[], incoming: readonly RouteTestRun[]): RouteTestRun[] {
  const ids = new Set(existing.map(({ id }) => id))
  return [...existing, ...incoming.filter(({ id }) => !ids.has(id))]
}

export const routeLabStore = createRouteLabStore(apiClient, sessionStore)

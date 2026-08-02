/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { reactive } from 'vue'

import { APIRequestError } from './api/client'
import type {
  ConfigDependency,
  ConfigFile,
  ConfigTreeNode,
  ConfigTree,
  DiffResponse,
  FileMutationResponse,
  GroupCollection,
  GroupMutationRequest,
  SearchResponse,
  SessionResponse,
  WorkspaceDetail,
  WorkspaceSummary,
} from './api/types'
import type { SessionStore } from './session'
import { createWorkspaceStore, type WorkspaceClient } from './workspace'

const workspaceID = '0123456789abcdef0123456789abcdef'
const otherWorkspaceID = 'fedcba9876543210fedcba9876543210'
const digestA = 'a'.repeat(64)
const digestB = 'b'.repeat(64)
const draftETagA = `"draft-v1:${digestA}"`
const draftETagB = `"draft-v1:${digestB}"`
const groupsETagA = `"groups-v1:${digestA}"`
const groupsETagB = `"groups-v1:${digestB}"`

function workspaceFixture(
  overrides: Partial<WorkspaceDetail> = {},
): WorkspaceDetail {
  return {
    id: workspaceID,
    name: 'Primary workspace',
    state: 'ready',
    production_digest: digestA,
    base_digest: digestA,
    draft_etag: draftETagA,
    entry_count: 1,
    managed_bytes: 24,
    workspace_bytes: 128,
    created_by: 7,
    created_at: '2026-07-17T08:00:00Z',
    updated_at: '2026-07-17T08:01:00Z',
    ...overrides,
  }
}

function treeEntryFixture(
  path = 'conf.d/site.conf',
  digest = digestA,
  diffStatus: ConfigTreeNode['diff_status'] = 'unchanged',
): ConfigTreeNode {
  return {
    path,
    name: path.split('/').at(-1) ?? path,
    entry_type: 'regular',
    managed: true,
    read_only: false,
    status_reason_code: 'managed_text',
    size_bytes: 24,
    content_digest: digest,
    diff_status: diffStatus,
  }
}

function treeFixture(etag = draftETagA): ConfigTree {
  return {
    entries: [treeEntryFixture()],
    dependencies: [dependencyFixture()],
    draft_etag: etag,
  }
}

function dependencyFixture(): ConfigDependency {
  return {
    source: 'nginx.conf',
    line: 7,
    column: 5,
    display_value: 'conf.d/*.conf',
    target: 'conf.d/site.conf',
    status: 'resolved',
    cycle: false,
  }
}

function fileFixture(
  path = 'conf.d/site.conf',
  content = 'server { listen 80; }\n',
  etag = draftETagA,
): ConfigFile {
  return {
    path,
    content,
    size_bytes: new TextEncoder().encode(content).length,
    content_digest: digestA,
    line_ending: content.includes('\r\n') ? 'crlf' : 'lf',
    draft_etag: etag,
  }
}

function mutationFixture(
  path = 'conf.d/site.conf',
  includeEntry = true,
): FileMutationResponse {
  const result: FileMutationResponse = {
    workspace: workspaceFixture({ draft_etag: draftETagB }),
    draft_etag: draftETagB,
  }
  if (includeEntry) {
    result.entry = treeEntryFixture(path, digestB, 'modified')
  }
  return result
}

function searchFixture(snippet = 'server {'): SearchResponse {
  return {
    matches: [{ path: 'conf.d/site.conf', line: 1, column: 1, snippet }],
    complete: true,
  }
}

function diffFixture(path = 'conf.d/site.conf'): DiffResponse {
  return {
    files: [{ path, status: 'modified', added_lines: 1, removed_lines: 1 }],
    complete: true,
    reason: '',
    patch: '',
  }
}

function groupsFixture(etag = groupsETagA): GroupCollection {
  return {
    groups: [
      {
        id: otherWorkspaceID,
        name: 'Sites',
        sort_order: 1,
        members: ['conf.d/site.conf'],
        missing: [],
        created_by: 7,
        created_at: '2026-07-17T08:00:00Z',
        updated_at: '2026-07-17T08:01:00Z',
      },
    ],
    groups_etag: etag,
  }
}

type WorkspaceClientMock = {
  listWorkspaces: ReturnType<typeof vi.fn<() => Promise<WorkspaceSummary[]>>>
  createWorkspace: ReturnType<
    typeof vi.fn<(name: string, csrfToken: string, signal?: AbortSignal) => Promise<WorkspaceDetail>>
  >
  getWorkspace: ReturnType<typeof vi.fn<(id: string, signal?: AbortSignal) => Promise<WorkspaceDetail>>>
  deleteWorkspace: ReturnType<
    typeof vi.fn<(id: string, confirmName: string, etag: string, csrfToken: string) => Promise<void>>
  >
  getConfigTree: ReturnType<typeof vi.fn<(id: string, signal?: AbortSignal) => Promise<ConfigTree>>>
  getConfigFile: ReturnType<typeof vi.fn<(id: string, path: string, signal?: AbortSignal) => Promise<ConfigFile>>>
  createConfigFile: ReturnType<
    typeof vi.fn<
      (id: string, path: string, content: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
    >
  >
  replaceConfigFile: ReturnType<
    typeof vi.fn<
      (id: string, path: string, content: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
    >
  >
  copyConfigFile: ReturnType<
    typeof vi.fn<
      (id: string, sourcePath: string, destinationPath: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
    >
  >
  renameConfigFile: ReturnType<
    typeof vi.fn<
      (id: string, sourcePath: string, destinationPath: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
    >
  >
  deleteConfigFile: ReturnType<
    typeof vi.fn<
      (id: string, path: string, confirmPath: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
    >
  >
  searchConfigFiles: ReturnType<
    typeof vi.fn<(id: string, query: string, signal?: AbortSignal) => Promise<SearchResponse>>
  >
  getConfigDiff: ReturnType<
    typeof vi.fn<(id: string, path?: string, signal?: AbortSignal) => Promise<DiffResponse>>
  >
  listConfigGroups: ReturnType<
    typeof vi.fn<(workspaceId?: string, signal?: AbortSignal) => Promise<GroupCollection>>
  >
  createConfigGroup: ReturnType<
    typeof vi.fn<
      (input: GroupMutationRequest, etag: string, csrfToken: string) => Promise<GroupCollection>
    >
  >
  replaceConfigGroup: ReturnType<
    typeof vi.fn<
      (id: string, input: GroupMutationRequest, etag: string, csrfToken: string) => Promise<GroupCollection>
    >
  >
  deleteConfigGroup: ReturnType<
    typeof vi.fn<(id: string, confirmName: string, etag: string, csrfToken: string) => Promise<GroupCollection>>
  >
}

function workspaceClientStub(): WorkspaceClient & WorkspaceClientMock {
  return {
    listWorkspaces: vi.fn<() => Promise<WorkspaceSummary[]>>().mockResolvedValue([workspaceFixture()]),
    createWorkspace: vi
      .fn<(name: string, csrfToken: string, signal?: AbortSignal) => Promise<WorkspaceDetail>>()
      .mockResolvedValue(workspaceFixture({ id: otherWorkspaceID, name: 'Review changes' })),
    getWorkspace: vi
      .fn<(id: string, signal?: AbortSignal) => Promise<WorkspaceDetail>>()
      .mockResolvedValue(workspaceFixture()),
    deleteWorkspace: vi
      .fn<(id: string, confirmName: string, etag: string, csrfToken: string) => Promise<void>>()
      .mockResolvedValue(undefined),
    getConfigTree: vi
      .fn<(id: string, signal?: AbortSignal) => Promise<ConfigTree>>()
      .mockResolvedValue(treeFixture()),
    getConfigFile: vi
      .fn<(id: string, path: string, signal?: AbortSignal) => Promise<ConfigFile>>()
      .mockImplementation(async (_id, path) => fileFixture(path)),
    replaceConfigFile: vi
      .fn<
        (id: string, path: string, content: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
      >()
      .mockResolvedValue(mutationFixture()),
    createConfigFile: vi
      .fn<
        (id: string, path: string, content: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
      >()
      .mockImplementation(async (_id, path) => mutationFixture(path)),
    copyConfigFile: vi
      .fn<
        (id: string, sourcePath: string, destinationPath: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
      >()
      .mockImplementation(async (_id, _sourcePath, destinationPath) => mutationFixture(destinationPath)),
    renameConfigFile: vi
      .fn<
        (id: string, sourcePath: string, destinationPath: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
      >()
      .mockImplementation(async (_id, _sourcePath, destinationPath) => mutationFixture(destinationPath)),
    deleteConfigFile: vi
      .fn<
        (id: string, path: string, confirmPath: string, etag: string, csrfToken: string) => Promise<FileMutationResponse>
      >()
      .mockResolvedValue(mutationFixture('conf.d/site.conf', false)),
    searchConfigFiles: vi
      .fn<(id: string, query: string, signal?: AbortSignal) => Promise<SearchResponse>>()
      .mockResolvedValue(searchFixture()),
    getConfigDiff: vi
      .fn<(id: string, path?: string, signal?: AbortSignal) => Promise<DiffResponse>>()
      .mockResolvedValue(diffFixture()),
    listConfigGroups: vi
      .fn<(workspaceId?: string, signal?: AbortSignal) => Promise<GroupCollection>>()
      .mockResolvedValue(groupsFixture()),
    createConfigGroup: vi
      .fn<(input: GroupMutationRequest, etag: string, csrfToken: string) => Promise<GroupCollection>>()
      .mockResolvedValue(groupsFixture(groupsETagB)),
    replaceConfigGroup: vi
      .fn<
        (id: string, input: GroupMutationRequest, etag: string, csrfToken: string) => Promise<GroupCollection>
      >()
      .mockResolvedValue(groupsFixture(groupsETagB)),
    deleteConfigGroup: vi
      .fn<(id: string, confirmName: string, etag: string, csrfToken: string) => Promise<GroupCollection>>()
      .mockResolvedValue({ groups: [], groups_etag: groupsETagB }),
  }
}

function deferred<T>(): {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (reason: unknown) => void
} {
  let resolvePromise: ((value: T) => void) | undefined
  let rejectPromise: ((reason: unknown) => void) | undefined
  const promise = new Promise<T>((resolve, reject) => {
    resolvePromise = resolve
    rejectPromise = reject
  })
  return {
    promise,
    resolve(value) {
      if (resolvePromise === undefined) throw new Error('deferred resolver is unavailable')
      resolvePromise(value)
    },
    reject(reason) {
      if (rejectPromise === undefined) throw new Error('deferred rejecter is unavailable')
      rejectPromise(reason)
    },
  }
}

function currentSession(): SessionResponse {
  return {
    user: { id: 7, username: 'operator', created_at: '2026-07-17T08:00:00Z' },
    csrf_token: 'csrf-1',
    created_at: '2026-07-17T08:30:00Z',
    last_seen_at: '2026-07-17T09:00:00Z',
    idle_expires_at: '2026-07-17T10:00:00Z',
    absolute_expires_at: '2026-07-17T18:00:00Z',
  }
}

function sessionStoreStub(): SessionStore & { expire: () => void } {
  const state = reactive({ phase: 'authenticated' as const, session: currentSession() })
  const listeners = new Set<() => void>()
  return {
    state,
    handleAPIError: vi.fn(() => false),
    login: vi.fn(async () => undefined),
    logout: vi.fn(async () => undefined),
    restore: vi.fn(async () => undefined),
    onExpired(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    expire() {
      state.phase = 'anonymous' as never
      state.session = null as never
      for (const listener of listeners) listener()
    },
  }
}

function configFailure(code: 'CONFIG_WORKSPACE_CONFLICT' | 'CONFIG_WORKSPACE_STALE' | 'CONFIG_WORKSPACE_NEEDS_ATTENTION' | 'AGENT_UNAVAILABLE', details?: Record<string, string>): APIRequestError {
  return new APIRequestError({
    kind: 'api',
    message: code,
    status: code === 'AGENT_UNAVAILABLE' ? 503 : 409,
    apiError: { code, message: code, request_id: 'request-1', ...(details === undefined ? {} : { details }) },
  })
}

describe('in-memory configuration workspace store', () => {
  it('loads the bounded list then opens workspace metadata and tree', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())

    await store.loadWorkspaces()
    await store.openWorkspace(workspaceID)

    expect(store.state.phase).toBe('ready')
    expect(store.state.workspaces).toEqual([workspaceFixture()])
    expect(store.state.active).toEqual(workspaceFixture())
    expect(store.state.tree).toEqual(treeFixture().entries)
    expect(store.state.dependencies).toEqual(treeFixture().dependencies)
    expect(client.getWorkspace).toHaveBeenCalledWith(workspaceID, expect.any(AbortSignal))
    expect(client.getConfigTree).toHaveBeenCalledWith(workspaceID, expect.any(AbortSignal))
  })

  it('opens a published workspace and keeps its historical draft available for reading', async () => {
    const client = workspaceClientStub()
    const published = workspaceFixture({
      state: 'published',
      last_release_id: otherWorkspaceID,
    })
    client.getWorkspace.mockResolvedValueOnce(published)
    const store = createWorkspaceStore(client, sessionStoreStub())

    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')

    expect(store.state.phase).toBe('ready')
    expect(store.state.active).toEqual(published)
    expect(store.state.tree).toEqual(treeFixture().entries)
    expect(store.document('conf.d/site.conf')?.serverContent).toBe('server { listen 80; }\n')
    expect(client.getConfigTree).toHaveBeenCalledWith(workspaceID, expect.any(AbortSignal))
    expect(client.getConfigFile).toHaveBeenCalledWith(
      workspaceID,
      'conf.d/site.conf',
      expect.any(AbortSignal),
    )
  })

  it('keeps multiple open tabs and refuses to close a dirty tab without explicit discard', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    await store.openFile('conf.d/other.conf')

    store.updateDocument('conf.d/site.conf', 'server { listen 8080; }\n')

    expect(store.state.documents.map(({ path }) => path)).toEqual([
      'conf.d/site.conf',
      'conf.d/other.conf',
    ])
    expect(store.closeFile('conf.d/site.conf')).toBe(false)
    expect(store.closeFile('conf.d/site.conf', true)).toBe(true)
    expect(store.document('conf.d/site.conf')).toBeUndefined()
  })

  it('calculates dirty state from exact text without normalizing CRLF or final newline', async () => {
    const client = workspaceClientStub()
    client.getConfigFile.mockResolvedValue(fileFixture('conf.d/site.conf', 'server {}\r\n'))
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')

    store.updateDocument('conf.d/site.conf', 'server {}\n')
    expect(store.document('conf.d/site.conf')).toMatchObject({ dirty: true, lineEnding: 'crlf' })
    expect(store.hasUnsavedChanges()).toBe(true)

    store.updateDocument('conf.d/site.conf', 'server {}\r\n')
    expect(store.document('conf.d/site.conf')?.dirty).toBe(false)
    expect(store.hasUnsavedChanges()).toBe(false)
  })

  it('exposes the route-leave decision without discarding dirty document memory', async () => {
    const store = createWorkspaceStore(workspaceClientStub(), sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')

    expect(store.hasUnsavedChanges()).toBe(false)
    store.updateDocument('conf.d/site.conf', 'local route recovery text')

    expect(store.hasUnsavedChanges()).toBe(true)
    expect(store.document('conf.d/site.conf')?.content).toBe('local route recovery text')
  })

  it('saves only by explicit action using current CSRF and workspace ETag', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'server { listen 8081; }\n')

    await store.saveFile('conf.d/site.conf')

    expect(client.replaceConfigFile).toHaveBeenCalledTimes(1)
    expect(client.replaceConfigFile).toHaveBeenCalledWith(
      workspaceID,
      'conf.d/site.conf',
      'server { listen 8081; }\n',
      draftETagA,
      'csrf-1',
    )
    expect(store.document('conf.d/site.conf')).toMatchObject({
      content: 'server { listen 8081; }\n',
      serverContent: 'server { listen 8081; }\n',
      contentDigest: digestB,
      dirty: false,
      requiresRefresh: false,
    })
    expect(store.state.active?.draft_etag).toBe(draftETagB)
  })

  it('keeps local text after conflict and requires a fresh server read before saving', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'server { listen 8081; }\n')
    client.replaceConfigFile.mockRejectedValueOnce(
      configFailure('CONFIG_WORKSPACE_CONFLICT', { current_etag: draftETagB }),
    )

    await expect(store.saveFile('conf.d/site.conf')).rejects.toMatchObject({
      apiError: { code: 'CONFIG_WORKSPACE_CONFLICT' },
    })

    expect(client.replaceConfigFile).toHaveBeenCalledTimes(1)
    expect(store.document('conf.d/site.conf')?.content).toContain('8081')
    expect(store.document('conf.d/site.conf')?.requiresRefresh).toBe(true)
    expect(store.state.banner?.kind).toBe('conflict')
    expect(store.canSave('conf.d/site.conf')).toBe(false)
  })

  it.each([
    ['stale', 'CONFIG_WORKSPACE_STALE', 'stale'],
    ['needs_attention', 'CONFIG_WORKSPACE_NEEDS_ATTENTION', 'needs_attention'],
  ] as const)('makes a %s workspace read-only after the stable API error', async (state, code, banner) => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'local text')
    client.replaceConfigFile.mockRejectedValueOnce(configFailure(code))

    await expect(store.saveFile('conf.d/site.conf')).rejects.toMatchObject({ apiError: { code } })

    expect(store.state.active?.state).toBe(state)
    expect(store.state.banner?.kind).toBe(banner)
    expect(store.document('conf.d/site.conf')?.content).toBe('local text')
    expect(store.canSave('conf.d/site.conf')).toBe(false)
  })

  it('copies local content only through the explicit clipboard action', async () => {
    const writeText = vi.fn<(value: string) => Promise<void>>().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const store = createWorkspaceStore(workspaceClientStub(), sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'local recovery text')

    await expect(store.copyLocalContent('conf.d/site.conf')).resolves.toBe(true)
    expect(writeText).toHaveBeenCalledWith('local recovery text')
  })

  it('reloads the server version only after the named action', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'local recovery text')
    client.getConfigFile.mockResolvedValueOnce(fileFixture('conf.d/site.conf', 'server fresh\n', draftETagB))

    expect(store.document('conf.d/site.conf')?.content).toBe('local recovery text')
    await store.reloadFile('conf.d/site.conf')

    expect(store.document('conf.d/site.conf')).toMatchObject({
      content: 'server fresh\n',
      serverContent: 'server fresh\n',
      dirty: false,
      requiresRefresh: false,
    })
    expect(store.state.active?.draft_etag).toBe(draftETagB)
  })

  it('preserves global dirty memory on session expiry and refreshes metadata without sending text after login', async () => {
    const client = workspaceClientStub()
    const session = sessionStoreStub()
    const store = createWorkspaceStore(client, session)
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'local recovery text')

    session.expire()

    expect(store.document('conf.d/site.conf')).toMatchObject({
      content: 'local recovery text',
      dirty: true,
      requiresRefresh: true,
    })
    expect(store.state.banner?.kind).toBe('session_expired')
    expect(store.canSave('conf.d/site.conf')).toBe(false)

    session.state.phase = 'authenticated'
    session.state.session = currentSession()
    client.getWorkspace.mockResolvedValueOnce(workspaceFixture({ draft_etag: draftETagB }))
    client.getConfigTree.mockResolvedValueOnce(treeFixture(draftETagB))
    await store.markSessionRestored()

    expect(client.getWorkspace).toHaveBeenCalledTimes(2)
    expect(client.getConfigTree).toHaveBeenCalledTimes(2)
    expect(client.replaceConfigFile).not.toHaveBeenCalled()
    expect(client.getConfigFile).toHaveBeenCalledTimes(1)
    expect(store.state.dependencies).toEqual(treeFixture(draftETagB).dependencies)
    expect(store.document('conf.d/site.conf')?.content).toBe('local recovery text')
    expect(store.canSave('conf.d/site.conf')).toBe(false)
    expect(store.state.banner?.kind).toBe('conflict')

    await store.openWorkspace(workspaceID)

    expect(store.document('conf.d/site.conf')).toMatchObject({
      content: 'local recovery text',
      dirty: true,
      requiresRefresh: true,
    })
    expect(store.state.banner?.kind).toBe('conflict')
  })

  it('cancels an obsolete workspace open and never installs its late result', async () => {
    const client = workspaceClientStub()
    let firstSignal: AbortSignal | undefined
    client.getWorkspace.mockImplementation(async (id, signal) => {
      if (id === workspaceID) {
        firstSignal = signal
        await new Promise<void>((resolve) => signal?.addEventListener('abort', () => resolve(), { once: true }))
        throw new DOMException('aborted', 'AbortError')
      }
      return workspaceFixture({ id: otherWorkspaceID, name: 'Other workspace' })
    })
    client.getConfigTree.mockResolvedValue(treeFixture())
    const store = createWorkspaceStore(client, sessionStoreStub())

    const obsolete = store.openWorkspace(workspaceID)
    await store.openWorkspace(otherWorkspaceID)
    await expect(obsolete).resolves.toBeUndefined()

    expect(firstSignal?.aborted).toBe(true)
    expect(store.state.active?.id).toBe(otherWorkspaceID)
    expect(client.getWorkspace).toHaveBeenCalledTimes(2)
  })

  it('keeps agent unavailability inline without retrying automatically', async () => {
    const client = workspaceClientStub()
    client.getWorkspace.mockRejectedValueOnce(configFailure('AGENT_UNAVAILABLE'))
    const store = createWorkspaceStore(client, sessionStoreStub())

    await expect(store.openWorkspace(workspaceID)).rejects.toMatchObject({
      apiError: { code: 'AGENT_UNAVAILABLE' },
    })

    expect(client.getWorkspace).toHaveBeenCalledTimes(1)
    expect(store.state.phase).toBe('error')
    expect(store.state.banner?.kind).toBe('agent_unavailable')
  })

  it('creates a workspace with session CSRF and refreshes the bounded list', async () => {
    const client = workspaceClientStub()
    const created = workspaceFixture({ id: otherWorkspaceID, name: 'Review changes' })
    client.createWorkspace.mockResolvedValueOnce(created)
    client.listWorkspaces.mockResolvedValueOnce([created])
    const store = createWorkspaceStore(client, sessionStoreStub())

    await expect(store.createWorkspace('Review changes')).resolves.toEqual(created)

    expect(client.createWorkspace).toHaveBeenCalledWith(
      'Review changes',
      'csrf-1',
      expect.any(AbortSignal),
    )
    expect(client.listWorkspaces).toHaveBeenCalledTimes(1)
    expect(store.state.workspaces).toEqual([created])
    expect(store.state.pendingAction).toBeNull()
  })

  it('deletes the active named workspace with its current ETag and clears its page state', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.loadWorkspaces()
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')

    await store.deleteWorkspace(workspaceID, 'Primary workspace')

    expect(client.deleteWorkspace).toHaveBeenCalledWith(
      workspaceID,
      'Primary workspace',
      draftETagA,
      'csrf-1',
    )
    expect(store.state.workspaces).toEqual([])
    expect(store.state.active).toBeNull()
    expect(store.state.tree).toEqual([])
    expect(store.state.dependencies).toEqual([])
    expect(store.state.documents).toEqual([])
    expect(store.state.selectedPath).toBeNull()
  })

  it('creates a file without sending or discarding unrelated dirty editor text', async () => {
    const client = workspaceClientStub()
    client.createConfigFile.mockResolvedValueOnce(mutationFixture('conf.d/new.conf'))
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'local unsaved text\r\n')

    await store.createFile('conf.d/new.conf', 'server {}\r\n')

    expect(client.createConfigFile).toHaveBeenCalledWith(
      workspaceID,
      'conf.d/new.conf',
      'server {}\r\n',
      draftETagA,
      'csrf-1',
    )
    expect(store.state.active?.draft_etag).toBe(draftETagB)
    expect(store.state.tree).toContainEqual(treeEntryFixture('conf.d/new.conf', digestB, 'modified'))
    expect(store.document('conf.d/site.conf')).toMatchObject({
      content: 'local unsaved text\r\n',
      dirty: true,
    })
  })

  it('copies a file with current workspace authority and inserts the returned destination', async () => {
    const client = workspaceClientStub()
    client.copyConfigFile.mockResolvedValueOnce(mutationFixture('conf.d/site-copy.conf'))
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)

    await store.copyFile('conf.d/site.conf', 'conf.d/site-copy.conf')

    expect(client.copyConfigFile).toHaveBeenCalledWith(
      workspaceID,
      'conf.d/site.conf',
      'conf.d/site-copy.conf',
      draftETagA,
      'csrf-1',
    )
    expect(store.state.active?.draft_etag).toBe(draftETagB)
    expect(store.state.tree.map(({ path }) => path)).toContain('conf.d/site-copy.conf')
  })

  it('renames a file and moves only a clean open document while preserving exact text', async () => {
    const client = workspaceClientStub()
    client.getConfigFile.mockResolvedValueOnce(fileFixture('conf.d/site.conf', 'server {}\r\n'))
    client.renameConfigFile.mockResolvedValueOnce(mutationFixture('conf.d/renamed.conf'))
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')

    await store.renameFile('conf.d/site.conf', 'conf.d/renamed.conf')

    expect(client.renameConfigFile).toHaveBeenCalledWith(
      workspaceID,
      'conf.d/site.conf',
      'conf.d/renamed.conf',
      draftETagA,
      'csrf-1',
    )
    expect(store.state.tree.map(({ path }) => path)).toEqual(['conf.d/renamed.conf'])
    expect(store.document('conf.d/site.conf')).toBeUndefined()
    expect(store.document('conf.d/renamed.conf')).toMatchObject({
      content: 'server {}\r\n',
      serverContent: 'server {}\r\n',
      lineEnding: 'crlf',
      dirty: false,
      contentDigest: digestB,
    })
    expect(store.state.selectedPath).toBe('conf.d/renamed.conf')
  })

  it('deletes a file and closes only its clean open document', async () => {
    const client = workspaceClientStub()
    client.deleteConfigFile.mockResolvedValueOnce(mutationFixture('conf.d/site.conf', false))
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')

    await store.deleteFile('conf.d/site.conf', 'conf.d/site.conf')

    expect(client.deleteConfigFile).toHaveBeenCalledWith(
      workspaceID,
      'conf.d/site.conf',
      'conf.d/site.conf',
      draftETagA,
      'csrf-1',
    )
    expect(store.state.tree).toEqual([])
    expect(store.document('conf.d/site.conf')).toBeUndefined()
    expect(store.state.selectedPath).toBeNull()
  })

  it('rejects rename and delete before transport when the affected document is dirty', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'local dirty text\r\n')

    await expect(
      store.renameFile('conf.d/site.conf', 'conf.d/renamed.conf'),
    ).rejects.toThrow('dirty')
    await expect(store.deleteFile('conf.d/site.conf', 'conf.d/site.conf')).rejects.toThrow('dirty')

    expect(client.renameConfigFile).not.toHaveBeenCalled()
    expect(client.deleteConfigFile).not.toHaveBeenCalled()
    expect(store.document('conf.d/site.conf')).toMatchObject({
      content: 'local dirty text\r\n',
      dirty: true,
    })
    expect(store.state.pendingAction).toBeNull()
  })

  it('rejects deleting the active workspace while any document is dirty', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.loadWorkspaces()
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'local dirty text')

    await expect(store.deleteWorkspace(workspaceID, 'Primary workspace')).rejects.toThrow('dirty')

    expect(client.deleteWorkspace).not.toHaveBeenCalled()
    expect(store.state.active?.id).toBe(workspaceID)
    expect(store.document('conf.d/site.conf')?.content).toBe('local dirty text')
  })

  it.each([
    ['create', (store: ReturnType<typeof createWorkspaceStore>) => store.createFile('new.conf', '')],
    ['copy', (store: ReturnType<typeof createWorkspaceStore>) => store.copyFile('a.conf', 'b.conf')],
    ['rename', (store: ReturnType<typeof createWorkspaceStore>) => store.renameFile('conf.d/site.conf', 'renamed.conf')],
    ['delete', (store: ReturnType<typeof createWorkspaceStore>) => store.deleteFile('conf.d/site.conf', 'conf.d/site.conf')],
  ])('rejects %s file mutation while the active workspace is not ready', async (_name, action) => {
    const client = workspaceClientStub()
    client.getWorkspace.mockResolvedValueOnce(workspaceFixture({ state: 'stale' }))
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')

    await expect(action(store)).rejects.toThrow('ready workspace')

    expect(client.createConfigFile).not.toHaveBeenCalled()
    expect(client.copyConfigFile).not.toHaveBeenCalled()
    expect(client.renameConfigFile).not.toHaveBeenCalled()
    expect(client.deleteConfigFile).not.toHaveBeenCalled()
  })

  it('rejects every mutation after session expiry without calling transport', async () => {
    const client = workspaceClientStub()
    const session = sessionStoreStub()
    const store = createWorkspaceStore(client, session)
    await store.loadWorkspaces()
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    await store.loadGroups(workspaceID)
    session.expire()

    const groupInput: GroupMutationRequest = { name: 'Sites', sort_order: 1, members: [] }
    const actions = [
      () => store.createWorkspace('Review changes'),
      () => store.deleteWorkspace(workspaceID, 'Primary workspace'),
      () => store.createFile('new.conf', ''),
      () => store.copyFile('a.conf', 'b.conf'),
      () => store.renameFile('conf.d/site.conf', 'renamed.conf'),
      () => store.deleteFile('conf.d/site.conf', 'conf.d/site.conf'),
      () => store.createGroup(groupInput),
      () => store.replaceGroup(otherWorkspaceID, groupInput),
      () => store.deleteGroup(otherWorkspaceID, 'Sites'),
    ]

    for (const action of actions) {
      await expect(action()).rejects.toThrow('authenticated session')
    }
    expect(client.createWorkspace).not.toHaveBeenCalled()
    expect(client.deleteWorkspace).not.toHaveBeenCalled()
    expect(client.createConfigFile).not.toHaveBeenCalled()
    expect(client.copyConfigFile).not.toHaveBeenCalled()
    expect(client.renameConfigFile).not.toHaveBeenCalled()
    expect(client.deleteConfigFile).not.toHaveBeenCalled()
    expect(client.createConfigGroup).not.toHaveBeenCalled()
    expect(client.replaceConfigGroup).not.toHaveBeenCalled()
    expect(client.deleteConfigGroup).not.toHaveBeenCalled()
  })

  it('keeps mutations single-flight and clears pending state after success', async () => {
    const client = workspaceClientStub()
    const first = deferred<FileMutationResponse>()
    client.createConfigFile.mockReturnValueOnce(first.promise)
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)

    const pending = store.createFile('conf.d/new.conf', 'server {}\n')
    expect(store.state.pendingAction).toEqual({ kind: 'create_file', path: 'conf.d/new.conf' })
    await expect(store.copyFile('conf.d/site.conf', 'conf.d/copy.conf')).rejects.toThrow(
      'already in progress',
    )
    expect(client.copyConfigFile).not.toHaveBeenCalled()

    first.resolve(mutationFixture('conf.d/new.conf'))
    await pending
    expect(store.state.pendingAction).toBeNull()
  })

  it('includes explicit document save in the global mutation single-flight', async () => {
    const client = workspaceClientStub()
    const first = deferred<FileMutationResponse>()
    client.replaceConfigFile.mockReturnValueOnce(first.promise)
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'server { listen 8081; }\n')

    const pending = store.saveFile('conf.d/site.conf')
    expect(store.state.pendingAction).toEqual({ kind: 'save_file', path: 'conf.d/site.conf' })
    await expect(store.createFile('conf.d/new.conf', 'server {}\n')).rejects.toThrow(
      'already in progress',
    )
    expect(client.createConfigFile).not.toHaveBeenCalled()

    first.resolve(mutationFixture())
    await pending
    expect(store.state.pendingAction).toBeNull()
  })

  it('clears pending state after an error and records conflict identity without losing dirty text', async () => {
    const client = workspaceClientStub()
    client.createConfigFile.mockRejectedValueOnce(
      configFailure('CONFIG_WORKSPACE_CONFLICT', { current_etag: draftETagB }),
    )
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.openFile('conf.d/site.conf')
    store.updateDocument('conf.d/site.conf', 'local dirty text\r\n')

    await expect(store.createFile('conf.d/new.conf', 'server {}\n')).rejects.toMatchObject({
      apiError: { code: 'CONFIG_WORKSPACE_CONFLICT' },
    })

    expect(client.createConfigFile).toHaveBeenCalledTimes(1)
    expect(store.state.pendingAction).toBeNull()
    expect(store.state.active?.draft_etag).toBe(draftETagB)
    expect(store.state.banner?.kind).toBe('conflict')
    expect(store.document('conf.d/site.conf')).toMatchObject({
      content: 'local dirty text\r\n',
      dirty: true,
      requiresRefresh: true,
    })
  })

  it('loads and creates groups with only the server collection ETag', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.loadGroups()
    const input: GroupMutationRequest = { name: 'Sites', sort_order: 2, members: ['conf.d/site.conf'] }

    await store.createGroup(input)

    expect(client.createConfigGroup).toHaveBeenCalledWith(input, groupsETagA, 'csrf-1')
    expect(store.state.groups).toEqual(groupsFixture(groupsETagB))
    expect(store.state.active).toBeNull()
  })

  it('replaces a group with current group identity without changing workspace identity', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.loadGroups(workspaceID)
    const input: GroupMutationRequest = { name: 'Sites', sort_order: 3, members: [] }

    await store.replaceGroup(otherWorkspaceID, input)

    expect(client.replaceConfigGroup).toHaveBeenCalledWith(
      otherWorkspaceID,
      input,
      groupsETagA,
      'csrf-1',
    )
    expect(store.state.groups).toEqual(groupsFixture(groupsETagB))
    expect(store.state.active?.draft_etag).toBe(draftETagA)
  })

  it('deletes a named group with current group identity', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.loadGroups()

    await store.deleteGroup(otherWorkspaceID, 'Sites')

    expect(client.deleteConfigGroup).toHaveBeenCalledWith(
      otherWorkspaceID,
      'Sites',
      groupsETagA,
      'csrf-1',
    )
    expect(store.state.groups).toEqual({ groups: [], groups_etag: groupsETagB })
  })

  it('cancels an obsolete search and installs only the latest response', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    const first = deferred<SearchResponse>()
    client.searchConfigFiles.mockReturnValueOnce(first.promise).mockResolvedValueOnce(searchFixture('listen 443'))

    const obsolete = store.searchFiles('listen 80')
    const latest = store.searchFiles('listen 443')
    first.reject(new DOMException('aborted', 'AbortError'))

    await expect(obsolete).resolves.toBeUndefined()
    await latest
    expect(store.state.search).toEqual(searchFixture('listen 443'))
    expect(client.searchConfigFiles.mock.calls[0]?.[2]?.aborted).toBe(true)
    expect(client.searchConfigFiles.mock.calls[1]?.slice(0, 2)).toEqual([workspaceID, 'listen 443'])
  })

  it('cancels an obsolete current-file diff and installs only the latest all-files response', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    const first = deferred<DiffResponse>()
    const allFiles = diffFixture('conf.d/all.conf')
    client.getConfigDiff.mockReturnValueOnce(first.promise).mockResolvedValueOnce(allFiles)

    const obsolete = store.loadDiff('conf.d/site.conf')
    const latest = store.loadDiff()
    first.reject(new DOMException('aborted', 'AbortError'))

    await expect(obsolete).resolves.toBeUndefined()
    await latest
    expect(store.state.diff).toEqual(allFiles)
    expect(client.getConfigDiff.mock.calls[0]?.[2]?.aborted).toBe(true)
    expect(client.getConfigDiff.mock.calls[0]?.slice(0, 2)).toEqual([workspaceID, 'conf.d/site.conf'])
    expect(client.getConfigDiff.mock.calls[1]?.slice(0, 2)).toEqual([workspaceID, undefined])
  })

  it('cancels an obsolete group read and installs only the latest collection', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    const first = deferred<GroupCollection>()
    client.listConfigGroups.mockReturnValueOnce(first.promise).mockResolvedValueOnce(groupsFixture(groupsETagB))

    const obsolete = store.loadGroups(workspaceID)
    const latest = store.loadGroups(workspaceID)
    first.reject(new DOMException('aborted', 'AbortError'))

    await expect(obsolete).resolves.toBeUndefined()
    await latest
    expect(store.state.groups).toEqual(groupsFixture(groupsETagB))
    expect(client.listConfigGroups.mock.calls[0]?.[1]?.aborted).toBe(true)
  })

  it('aborts pending review reads and clears their results when workspace identity changes', async () => {
    const client = workspaceClientStub()
    const store = createWorkspaceStore(client, sessionStoreStub())
    await store.openWorkspace(workspaceID)
    await store.searchFiles('server')
    await store.loadDiff()
    expect(store.state.search).toEqual(searchFixture())
    expect(store.state.diff).toEqual(diffFixture())

    const search = deferred<SearchResponse>()
    const diff = deferred<DiffResponse>()
    client.searchConfigFiles.mockReturnValueOnce(search.promise)
    client.getConfigDiff.mockReturnValueOnce(diff.promise)
    const obsoleteSearch = store.searchFiles('pending')
    const obsoleteDiff = store.loadDiff('conf.d/site.conf')
    const searchSignal = client.searchConfigFiles.mock.calls.at(-1)?.[2]
    const diffSignal = client.getConfigDiff.mock.calls.at(-1)?.[2]
    client.getWorkspace.mockResolvedValueOnce(
      workspaceFixture({ id: otherWorkspaceID, name: 'Other workspace' }),
    )

    await store.openWorkspace(otherWorkspaceID)
    search.reject(new DOMException('aborted', 'AbortError'))
    diff.reject(new DOMException('aborted', 'AbortError'))
    await expect(obsoleteSearch).resolves.toBeUndefined()
    await expect(obsoleteDiff).resolves.toBeUndefined()

    expect(searchSignal?.aborted).toBe(true)
    expect(diffSignal?.aborted).toBe(true)
    expect(store.state.search).toBeNull()
    expect(store.state.diff).toBeNull()
    expect(store.state.active?.id).toBe(otherWorkspaceID)
  })
})

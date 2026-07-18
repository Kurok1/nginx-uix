/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { reactive } from 'vue'

import { apiClient, APIRequestError } from './api/client'
import type {
  ConfigDependency,
  ConfigFile,
  ConfigTree,
  ConfigTreeNode,
  DiffResponse,
  FileMutationResponse,
  GroupCollection,
  GroupMutationRequest,
  SearchResponse,
  WorkspaceDetail,
  WorkspaceSummary,
} from './api/types'
import { sessionStore, type SessionStore } from './session'

export interface WorkspaceClient {
  listWorkspaces: (signal?: AbortSignal) => Promise<WorkspaceSummary[]>
  createWorkspace: (
    name: string,
    csrfToken: string,
    signal?: AbortSignal,
  ) => Promise<WorkspaceDetail>
  getWorkspace: (id: string, signal?: AbortSignal) => Promise<WorkspaceDetail>
  deleteWorkspace: (
    id: string,
    confirmName: string,
    etag: string,
    csrfToken: string,
  ) => Promise<void>
  getConfigTree: (id: string, signal?: AbortSignal) => Promise<ConfigTree>
  getConfigFile: (id: string, path: string, signal?: AbortSignal) => Promise<ConfigFile>
  createConfigFile: (
    id: string,
    path: string,
    content: string,
    etag: string,
    csrfToken: string,
  ) => Promise<FileMutationResponse>
  replaceConfigFile: (
    id: string,
    path: string,
    content: string,
    etag: string,
    csrfToken: string,
  ) => Promise<FileMutationResponse>
  copyConfigFile: (
    id: string,
    sourcePath: string,
    destinationPath: string,
    etag: string,
    csrfToken: string,
  ) => Promise<FileMutationResponse>
  renameConfigFile: (
    id: string,
    sourcePath: string,
    destinationPath: string,
    etag: string,
    csrfToken: string,
  ) => Promise<FileMutationResponse>
  deleteConfigFile: (
    id: string,
    path: string,
    confirmPath: string,
    etag: string,
    csrfToken: string,
  ) => Promise<FileMutationResponse>
  searchConfigFiles: (
    id: string,
    query: string,
    signal?: AbortSignal,
  ) => Promise<SearchResponse>
  getConfigDiff: (id: string, path?: string, signal?: AbortSignal) => Promise<DiffResponse>
  listConfigGroups: (
    workspaceId?: string,
    signal?: AbortSignal,
  ) => Promise<GroupCollection>
  createConfigGroup: (
    input: GroupMutationRequest,
    etag: string,
    csrfToken: string,
  ) => Promise<GroupCollection>
  replaceConfigGroup: (
    id: string,
    input: GroupMutationRequest,
    etag: string,
    csrfToken: string,
  ) => Promise<GroupCollection>
  deleteConfigGroup: (
    id: string,
    confirmName: string,
    etag: string,
    csrfToken: string,
  ) => Promise<GroupCollection>
}

export type WorkspacePendingAction =
  | { kind: 'create_workspace' }
  | { kind: 'delete_workspace'; id: string }
  | { kind: 'create_file'; path: string }
  | { kind: 'save_file'; path: string }
  | { kind: 'copy_file'; path: string }
  | { kind: 'rename_file'; path: string }
  | { kind: 'delete_file'; path: string }
  | { kind: 'create_group' }
  | { kind: 'replace_group'; id: string }
  | { kind: 'delete_group'; id: string }

export interface OpenDocument {
  path: string
  serverContent: string
  content: string
  lineEnding: ConfigFile['line_ending']
  contentDigest: string
  dirty: boolean
  requiresRefresh: boolean
}

export interface WorkspaceStateModel {
  phase: 'idle' | 'loading' | 'ready' | 'error'
  workspaces: WorkspaceSummary[]
  active: WorkspaceDetail | null
  tree: ConfigTreeNode[]
  dependencies: ConfigDependency[]
  documents: OpenDocument[]
  selectedPath: string | null
  activeTask: 'files' | 'editor' | 'review'
  search: SearchResponse | null
  diff: DiffResponse | null
  groups: GroupCollection | null
  pendingAction: WorkspacePendingAction | null
  banner: {
    kind:
      | 'conflict'
      | 'stale'
      | 'needs_attention'
      | 'session_expired'
      | 'agent_unavailable'
    message: string
  } | null
}

export interface WorkspaceStore {
  readonly state: WorkspaceStateModel
  loadWorkspaces: () => Promise<void>
  createWorkspace: (name: string) => Promise<WorkspaceDetail>
  deleteWorkspace: (id: string, confirmName: string) => Promise<void>
  openWorkspace: (id: string) => Promise<void>
  openFile: (path: string) => Promise<void>
  closeFile: (path: string, discard?: boolean) => boolean
  createFile: (path: string, content: string) => Promise<void>
  copyFile: (sourcePath: string, destinationPath: string) => Promise<void>
  renameFile: (sourcePath: string, destinationPath: string) => Promise<void>
  deleteFile: (path: string, confirmPath: string) => Promise<void>
  updateDocument: (path: string, content: string) => void
  saveFile: (path: string) => Promise<void>
  reloadFile: (path: string) => Promise<void>
  copyLocalContent: (path: string) => Promise<boolean>
  canSave: (path: string) => boolean
  hasUnsavedChanges: () => boolean
  markSessionExpired: () => void
  markSessionRestored: () => Promise<void>
  searchFiles: (query: string) => Promise<void>
  loadDiff: (path?: string) => Promise<void>
  loadGroups: (workspaceId?: string) => Promise<void>
  createGroup: (input: GroupMutationRequest) => Promise<void>
  replaceGroup: (id: string, input: GroupMutationRequest) => Promise<void>
  deleteGroup: (id: string, confirmName: string) => Promise<void>
  document: (path: string) => OpenDocument | undefined
}

export function createWorkspaceStore(
  client: WorkspaceClient,
  sessions: SessionStore,
): WorkspaceStore {
  const state = reactive<WorkspaceStateModel>({
    phase: 'idle',
    workspaces: [],
    active: null,
    tree: [],
    dependencies: [],
    documents: [],
    selectedPath: null,
    activeTask: 'files',
    search: null,
    diff: null,
    groups: null,
    pendingAction: null,
    banner: null,
  })
  const savingPaths = new Set<string>()
  const fileControllers = new Map<string, AbortController>()
  let listController: AbortController | null = null
  let workspaceController: AbortController | null = null
  let searchController: AbortController | null = null
  let diffController: AbortController | null = null
  let groupsController: AbortController | null = null
  let workspaceGeneration = 0

  sessions.onExpired(markSessionExpired)

  function document(path: string): OpenDocument | undefined {
    return state.documents.find((candidate) => candidate.path === path)
  }

  function hasUnsavedChanges(): boolean {
    return state.documents.some((candidate) => candidate.dirty)
  }

  function canSave(path: string): boolean {
    const candidate = document(path)
    return Boolean(
      candidate?.dirty &&
        !candidate.requiresRefresh &&
        state.active?.state === 'ready' &&
        sessions.state.phase === 'authenticated' &&
        sessions.state.session !== null &&
        state.pendingAction === null &&
        !savingPaths.has(path),
    )
  }

  function requireCSRFToken(): string {
    const token = sessions.state.session?.csrf_token
    if (sessions.state.phase !== 'authenticated' || token === undefined) {
      throw new Error('an authenticated session is required')
    }
    return token
  }

  function requireReadyWorkspace(): WorkspaceDetail {
    if (state.active === null || state.active.state !== 'ready') {
      throw new Error('a ready workspace is required')
    }
    return state.active
  }

  async function runMutation<T>(
    pending: WorkspacePendingAction,
    operation: () => Promise<T>,
    errorDocument?: OpenDocument,
  ): Promise<T> {
    if (state.pendingAction !== null) {
      throw new Error('another workspace mutation is already in progress')
    }
    state.pendingAction = pending
    try {
      return await operation()
    } catch (error) {
      handleError(error, errorDocument)
      throw error
    } finally {
      state.pendingAction = null
    }
  }

  async function loadWorkspaces(): Promise<void> {
    listController?.abort()
    const controller = new AbortController()
    listController = controller
    state.phase = 'loading'
    try {
      const workspaces = await client.listWorkspaces(controller.signal)
      if (controller.signal.aborted || listController !== controller) {
        return
      }
      state.workspaces = workspaces
      state.phase = state.active === null ? 'idle' : 'ready'
    } catch (error) {
      if (controller.signal.aborted || listController !== controller || isAbortError(error)) {
        return
      }
      state.phase = 'error'
      handleError(error)
      throw error
    } finally {
      if (listController === controller) {
        listController = null
      }
    }
  }

  async function createWorkspace(name: string): Promise<WorkspaceDetail> {
    const csrfToken = requireCSRFToken()
    return runMutation({ kind: 'create_workspace' }, async () => {
      const controller = new AbortController()
      const workspace = await client.createWorkspace(name, csrfToken, controller.signal)
      await loadWorkspaces()
      return workspace
    })
  }

  async function deleteWorkspace(id: string, confirmName: string): Promise<void> {
    const csrfToken = requireCSRFToken()
    if (state.active?.id === id && hasUnsavedChanges()) {
      throw new Error('dirty documents must be resolved before deleting the workspace')
    }
    const workspace =
      state.active?.id === id
        ? state.active
        : state.workspaces.find((candidate) => candidate.id === id)
    if (workspace === undefined || workspace === null) {
      throw new Error('workspace is not loaded')
    }

    await runMutation({ kind: 'delete_workspace', id }, async () => {
      await client.deleteWorkspace(id, confirmName, workspace.draft_etag, csrfToken)
      state.workspaces = state.workspaces.filter((candidate) => candidate.id !== id)
      if (state.active?.id === id) {
        clearActiveWorkspace()
      }
    })
  }

  async function openWorkspace(id: string): Promise<void> {
    workspaceController?.abort()
    abortFileReads()
    abortReviewReads()
    const controller = new AbortController()
    workspaceController = controller
    workspaceGeneration += 1
    const generation = workspaceGeneration
    const previousID = state.active?.id
    state.phase = 'loading'
    state.banner = null
    state.search = null
    state.diff = null

    try {
      const workspace = await client.getWorkspace(id, controller.signal)
      if (!isCurrentWorkspaceRequest(controller, generation)) {
        return
      }
      const tree = await client.getConfigTree(id, controller.signal)
      if (!isCurrentWorkspaceRequest(controller, generation)) {
        return
      }

      state.active = { ...workspace, draft_etag: tree.draft_etag }
      state.tree = tree.entries
      state.dependencies = tree.dependencies
      state.phase = 'ready'
      state.banner =
        bannerForWorkspace(state.active) ??
        (previousID === id &&
        state.documents.some(({ dirty, requiresRefresh }) => dirty && requiresRefresh)
          ? { kind: 'conflict', message: 'Reload the server file before saving local text.' }
          : null)
      replaceWorkspaceSummary(state.active)
      if (previousID !== id) {
        state.documents = []
        state.selectedPath = null
        state.activeTask = 'files'
      }
    } catch (error) {
      if (!isCurrentWorkspaceRequest(controller, generation) || isAbortError(error)) {
        return
      }
      state.phase = 'error'
      handleError(error)
      throw error
    } finally {
      if (workspaceController === controller) {
        workspaceController = null
      }
    }
  }

  async function openFile(path: string): Promise<void> {
    const existing = document(path)
    if (existing !== undefined) {
      state.selectedPath = path
      state.activeTask = 'editor'
      return
    }
    const workspace = state.active
    if (workspace === null) {
      throw new Error('open a workspace before opening a file')
    }

    fileControllers.get(path)?.abort()
    const controller = new AbortController()
    const generation = workspaceGeneration
    fileControllers.set(path, controller)
    try {
      const file = await client.getConfigFile(workspace.id, path, controller.signal)
      if (
        controller.signal.aborted ||
        fileControllers.get(path) !== controller ||
        generation !== workspaceGeneration ||
        state.active?.id !== workspace.id
      ) {
        return
      }
      state.documents.push({
        path: file.path,
        serverContent: file.content,
        content: file.content,
        lineEnding: file.line_ending,
        contentDigest: file.content_digest,
        dirty: false,
        requiresRefresh: false,
      })
      state.active.draft_etag = file.draft_etag
      state.selectedPath = file.path
      state.activeTask = 'editor'
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) {
        return
      }
      handleError(error)
      throw error
    } finally {
      if (fileControllers.get(path) === controller) {
        fileControllers.delete(path)
      }
    }
  }

  function closeFile(path: string, discard = false): boolean {
    const candidate = document(path)
    if (candidate === undefined) {
      return true
    }
    if (candidate.dirty && !discard) {
      return false
    }
    const index = state.documents.indexOf(candidate)
    state.documents.splice(index, 1)
    if (state.selectedPath === path) {
      state.selectedPath = state.documents.at(-1)?.path ?? null
      if (state.selectedPath === null) {
        state.activeTask = 'files'
      }
    }
    return true
  }

  async function createFile(path: string, content: string): Promise<void> {
    const csrfToken = requireCSRFToken()
    const workspace = requireReadyWorkspace()
    await runMutation({ kind: 'create_file', path }, async () => {
      const result = await client.createConfigFile(
        workspace.id,
        path,
        content,
        workspace.draft_etag,
        csrfToken,
      )
      if (state.active?.id === workspace.id) {
        applyFileMutation(result)
      }
    })
  }

  async function copyFile(sourcePath: string, destinationPath: string): Promise<void> {
    const csrfToken = requireCSRFToken()
    const workspace = requireReadyWorkspace()
    await runMutation({ kind: 'copy_file', path: destinationPath }, async () => {
      const result = await client.copyConfigFile(
        workspace.id,
        sourcePath,
        destinationPath,
        workspace.draft_etag,
        csrfToken,
      )
      if (state.active?.id === workspace.id) {
        applyFileMutation(result)
      }
    })
  }

  async function renameFile(sourcePath: string, destinationPath: string): Promise<void> {
    const csrfToken = requireCSRFToken()
    const workspace = requireReadyWorkspace()
    const openDocument = document(sourcePath)
    if (openDocument?.dirty) {
      throw new Error('dirty document must be resolved before renaming the file')
    }

    await runMutation({ kind: 'rename_file', path: sourcePath }, async () => {
      const result = await client.renameConfigFile(
        workspace.id,
        sourcePath,
        destinationPath,
        workspace.draft_etag,
        csrfToken,
      )
      if (state.active?.id !== workspace.id) {
        return
      }
      removeTreeEntry(sourcePath)
      applyFileMutation(result)
      if (openDocument !== undefined) {
        openDocument.path = destinationPath
        openDocument.contentDigest = result.entry?.content_digest ?? openDocument.contentDigest
      }
      if (state.selectedPath === sourcePath) {
        state.selectedPath = destinationPath
      }
    })
  }

  async function deleteFile(path: string, confirmPath: string): Promise<void> {
    const csrfToken = requireCSRFToken()
    const workspace = requireReadyWorkspace()
    const openDocument = document(path)
    if (openDocument?.dirty) {
      throw new Error('dirty document must be resolved before deleting the file')
    }

    await runMutation({ kind: 'delete_file', path }, async () => {
      const result = await client.deleteConfigFile(
        workspace.id,
        path,
        confirmPath,
        workspace.draft_etag,
        csrfToken,
      )
      if (state.active?.id !== workspace.id) {
        return
      }
      removeTreeEntry(path)
      applyFileMutation(result)
      closeFile(path)
    })
  }

  function updateDocument(path: string, content: string): void {
    const candidate = requireDocument(path)
    candidate.content = content
    candidate.dirty = content !== candidate.serverContent
    if (candidate.dirty && sessions.state.phase !== 'authenticated') {
      candidate.requiresRefresh = true
    }
  }

  async function saveFile(path: string): Promise<void> {
    const candidate = requireDocument(path)
    const workspace = state.active
    const csrfToken = sessions.state.session?.csrf_token
    if (!canSave(path) || workspace === null || csrfToken === undefined) {
      throw new Error('file cannot be saved in its current state')
    }

    const content = candidate.content
    const workspaceID = workspace.id
    await runMutation(
      { kind: 'save_file', path },
      async () => {
        savingPaths.add(path)
        try {
          const result = await client.replaceConfigFile(
            workspaceID,
            path,
            content,
            workspace.draft_etag,
            csrfToken,
          )
          if (state.active?.id !== workspaceID) {
            return
          }

          applyFileMutation(result)
          candidate.serverContent = content
          candidate.contentDigest = result.entry?.content_digest ?? candidate.contentDigest
          candidate.lineEnding = detectLineEnding(content)
          candidate.dirty = candidate.content !== content
          candidate.requiresRefresh = false
        } finally {
          savingPaths.delete(path)
        }
      },
      candidate,
    )
  }

  async function reloadFile(path: string): Promise<void> {
    const candidate = requireDocument(path)
    const workspace = state.active
    if (workspace === null) {
      throw new Error('open a workspace before reloading a file')
    }

    const file = await client.getConfigFile(workspace.id, path)
    if (state.active?.id !== workspace.id) {
      return
    }
    candidate.serverContent = file.content
    candidate.content = file.content
    candidate.lineEnding = file.line_ending
    candidate.contentDigest = file.content_digest
    candidate.dirty = false
    candidate.requiresRefresh = false
    state.active.draft_etag = file.draft_etag
    if (state.documents.every((item) => !item.requiresRefresh)) {
      state.banner = bannerForWorkspace(state.active)
    }
  }

  async function copyLocalContent(path: string): Promise<boolean> {
    const candidate = document(path)
    const clipboard = globalThis.navigator?.clipboard
    if (candidate === undefined || clipboard === undefined) {
      return false
    }
    try {
      await clipboard.writeText(candidate.content)
      return true
    } catch {
      return false
    }
  }

  function markSessionExpired(): void {
    for (const candidate of state.documents) {
      if (candidate.dirty) {
        candidate.requiresRefresh = true
      }
    }
    state.banner = { kind: 'session_expired', message: 'Session expired; local text remains in memory.' }
  }

  async function markSessionRestored(): Promise<void> {
    const workspace = state.active
    if (workspace === null) {
      return
    }
    if (sessions.state.phase !== 'authenticated' || sessions.state.session === null) {
      throw new Error('restore the session before refreshing workspace state')
    }

    const controller = new AbortController()
    const [freshWorkspace, tree] = await Promise.all([
      client.getWorkspace(workspace.id, controller.signal),
      client.getConfigTree(workspace.id, controller.signal),
    ])
    if (state.active?.id !== workspace.id) {
      return
    }
    state.active = { ...freshWorkspace, draft_etag: tree.draft_etag }
    state.tree = tree.entries
    state.dependencies = tree.dependencies
    replaceWorkspaceSummary(state.active)
    for (const candidate of state.documents) {
      candidate.requiresRefresh = true
    }
    state.phase = 'ready'
    state.banner = state.documents.some((candidate) => candidate.dirty)
      ? { kind: 'conflict', message: 'Reload the server file before saving local text.' }
      : bannerForWorkspace(state.active)
  }

  async function searchFiles(query: string): Promise<void> {
    const workspace = state.active
    if (workspace === null) {
      throw new Error('open a workspace before searching files')
    }
    searchController?.abort()
    const controller = new AbortController()
    searchController = controller
    state.search = null
    try {
      const result = await client.searchConfigFiles(workspace.id, query, controller.signal)
      if (
        !controller.signal.aborted &&
        searchController === controller &&
        state.active?.id === workspace.id
      ) {
        state.search = result
      }
    } catch (error) {
      if (controller.signal.aborted || searchController !== controller || isAbortError(error)) {
        return
      }
      handleError(error)
      throw error
    } finally {
      if (searchController === controller) {
        searchController = null
      }
    }
  }

  async function loadDiff(path?: string): Promise<void> {
    const workspace = state.active
    if (workspace === null) {
      throw new Error('open a workspace before loading a diff')
    }
    diffController?.abort()
    const controller = new AbortController()
    diffController = controller
    state.diff = null
    try {
      const result = await client.getConfigDiff(workspace.id, path, controller.signal)
      if (
        !controller.signal.aborted &&
        diffController === controller &&
        state.active?.id === workspace.id
      ) {
        state.diff = result
      }
    } catch (error) {
      if (controller.signal.aborted || diffController !== controller || isAbortError(error)) {
        return
      }
      handleError(error)
      throw error
    } finally {
      if (diffController === controller) {
        diffController = null
      }
    }
  }

  async function loadGroups(workspaceId?: string): Promise<void> {
    groupsController?.abort()
    const controller = new AbortController()
    groupsController = controller
    state.groups = null
    try {
      const result = await client.listConfigGroups(workspaceId, controller.signal)
      if (
        !controller.signal.aborted &&
        groupsController === controller &&
        (workspaceId === undefined || state.active?.id === workspaceId)
      ) {
        state.groups = result
      }
    } catch (error) {
      if (controller.signal.aborted || groupsController !== controller || isAbortError(error)) {
        return
      }
      handleError(error)
      throw error
    } finally {
      if (groupsController === controller) {
        groupsController = null
      }
    }
  }

  async function createGroup(input: GroupMutationRequest): Promise<void> {
    const csrfToken = requireCSRFToken()
    const groups = requireGroups()
    await runMutation({ kind: 'create_group' }, async () => {
      state.groups = await client.createConfigGroup(input, groups.groups_etag, csrfToken)
    })
  }

  async function replaceGroup(id: string, input: GroupMutationRequest): Promise<void> {
    const csrfToken = requireCSRFToken()
    const groups = requireGroups()
    await runMutation({ kind: 'replace_group', id }, async () => {
      state.groups = await client.replaceConfigGroup(id, input, groups.groups_etag, csrfToken)
    })
  }

  async function deleteGroup(id: string, confirmName: string): Promise<void> {
    const csrfToken = requireCSRFToken()
    const groups = requireGroups()
    await runMutation({ kind: 'delete_group', id }, async () => {
      state.groups = await client.deleteConfigGroup(id, confirmName, groups.groups_etag, csrfToken)
    })
  }

  function requireGroups(): GroupCollection {
    if (state.groups === null) {
      throw new Error('load groups before mutating the collection')
    }
    return state.groups
  }

  function requireDocument(path: string): OpenDocument {
    const candidate = document(path)
    if (candidate === undefined) {
      throw new Error('file is not open')
    }
    return candidate
  }

  function replaceWorkspaceSummary(workspace: WorkspaceDetail): void {
    const index = state.workspaces.findIndex((candidate) => candidate.id === workspace.id)
    if (index >= 0) {
      state.workspaces[index] = workspace
    }
  }

  function applyFileMutation(result: FileMutationResponse): void {
    state.active = result.workspace
    replaceWorkspaceSummary(result.workspace)
    if (result.entry !== undefined) {
      replaceTreeEntry(result.entry)
    }
    state.banner = bannerForWorkspace(result.workspace)
    state.search = null
    state.diff = null
  }

  function replaceTreeEntry(entry: ConfigTreeNode): void {
    const index = state.tree.findIndex((candidate) => candidate.path === entry.path)
    if (index >= 0) {
      state.tree[index] = entry
    } else {
      state.tree.push(entry)
    }
  }

  function removeTreeEntry(path: string): void {
    state.tree = state.tree.filter((candidate) => candidate.path !== path)
  }

  function abortFileReads(): void {
    for (const controller of fileControllers.values()) {
      controller.abort()
    }
    fileControllers.clear()
  }

  function abortReviewReads(): void {
    searchController?.abort()
    diffController?.abort()
    groupsController?.abort()
    searchController = null
    diffController = null
    groupsController = null
  }

  function clearActiveWorkspace(): void {
    abortFileReads()
    abortReviewReads()
    workspaceGeneration += 1
    state.phase = 'idle'
    state.active = null
    state.tree = []
    state.dependencies = []
    state.documents = []
    state.selectedPath = null
    state.activeTask = 'files'
    state.search = null
    state.diff = null
    state.groups = null
    state.banner = null
  }

  function isCurrentWorkspaceRequest(controller: AbortController, generation: number): boolean {
    return (
      !controller.signal.aborted &&
      workspaceController === controller &&
      workspaceGeneration === generation
    )
  }

  function handleError(error: unknown, candidate?: OpenDocument): void {
    if (sessions.handleAPIError(error)) {
      return
    }
    if (!(error instanceof APIRequestError) || error.kind !== 'api') {
      return
    }
    switch (error.apiError?.code) {
      case 'CONFIG_WORKSPACE_CONFLICT': {
        const currentETag = error.apiError?.details?.current_etag
        if (typeof currentETag === 'string' && currentETag.startsWith('"draft-v1:')) {
          if (state.active !== null) {
            state.active.draft_etag = currentETag
            replaceWorkspaceSummary(state.active)
          }
          for (const openDocument of state.documents) {
            if (openDocument.dirty) {
              openDocument.requiresRefresh = true
            }
          }
        } else if (
          typeof currentETag === 'string' &&
          currentETag.startsWith('"groups-v1:') &&
          state.groups !== null
        ) {
          state.groups.groups_etag = currentETag
        } else if (candidate !== undefined) {
          candidate.requiresRefresh = true
        }
        state.banner = { kind: 'conflict', message: 'The workspace changed on the server.' }
        break
      }
      case 'CONFIG_WORKSPACE_STALE':
        if (state.active !== null) {
          state.active.state = 'stale'
        }
        state.banner = { kind: 'stale', message: 'The production configuration has changed.' }
        break
      case 'CONFIG_WORKSPACE_NEEDS_ATTENTION':
        if (state.active !== null) {
          state.active.state = 'needs_attention'
        }
        state.banner = {
          kind: 'needs_attention',
          message: 'Workspace consistency needs administrator attention.',
        }
        break
      case 'AGENT_UNAVAILABLE':
        state.banner = { kind: 'agent_unavailable', message: 'The local agent is unavailable.' }
        break
    }
  }

  return {
    state,
    loadWorkspaces,
    createWorkspace,
    deleteWorkspace,
    openWorkspace,
    openFile,
    closeFile,
    createFile,
    copyFile,
    renameFile,
    deleteFile,
    updateDocument,
    saveFile,
    reloadFile,
    copyLocalContent,
    canSave,
    hasUnsavedChanges,
    markSessionExpired,
    markSessionRestored,
    searchFiles,
    loadDiff,
    loadGroups,
    createGroup,
    replaceGroup,
    deleteGroup,
    document,
  }
}

function bannerForWorkspace(workspace: WorkspaceDetail): WorkspaceStateModel['banner'] {
  switch (workspace.state) {
    case 'stale':
      return { kind: 'stale', message: 'The production configuration has changed.' }
    case 'needs_attention':
      return {
        kind: 'needs_attention',
        message: 'Workspace consistency needs administrator attention.',
      }
    default:
      return null
  }
}

function detectLineEnding(content: string): ConfigFile['line_ending'] {
  const hasCRLF = content.includes('\r\n')
  const remaining = content.replaceAll('\r\n', '')
  const hasLF = remaining.includes('\n')
  const hasCR = remaining.includes('\r')
  if (!hasCRLF && !hasLF && !hasCR) {
    return 'none'
  }
  if (hasCRLF && !hasLF && !hasCR) {
    return 'crlf'
  }
  if (!hasCRLF && hasLF && !hasCR) {
    return 'lf'
  }
  return 'mixed'
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

export const workspaceStore = createWorkspaceStore(apiClient, sessionStore)

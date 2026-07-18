/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import type {
  APIError,
  APIErrorCode,
  APIErrorEnvelope,
  ConfigDependency,
  ConfigFile,
  ConfigGroup,
  ConfigTree,
  ConfigTreeNode,
  DiffResponse,
  EffectiveConfigOccurrence,
  EffectiveConfigResponse,
  FileDiffSummary,
  FileMutationResponse,
  GroupCollection,
  GroupMutationRequest,
  LoginRequest,
  NginxBuild,
  NginxProcess,
  RecoveryStatus,
  SearchMatch,
  SearchResponse,
  SessionResponse,
  StartupValidation,
  SystemStatusResponse,
  WorkspaceDetail,
  WorkspaceSummary,
} from './types'

const sessionPath = '/api/v1/auth/session'
const systemStatusPath = '/api/v1/system/status'
const effectiveConfigPath = '/api/v1/nginx/effective-config'
const workspacesPath = '/api/v1/config/workspaces'
const groupsPath = '/api/v1/config/groups'

export type APIRequestErrorKind = 'api' | 'malformed_response' | 'network'
export type APIErrorListener = (error: APIRequestError) => void
type APIErrorInput = Omit<APIError, 'code'> & { code: string }

export class APIRequestError extends Error {
  readonly apiError?: APIError
  readonly kind: APIRequestErrorKind
  readonly retryAfterSeconds?: number
  readonly status?: number

  constructor(options: {
    kind: APIRequestErrorKind
    message: string
    status?: number
    apiError?: APIErrorInput
    retryAfterSeconds?: number
  }) {
    super(options.message)
    this.name = 'APIRequestError'
    this.kind = options.kind
    this.status = options.status
    this.apiError = normalizeAPIError(options.apiError)
    this.retryAfterSeconds = options.retryAfterSeconds
  }
}

function normalizeAPIError(value: APIErrorInput | undefined): APIError | undefined {
  if (value === undefined) {
    return undefined
  }
  if (!isAPIErrorCode(value.code)) {
    throw new TypeError('unknown API error code')
  }
  return { ...value, code: value.code }
}

type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>

export class APIClient {
  private readonly errorListeners = new Set<APIErrorListener>()
  private readonly fetcher: Fetcher

  constructor(fetcher: Fetcher = (input, init) => fetch(input, init)) {
    this.fetcher = fetcher
  }

  async login(input: LoginRequest): Promise<SessionResponse> {
    const response = await this.send(sessionPath, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    return parseSessionResponse(await readJSON(response), response.status)
  }

  async getSession(): Promise<SessionResponse> {
    const response = await this.send(sessionPath, { method: 'GET' })
    return parseSessionResponse(await readJSON(response), response.status)
  }

  async getSystemStatus(signal?: AbortSignal): Promise<SystemStatusResponse> {
    const response = await this.send(systemStatusPath, { method: 'GET', signal })
    return parseSystemStatusResponse(await readJSON(response), response.status)
  }

  async getEffectiveConfig(signal?: AbortSignal): Promise<EffectiveConfigResponse> {
    const response = await this.send(effectiveConfigPath, { method: 'GET', signal })
    return parseEffectiveConfigResponse(await readJSON(response), response.status)
  }

  async listWorkspaces(signal?: AbortSignal): Promise<WorkspaceSummary[]> {
    const response = await this.send(workspacesPath, { method: 'GET', signal })
    return parseWorkspaceList(await readJSON(response), response.status)
  }

  async createWorkspace(
    name: string,
    csrfToken: string,
    signal?: AbortSignal,
  ): Promise<WorkspaceDetail> {
    const response = await this.send(workspacesPath, {
      method: 'POST',
      headers: jsonMutationHeaders(csrfToken),
      body: JSON.stringify({ name }),
      signal,
    })
    const result = parseWorkspace(await readJSON(response), response.status)
    requireMatchingETag(response, result.draft_etag)
    return result
  }

  async getWorkspace(id: string, signal?: AbortSignal): Promise<WorkspaceDetail> {
    const response = await this.send(workspacePath(id), { method: 'GET', signal })
    const result = parseWorkspace(await readJSON(response), response.status)
    requireMatchingETag(response, result.draft_etag)
    return result
  }

  async deleteWorkspace(
    id: string,
    confirmName: string,
    etag: string,
    csrfToken: string,
  ): Promise<void> {
    const response = await this.send(workspacePath(id), {
      method: 'DELETE',
      headers: jsonMutationHeaders(csrfToken, etag),
      body: JSON.stringify({ confirm_name: confirmName }),
    })
    requireNoContent(response)
  }

  async getConfigTree(id: string, signal?: AbortSignal): Promise<ConfigTree> {
    const response = await this.send(`${workspacePath(id)}/files`, { method: 'GET', signal })
    const result = parseConfigTree(await readJSON(response), response.status)
    requireMatchingETag(response, result.draft_etag)
    return result
  }

  async getConfigFile(id: string, path: string, signal?: AbortSignal): Promise<ConfigFile> {
    const response = await this.send(withQuery(`${workspacePath(id)}/files`, { path }), {
      method: 'GET',
      signal,
    })
    const result = parseConfigFile(await readJSON(response), response.status)
    requireMatchingETag(response, result.draft_etag)
    return result
  }

  async createConfigFile(
    id: string,
    path: string,
    content: string,
    etag: string,
    csrfToken: string,
  ): Promise<FileMutationResponse> {
    return this.fileMutation(
      `${workspacePath(id)}/files`,
      'POST',
      { path, content },
      etag,
      csrfToken,
    )
  }

  async replaceConfigFile(
    id: string,
    path: string,
    content: string,
    etag: string,
    csrfToken: string,
  ): Promise<FileMutationResponse> {
    return this.fileMutation(
      withQuery(`${workspacePath(id)}/files`, { path }),
      'PUT',
      { content },
      etag,
      csrfToken,
    )
  }

  async copyConfigFile(
    id: string,
    sourcePath: string,
    destinationPath: string,
    etag: string,
    csrfToken: string,
  ): Promise<FileMutationResponse> {
    return this.fileMutation(
      `${workspacePath(id)}/files/copies`,
      'POST',
      { source_path: sourcePath, destination_path: destinationPath },
      etag,
      csrfToken,
    )
  }

  async renameConfigFile(
    id: string,
    sourcePath: string,
    destinationPath: string,
    etag: string,
    csrfToken: string,
  ): Promise<FileMutationResponse> {
    return this.fileMutation(
      withQuery(`${workspacePath(id)}/files`, { path: sourcePath }),
      'PATCH',
      { destination_path: destinationPath },
      etag,
      csrfToken,
    )
  }

  async deleteConfigFile(
    id: string,
    path: string,
    confirmPath: string,
    etag: string,
    csrfToken: string,
  ): Promise<FileMutationResponse> {
    return this.fileMutation(
      withQuery(`${workspacePath(id)}/files`, { path }),
      'DELETE',
      { confirm_path: confirmPath },
      etag,
      csrfToken,
    )
  }

  async searchConfigFiles(
    id: string,
    query: string,
    signal?: AbortSignal,
  ): Promise<SearchResponse> {
    const response = await this.send(
      withQuery(`${workspacePath(id)}/files/search`, { query }),
      { method: 'GET', signal },
    )
    return parseSearchResponse(await readJSON(response), response.status)
  }

  async getConfigDiff(
    id: string,
    path?: string,
    signal?: AbortSignal,
  ): Promise<DiffResponse> {
    const endpoint = `${workspacePath(id)}/diff`
    const response = await this.send(path === undefined ? endpoint : withQuery(endpoint, { path }), {
      method: 'GET',
      signal,
    })
    return parseDiffResponse(await readJSON(response), response.status)
  }

  async listConfigGroups(
    workspaceId?: string,
    signal?: AbortSignal,
  ): Promise<GroupCollection> {
    const endpoint =
      workspaceId === undefined ? groupsPath : withQuery(groupsPath, { workspace_id: workspaceId })
    const response = await this.send(endpoint, { method: 'GET', signal })
    return parseGroupCollectionResponse(response)
  }

  async createConfigGroup(
    input: GroupMutationRequest,
    etag: string,
    csrfToken: string,
  ): Promise<GroupCollection> {
    return this.groupMutation(groupsPath, 'POST', input, etag, csrfToken)
  }

  async replaceConfigGroup(
    id: string,
    input: GroupMutationRequest,
    etag: string,
    csrfToken: string,
  ): Promise<GroupCollection> {
    return this.groupMutation(`${groupsPath}/${id}`, 'PUT', input, etag, csrfToken)
  }

  async deleteConfigGroup(
    id: string,
    confirmName: string,
    etag: string,
    csrfToken: string,
  ): Promise<GroupCollection> {
    return this.groupMutation(
      `${groupsPath}/${id}`,
      'DELETE',
      { confirm_name: confirmName },
      etag,
      csrfToken,
    )
  }

  async logout(csrfToken: string): Promise<void> {
    const response = await this.send(sessionPath, {
      method: 'DELETE',
      headers: { 'X-CSRF-Token': csrfToken },
    })
    if (response.status !== 204) {
      throw malformedResponse(response.status)
    }
  }

  onError(listener: APIErrorListener): () => void {
    this.errorListeners.add(listener)
    return () => this.errorListeners.delete(listener)
  }

  private async fileMutation(
    path: string,
    method: 'POST' | 'PUT' | 'PATCH' | 'DELETE',
    body: Readonly<Record<string, string>>,
    etag: string,
    csrfToken: string,
  ): Promise<FileMutationResponse> {
    const response = await this.send(path, {
      method,
      headers: jsonMutationHeaders(csrfToken, etag),
      body: JSON.stringify(body),
    })
    const result = parseFileMutationResponse(await readJSON(response), response.status)
    requireMatchingETag(response, result.draft_etag)
    return result
  }

  private async groupMutation(
    path: string,
    method: 'POST' | 'PUT' | 'DELETE',
    body: GroupMutationRequest | Readonly<{ confirm_name: string }>,
    etag: string,
    csrfToken: string,
  ): Promise<GroupCollection> {
    const response = await this.send(path, {
      method,
      headers: jsonMutationHeaders(csrfToken, etag),
      body: JSON.stringify(body),
    })
    return parseGroupCollectionResponse(response)
  }

  private async send(path: string, init: RequestInit): Promise<Response> {
    let response: Response
    try {
      response = await this.fetcher(path, {
        ...init,
        credentials: 'same-origin',
        cache: 'no-store',
      })
    } catch {
      throw new APIRequestError({ kind: 'network', message: 'Network request failed' })
    }

    if (response.ok) {
      return response
    }

    const payload = await readJSON(response)
    const envelope = parseAPIErrorEnvelope(payload, response.status)
    const error = new APIRequestError({
      kind: 'api',
      message: envelope.error.message,
      status: response.status,
      apiError: envelope.error,
      retryAfterSeconds: parseRetryAfter(response.headers.get('Retry-After')),
    })
    for (const listener of this.errorListeners) {
      listener(error)
    }
    throw error
  }
}

export const apiClient = new APIClient()

function workspacePath(id: string): string {
  return `${workspacesPath}/${id}`
}

function withQuery(path: string, query: Readonly<Record<string, string>>): string {
  return `${path}?${new URLSearchParams(query).toString()}`
}

function jsonMutationHeaders(csrfToken: string, etag?: string): HeadersInit {
  return {
    'Content-Type': 'application/json',
    'X-CSRF-Token': csrfToken,
    ...(etag === undefined ? {} : { 'If-Match': etag }),
  }
}

function requireNoContent(response: Response): void {
  if (response.status !== 204) {
    throw malformedResponse(response.status)
  }
}

function requireMatchingETag(response: Response, dtoETag: string): void {
  if (response.headers.get('ETag') !== dtoETag) {
    throw malformedResponse(response.status)
  }
}

async function parseGroupCollectionResponse(response: Response): Promise<GroupCollection> {
  const result = parseGroupCollection(await readJSON(response), response.status)
  requireMatchingETag(response, result.groups_etag)
  return result
}

async function readJSON(response: Response): Promise<unknown> {
  try {
    return await response.json()
  } catch {
    throw malformedResponse(response.status)
  }
}

function parseSessionResponse(value: unknown, status: number): SessionResponse {
  if (
    !hasExactKeys(value, [
      'user',
      'csrf_token',
      'created_at',
      'last_seen_at',
      'idle_expires_at',
      'absolute_expires_at',
    ]) ||
    !hasExactKeys(value.user, ['id', 'username', 'created_at'])
  ) {
    throw malformedResponse(status)
  }
  const { user } = value
  if (
    !Number.isSafeInteger(user.id) ||
    typeof user.username !== 'string' ||
    !isRFC3339(user.created_at) ||
    typeof value.csrf_token !== 'string' ||
    !isRFC3339(value.created_at) ||
    !isRFC3339(value.last_seen_at) ||
    !isRFC3339(value.idle_expires_at) ||
    !isRFC3339(value.absolute_expires_at)
  ) {
    throw malformedResponse(status)
  }
  return {
    user: { id: user.id as number, username: user.username, created_at: user.created_at },
    csrf_token: value.csrf_token,
    created_at: value.created_at,
    last_seen_at: value.last_seen_at,
    idle_expires_at: value.idle_expires_at,
    absolute_expires_at: value.absolute_expires_at,
  }
}

function parseSystemStatusResponse(value: unknown, status: number): SystemStatusResponse {
  if (
    !hasExactKeys(value, [
      'sampled_at',
      'components',
      'master',
      'workers',
      'build',
      'startup_validation',
      'recovery',
      'issues',
    ]) ||
    !isRFC3339(value.sampled_at) ||
    !hasExactKeys(value.components, ['ui', 'agent', 'nginx']) ||
    value.components.ui !== 'healthy' ||
    !isOneOf(value.components.agent, ['healthy', 'unavailable']) ||
    !isOneOf(value.components.nginx, ['running', 'degraded', 'stopped', 'unknown']) ||
    !Array.isArray(value.workers) ||
    !Array.isArray(value.issues) ||
    !value.issues.every((issue) => typeof issue === 'string')
  ) {
    throw malformedResponse(status)
  }

  const master = value.master === null ? null : parseProcess(value.master, 'master', status)
  const workers = value.workers.map((worker) => parseProcess(worker, 'worker', status))
  const build = value.build === null ? null : parseBuild(value.build, status)
  const startupValidation =
    value.startup_validation === null
      ? null
      : parseStartupValidation(value.startup_validation, status)
  const recovery = value.recovery === null ? null : parseRecovery(value.recovery, status)

  return {
    sampled_at: value.sampled_at,
    components: {
      ui: value.components.ui,
      agent: value.components.agent,
      nginx: value.components.nginx,
    },
    master,
    workers,
    build,
    startup_validation: startupValidation,
    recovery,
    issues: [...value.issues],
  }
}

function parseEffectiveConfigResponse(value: unknown, status: number): EffectiveConfigResponse {
  if (
    !hasExactKeys(value, [
      'generated_at',
      'nginx_version',
      'entry_config_path',
      'occurrence_count',
      'occurrences',
    ]) ||
    !isRFC3339(value.generated_at) ||
    typeof value.nginx_version !== 'string' ||
    typeof value.entry_config_path !== 'string' ||
    !Number.isSafeInteger(value.occurrence_count) ||
    (value.occurrence_count as number) < 0 ||
    !Array.isArray(value.occurrences) ||
    value.occurrences.length !== value.occurrence_count
  ) {
    throw malformedResponse(status)
  }

  const ids = new Set<string>()
  const occurrences = value.occurrences.map((occurrence, index) => {
    const parsed = parseEffectiveConfigOccurrence(occurrence, index + 1, status)
    if (ids.has(parsed.id)) {
      throw malformedResponse(status)
    }
    ids.add(parsed.id)
    return parsed
  })

  return {
    generated_at: value.generated_at,
    nginx_version: value.nginx_version,
    entry_config_path: value.entry_config_path,
    occurrence_count: value.occurrence_count as number,
    occurrences,
  }
}

function parseEffectiveConfigOccurrence(
  value: unknown,
  expectedLoadOrder: number,
  status: number,
): EffectiveConfigOccurrence {
  if (
    !hasExactKeys(value, ['id', 'load_order', 'path', 'content']) ||
    typeof value.id !== 'string' ||
    value.id === '' ||
    value.load_order !== expectedLoadOrder ||
    typeof value.path !== 'string' ||
    typeof value.content !== 'string'
  ) {
    throw malformedResponse(status)
  }
  return {
    id: value.id,
    load_order: expectedLoadOrder,
    path: value.path,
    content: value.content,
  }
}

function parseProcess(value: unknown, role: NginxProcess['role'], status: number): NginxProcess {
  if (
    !hasExactKeys(value, ['pid', 'role', 'started_at']) ||
    !Number.isSafeInteger(value.pid) ||
    (value.pid as number) <= 0 ||
    value.role !== role ||
    !isRFC3339(value.started_at)
  ) {
    throw malformedResponse(status)
  }
  return { pid: value.pid as number, role, started_at: value.started_at }
}

function parseBuild(value: unknown, status: number): NginxBuild {
  if (
    !hasExactKeys(value, ['version', 'configure_arguments']) ||
    typeof value.version !== 'string' ||
    !Array.isArray(value.configure_arguments) ||
    !value.configure_arguments.every((argument) => typeof argument === 'string')
  ) {
    throw malformedResponse(status)
  }
  return {
    version: value.version,
    configure_arguments: [...value.configure_arguments],
  }
}

function parseStartupValidation(value: unknown, status: number): StartupValidation {
  if (
    !hasExactKeys(value, ['valid', 'checked_at', 'exit_code', 'diagnostic']) ||
    typeof value.valid !== 'boolean' ||
    !isRFC3339(value.checked_at) ||
    !isNullableNonNegativeInteger(value.exit_code) ||
    typeof value.diagnostic !== 'string'
  ) {
    throw malformedResponse(status)
  }
  return {
    valid: value.valid,
    checked_at: value.checked_at,
    exit_code: value.exit_code,
    diagnostic: value.diagnostic,
  }
}

function parseRecovery(value: unknown, status: number): RecoveryStatus {
  if (
    !hasExactKeys(value, ['count', 'last_result', 'permanent']) ||
    !Number.isSafeInteger(value.count) ||
    (value.count as number) < 0 ||
    !isOneOf(value.last_result, ['restarting', 'invalid_config', 'permanent_failure']) ||
    typeof value.permanent !== 'boolean'
  ) {
    throw malformedResponse(status)
  }
  return {
    count: value.count as number,
    last_result: value.last_result,
    permanent: value.permanent,
  }
}

function parseWorkspaceList(value: unknown, status: number): WorkspaceSummary[] {
  if (
    !hasExactKeys(value, ['workspaces']) ||
    !Array.isArray(value.workspaces) ||
    value.workspaces.length > 8
  ) {
    throw malformedResponse(status)
  }
  return value.workspaces.map((workspace) => parseWorkspace(workspace, status))
}

function parseWorkspace(value: unknown, status: number): WorkspaceDetail {
  if (
    !hasExactKeys(
      value,
      [
        'id',
        'name',
        'state',
        'production_digest',
        'base_digest',
        'draft_etag',
        'entry_count',
        'managed_bytes',
        'workspace_bytes',
        'created_by',
        'created_at',
        'updated_at',
      ],
      ['state_reason_code'],
    ) ||
    !isOpaqueID(value.id) ||
    !isBoundedString(value.name, 1, 80) ||
    !isOneOf(value.state, ['preparing', 'ready', 'stale', 'needs_attention']) ||
    (value.state_reason_code !== undefined &&
      (typeof value.state_reason_code !== 'string' || !/^[a-z0-9_]*$/.test(value.state_reason_code))) ||
    !isDigest(value.production_digest) ||
    !isDigest(value.base_digest) ||
    !isDraftETag(value.draft_etag) ||
    !isIntegerInRange(value.entry_count, 0, 4096) ||
    !isIntegerInRange(value.managed_bytes, 0, 33_554_432) ||
    !isIntegerInRange(value.workspace_bytes, 0, 536_870_912) ||
    !isIntegerInRange(value.created_by, 1) ||
    !isRFC3339(value.created_at) ||
    !isRFC3339(value.updated_at)
  ) {
    throw malformedResponse(status)
  }
  return {
    id: value.id,
    name: value.name,
    state: value.state,
    ...(value.state_reason_code === undefined ? {} : { state_reason_code: value.state_reason_code }),
    production_digest: value.production_digest,
    base_digest: value.base_digest,
    draft_etag: value.draft_etag,
    entry_count: value.entry_count as number,
    managed_bytes: value.managed_bytes as number,
    workspace_bytes: value.workspace_bytes as number,
    created_by: value.created_by as number,
    created_at: value.created_at,
    updated_at: value.updated_at,
  }
}

function parseConfigTree(value: unknown, status: number): ConfigTree {
  if (
    !hasExactKeys(value, ['entries', 'dependencies', 'draft_etag']) ||
    !Array.isArray(value.entries) ||
    value.entries.length > 4096 ||
    !Array.isArray(value.dependencies) ||
    value.dependencies.length > 16_384 ||
    !isDraftETag(value.draft_etag)
  ) {
    throw malformedResponse(status)
  }
  return {
    entries: value.entries.map((entry) => parseConfigTreeNode(entry, status)),
    dependencies: value.dependencies.map((dependency) => parseConfigDependency(dependency, status)),
    draft_etag: value.draft_etag,
  }
}

function parseConfigTreeNode(value: unknown, status: number): ConfigTreeNode {
  if (
    !hasExactKeys(
      value,
      ['path', 'name', 'entry_type', 'managed', 'read_only', 'status_reason_code'],
      [
        'size_bytes',
        'content_digest',
        'diff_status',
        'dependency_status',
        'dependency_target_count',
        'dependency_cycle',
      ],
    ) ||
    typeof value.path !== 'string' ||
    typeof value.name !== 'string' ||
    !isOneOf(value.entry_type, ['regular', 'directory', 'symlink', 'special']) ||
    typeof value.managed !== 'boolean' ||
    typeof value.read_only !== 'boolean' ||
    !isOneOf(value.status_reason_code, [
      'managed_text',
      'sensitive_material',
      'not_candidate',
      'invalid_text',
      'file_limit',
      'directory',
      'symlink_internal',
      'symlink_external',
      'symlink_unavailable',
      'special',
    ]) ||
    (value.size_bytes !== undefined && !isIntegerInRange(value.size_bytes, 0)) ||
    (value.content_digest !== undefined && !isDigest(value.content_digest)) ||
    (value.diff_status !== undefined &&
      !isOneOf(value.diff_status, ['unchanged', 'created', 'modified', 'deleted'])) ||
    (value.dependency_status !== undefined && !isDependencyStatus(value.dependency_status)) ||
    (value.dependency_target_count !== undefined &&
      !isIntegerInRange(value.dependency_target_count, 0, 16_384)) ||
    (value.dependency_cycle !== undefined && typeof value.dependency_cycle !== 'boolean')
  ) {
    throw malformedResponse(status)
  }
  return {
    path: value.path,
    name: value.name,
    entry_type: value.entry_type,
    managed: value.managed,
    read_only: value.read_only,
    status_reason_code: value.status_reason_code,
    ...(value.size_bytes === undefined ? {} : { size_bytes: value.size_bytes as number }),
    ...(value.content_digest === undefined ? {} : { content_digest: value.content_digest }),
    ...(value.diff_status === undefined ? {} : { diff_status: value.diff_status }),
    ...(value.dependency_status === undefined
      ? {}
      : { dependency_status: value.dependency_status }),
    ...(value.dependency_target_count === undefined
      ? {}
      : { dependency_target_count: value.dependency_target_count as number }),
    ...(value.dependency_cycle === undefined
      ? {}
      : { dependency_cycle: value.dependency_cycle }),
  }
}

function parseConfigDependency(value: unknown, status: number): ConfigDependency {
  if (
    !hasExactKeys(value, ['source', 'line', 'column', 'display_value', 'status', 'cycle'], ['target']) ||
    typeof value.source !== 'string' ||
    !isIntegerInRange(value.line, 1) ||
    !isIntegerInRange(value.column, 1) ||
    !isBoundedString(value.display_value, 0, 262_144) ||
    (value.target !== undefined && typeof value.target !== 'string') ||
    !isDependencyStatus(value.status) ||
    typeof value.cycle !== 'boolean'
  ) {
    throw malformedResponse(status)
  }
  return {
    source: value.source,
    line: value.line as number,
    column: value.column as number,
    display_value: value.display_value,
    ...(value.target === undefined ? {} : { target: value.target }),
    status: value.status,
    cycle: value.cycle,
  }
}

function parseConfigFile(value: unknown, status: number): ConfigFile {
  if (
    !hasExactKeys(value, [
      'path',
      'content',
      'size_bytes',
      'content_digest',
      'line_ending',
      'draft_etag',
    ]) ||
    typeof value.path !== 'string' ||
    typeof value.content !== 'string' ||
    !isIntegerInRange(value.size_bytes, 0, 2_097_152) ||
    !isDigest(value.content_digest) ||
    !isOneOf(value.line_ending, ['none', 'lf', 'crlf', 'mixed']) ||
    !isDraftETag(value.draft_etag)
  ) {
    throw malformedResponse(status)
  }
  return {
    path: value.path,
    content: value.content,
    size_bytes: value.size_bytes as number,
    content_digest: value.content_digest,
    line_ending: value.line_ending,
    draft_etag: value.draft_etag,
  }
}

function parseFileMutationResponse(value: unknown, status: number): FileMutationResponse {
  if (!hasExactKeys(value, ['workspace', 'draft_etag'], ['entry']) || !isDraftETag(value.draft_etag)) {
    throw malformedResponse(status)
  }
  const workspace = parseWorkspace(value.workspace, status)
  if (workspace.draft_etag !== value.draft_etag) {
    throw malformedResponse(status)
  }
  return {
    workspace,
    ...(value.entry === undefined ? {} : { entry: parseConfigTreeNode(value.entry, status) }),
    draft_etag: value.draft_etag,
  }
}

function parseDiffResponse(value: unknown, status: number): DiffResponse {
  if (
    !hasExactKeys(value, ['files', 'complete', 'reason', 'patch']) ||
    !Array.isArray(value.files) ||
    value.files.length > 4096 ||
    typeof value.complete !== 'boolean' ||
    !isOneOf(value.reason, ['', 'response_limit']) ||
    typeof value.patch !== 'string'
  ) {
    throw malformedResponse(status)
  }
  return {
    files: value.files.map((file) => parseFileDiffSummary(file, status)),
    complete: value.complete,
    reason: value.reason,
    patch: value.patch,
  }
}

function parseFileDiffSummary(value: unknown, status: number): FileDiffSummary {
  if (
    !hasExactKeys(value, ['path', 'status', 'added_lines', 'removed_lines']) ||
    typeof value.path !== 'string' ||
    !isOneOf(value.status, ['unchanged', 'created', 'deleted', 'modified']) ||
    !isIntegerInRange(value.added_lines, 0) ||
    !isIntegerInRange(value.removed_lines, 0)
  ) {
    throw malformedResponse(status)
  }
  return {
    path: value.path,
    status: value.status,
    added_lines: value.added_lines as number,
    removed_lines: value.removed_lines as number,
  }
}

function parseSearchResponse(value: unknown, status: number): SearchResponse {
  if (
    !hasExactKeys(value, ['matches', 'complete']) ||
    !Array.isArray(value.matches) ||
    value.matches.length > 500 ||
    typeof value.complete !== 'boolean'
  ) {
    throw malformedResponse(status)
  }
  return {
    matches: value.matches.map((match) => parseSearchMatch(match, status)),
    complete: value.complete,
  }
}

function parseSearchMatch(value: unknown, status: number): SearchMatch {
  if (
    !hasExactKeys(value, ['path', 'line', 'column', 'snippet']) ||
    typeof value.path !== 'string' ||
    !isIntegerInRange(value.line, 1) ||
    !isIntegerInRange(value.column, 1) ||
    !isBoundedString(value.snippet, 0, 240)
  ) {
    throw malformedResponse(status)
  }
  return {
    path: value.path,
    line: value.line as number,
    column: value.column as number,
    snippet: value.snippet,
  }
}

function parseGroupCollection(value: unknown, status: number): GroupCollection {
  if (
    !hasExactKeys(value, ['groups', 'groups_etag']) ||
    !Array.isArray(value.groups) ||
    value.groups.length > 128 ||
    !isGroupsETag(value.groups_etag)
  ) {
    throw malformedResponse(status)
  }
  return {
    groups: value.groups.map((group) => parseConfigGroup(group, status)),
    groups_etag: value.groups_etag,
  }
}

function parseConfigGroup(value: unknown, status: number): ConfigGroup {
  if (
    !hasExactKeys(value, [
      'id',
      'name',
      'sort_order',
      'members',
      'missing',
      'created_by',
      'created_at',
      'updated_at',
    ]) ||
    !isOpaqueID(value.id) ||
    !isBoundedString(value.name, 1, 64) ||
    !Number.isSafeInteger(value.sort_order) ||
    !isUniqueStringArray(value.members, 1024) ||
    !isUniqueStringArray(value.missing, 1024) ||
    !isIntegerInRange(value.created_by, 1) ||
    !isRFC3339(value.created_at) ||
    !isRFC3339(value.updated_at)
  ) {
    throw malformedResponse(status)
  }
  return {
    id: value.id,
    name: value.name,
    sort_order: value.sort_order as number,
    members: [...value.members],
    missing: [...value.missing],
    created_by: value.created_by as number,
    created_at: value.created_at,
    updated_at: value.updated_at,
  }
}

function parseAPIErrorEnvelope(value: unknown, status: number): APIErrorEnvelope {
  if (!hasExactKeys(value, ['error']) || !hasExactKeys(value.error, ['code', 'message', 'request_id'], ['details'])) {
    throw malformedResponse(status)
  }
  const { error } = value
  if (
    !isAPIErrorCode(error.code) ||
    typeof error.message !== 'string' ||
    typeof error.request_id !== 'string' ||
    !/^[A-Za-z0-9._-]{1,64}$/.test(error.request_id)
  ) {
    throw malformedResponse(status)
  }
  const details = parseAPIErrorDetails(error.code, error.details, status)
  return {
    error: {
      code: error.code,
      message: error.message,
      request_id: error.request_id,
      ...(details === undefined ? {} : { details }),
    },
  }
}

function parseAPIErrorDetails(
  code: APIErrorCode,
  value: unknown,
  status: number,
): Readonly<Record<string, string | number>> | undefined {
  if (value === undefined) {
    return undefined
  }
  const allowed = errorDetailKeys(code)
  if (!isRecord(value) || allowed.length === 0 || !hasOnlyAllowedKeys(value, allowed)) {
    throw malformedResponse(status)
  }
  const details: Record<string, string | number> = {}
  for (const [key, detail] of Object.entries(value)) {
    if (!isSafeErrorDetail(key, detail)) {
      throw malformedResponse(status)
    }
    details[key] = detail
  }
  return details
}

function errorDetailKeys(code: APIErrorCode): readonly string[] {
  switch (code) {
    case 'invalid_request':
      return ['field']
    case 'rate_limited':
      return ['retry_after_seconds']
    case 'CONFIG_PATH_INVALID':
      return ['path', 'field']
    case 'CONFIG_ENTRY_NOT_MANAGED':
      return ['path']
    case 'CONFIG_LIMIT_EXCEEDED':
      return ['limit_name', 'limit_value', 'actual']
    case 'CONFIG_WORKSPACE_CONFLICT':
      return ['current_etag', 'field', 'path']
    default:
      return []
  }
}

function isSafeErrorDetail(key: string, value: unknown): value is string | number {
  switch (key) {
    case 'path':
      return typeof value === 'string' && isSafeRelativePath(value)
    case 'current_etag':
      return isDraftETag(value) || isGroupsETag(value)
    case 'field':
      return isOneOf(value, [
        'body',
        'confirm_name',
        'confirm_path',
        'content',
        'destination_path',
        'group_id',
        'members',
        'name',
        'path',
        'query',
        'source_path',
        'workspace_id',
        'username',
      ])
    case 'limit_name':
      return isOneOf(value, [
        'request_body_bytes',
        'file_bytes',
        'entries',
        'managed_bytes',
        'workspaces',
        'workspace_bytes',
        'groups',
        'group_members',
        'total_group_members',
        'diff_response_bytes',
        'search_matches',
        'search_query_bytes',
        'include_token_bytes',
        'include_directive_bytes',
        'include_edges',
        'include_depth',
      ])
    case 'retry_after_seconds':
    case 'limit_value':
    case 'actual':
      return isIntegerInRange(value, 0)
    default:
      return false
  }
}

function malformedResponse(status: number): APIRequestError {
  return new APIRequestError({
    kind: 'malformed_response',
    message: 'API response was malformed',
    status,
  })
}

function parseRetryAfter(value: string | null): number | undefined {
  if (value === null || !/^[1-9]\d*$/.test(value)) {
    return undefined
  }
  const seconds = Number(value)
  return Number.isSafeInteger(seconds) ? seconds : undefined
}

const apiErrorCodes = [
  'invalid_request',
  'invalid_credentials',
  'unauthenticated',
  'origin_rejected',
  'csrf_rejected',
  'rate_limited',
  'unsupported_media_type',
  'service_unavailable',
  'internal_error',
  'AUTH_SESSION_EXPIRED',
  'NGINX_CONFIG_INVALID',
  'NGINX_COMMAND_TIMEOUT',
  'NGINX_OUTPUT_TOO_LARGE',
  'CONFIG_PATH_INVALID',
  'CONFIG_ENTRY_NOT_MANAGED',
  'CONFIG_LIMIT_EXCEEDED',
  'CONFIG_WORKSPACE_NOT_FOUND',
  'CONFIG_WORKSPACE_CONFLICT',
  'CONFIG_WORKSPACE_STALE',
  'CONFIG_WORKSPACE_NEEDS_ATTENTION',
  'CONFIG_SNAPSHOT_CHANGED',
  'AGENT_UNAVAILABLE',
  'CONFIG_OPERATION_TIMEOUT',
] as const satisfies readonly APIErrorCode[]

function isAPIErrorCode(value: unknown): value is APIErrorCode {
  return isOneOf(value, apiErrorCodes)
}

function hasExactKeys<const Required extends string, const Optional extends string>(
  value: unknown,
  required: readonly Required[],
  optional: readonly Optional[] = [],
): value is Record<Required, unknown> & Partial<Record<Optional, unknown>> {
  if (!isRecord(value)) {
    return false
  }
  const allowed = new Set<string>([...required, ...optional])
  return (
    required.every((key) => Object.hasOwn(value, key)) &&
    Object.keys(value).every((key) => allowed.has(key))
  )
}

function hasOnlyAllowedKeys(value: Record<string, unknown>, allowed: readonly string[]): boolean {
  const keys = new Set(allowed)
  return Object.keys(value).every((key) => keys.has(key))
}

function isOpaqueID(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{32}$/.test(value)
}

function isDigest(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{64}$/.test(value)
}

function isDraftETag(value: unknown): value is string {
  return typeof value === 'string' && /^"draft-v1:[0-9a-f]{64}"$/.test(value)
}

function isGroupsETag(value: unknown): value is string {
  return typeof value === 'string' && /^"groups-v1:[0-9a-f]{64}"$/.test(value)
}

function isBoundedString(value: unknown, minimum: number, maximum: number): value is string {
  if (typeof value !== 'string') {
    return false
  }
  const length = Array.from(value).length
  return length >= minimum && length <= maximum
}

function isIntegerInRange(value: unknown, minimum: number, maximum = Number.MAX_SAFE_INTEGER): boolean {
  return Number.isSafeInteger(value) && (value as number) >= minimum && (value as number) <= maximum
}

function isUniqueStringArray(value: unknown, maximum: number): value is string[] {
  return (
    Array.isArray(value) &&
    value.length <= maximum &&
    value.every((item) => typeof item === 'string') &&
    new Set(value).size === value.length
  )
}

function isDependencyStatus(value: unknown): value is ConfigDependency['status'] {
  return isOneOf(value, [
    'resolved',
    'missing',
    'external',
    'unresolved',
    'symlink',
    'special',
    'cycle',
  ])
}

function isSafeRelativePath(value: string): boolean {
  const parts = value.split('/')
  return (
    value !== '' &&
    !value.startsWith('/') &&
    new TextEncoder().encode(value).length <= 1024 &&
    parts.length <= 64 &&
    parts.every((part) => part !== '' && part !== '.' && part !== '..')
  )
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isOneOf<const Value extends string>(
  value: unknown,
  allowed: readonly Value[],
): value is Value {
  return typeof value === 'string' && allowed.some((candidate) => candidate === value)
}

function isNullableNonNegativeInteger(value: unknown): value is number | null {
  return value === null || (Number.isSafeInteger(value) && (value as number) >= 0)
}

function isRFC3339(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value) &&
    !Number.isNaN(Date.parse(value))
  )
}

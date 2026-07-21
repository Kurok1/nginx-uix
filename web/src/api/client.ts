/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import type {
  APIError,
  APIErrorCode,
  APIErrorEnvelope,
  AttentionCase,
  AuditEvent,
  ConfigBackup,
  ConfigRestore,
  ConfigDependency,
  ConfigFile,
  ConfigGroup,
  ConfigTree,
  ConfigTreeNode,
  DiffResponse,
  EffectiveConfigOccurrence,
  EffectiveConfigResponse,
  EffectiveConfigWarning,
  FileDiffSummary,
  FileMutationResponse,
  GroupCollection,
  GroupMutationRequest,
  LoginRequest,
  NginxBuild,
  NginxRestart,
  NginxProcess,
  PublishCheck,
  Release,
  ReleaseStage,
  RestartStage,
  RestoreStage,
  RetentionRun,
  RuntimeVerification,
  CursorPage,
  RecoveryStatus,
  SearchMatch,
  SearchResponse,
  SessionResponse,
  StartupValidation,
  SystemStatusResponse,
  WorkspaceDetail,
  WorkspaceSummary,
} from './types'
import {
  parseStructuredChangePreview,
  parseStructuredChangeResult,
  parseStructuredConfig,
  type StructuredChangePreview,
  type StructuredChangeResult,
  type StructuredConfig,
  type StructuredOperation,
} from './structured'
import {
  parseRouteAnalysis,
  parseRouteHistoryPage,
  parseRouteRun,
  type RouteAnalysis,
  type RouteHistoryPage,
  type RouteHistoryQuery,
  type RouteTestRequest,
  type RouteTestRun,
} from './route_lab'

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

  async getStructuredConfig(
    id: string,
    signal?: AbortSignal,
  ): Promise<StructuredConfig> {
    const response = await this.send(`${workspacePath(id)}/structured-config`, {
      method: 'GET',
      signal,
    })
    let result: StructuredConfig
    try {
      result = parseStructuredConfig(await readJSON(response), response.status)
    } catch {
      throw malformedResponse(response.status)
    }
    requireMatchingETag(response, result.draft_etag)
    return result
  }

  async previewStructuredChange(
    id: string,
    operation: StructuredOperation,
    csrfToken: string,
    signal?: AbortSignal,
  ): Promise<StructuredChangePreview> {
    const response = await this.send(`${workspacePath(id)}/structured-change-previews`, {
      method: 'POST',
      headers: jsonMutationHeaders(csrfToken),
      body: JSON.stringify(operation),
      signal,
    })
    let result: StructuredChangePreview
    try {
      result = parseStructuredChangePreview(await readJSON(response), response.status)
    } catch {
      throw malformedResponse(response.status)
    }
    requireMatchingETag(response, result.draft_etag)
    return result
  }

  async applyStructuredChange(
    id: string,
    operation: StructuredOperation,
    previewID: string,
    etag: string,
    csrfToken: string,
    signal?: AbortSignal,
  ): Promise<StructuredChangeResult> {
    const response = await this.send(`${workspacePath(id)}/structured-changes`, {
      method: 'POST',
      headers: jsonMutationHeaders(csrfToken, etag),
      body: JSON.stringify({ preview_id: previewID, ...operation }),
      signal,
    })
    let result: StructuredChangeResult
    try {
      result = parseStructuredChangeResult(
        await readJSON(response),
        response.status,
        parseWorkspace,
      )
    } catch {
      throw malformedResponse(response.status)
    }
    requireMatchingETag(response, result.draft_etag)
    return result
  }

  async analyzeRoute(
    workspaceId: string,
    input: RouteTestRequest,
    etag: string,
    csrfToken: string,
    signal?: AbortSignal,
  ): Promise<RouteAnalysis> {
    const response = await this.send(`${workspacePath(workspaceId)}/route-analyses`, {
      method: 'POST',
      headers: jsonMutationHeaders(csrfToken, etag),
      body: JSON.stringify(input),
      signal,
    })
    try {
      return parseRouteAnalysis(await readJSON(response), response.status)
    } catch {
      throw malformedResponse(response.status)
    }
  }

  async createRouteTest(
    workspaceId: string,
    input: RouteTestRequest,
    etag: string,
    csrfToken: string,
    signal?: AbortSignal,
  ): Promise<RouteTestRun> {
    const response = await this.send(`${workspacePath(workspaceId)}/route-tests`, {
      method: 'POST',
      headers: jsonMutationHeaders(csrfToken, etag),
      body: JSON.stringify(input),
      signal,
    })
    let result: RouteTestRun
    try {
      result = parseRouteRun(await readJSON(response), response.status)
    } catch {
      throw malformedResponse(response.status)
    }
    requireLocation(response, 202, `/api/v1/route-tests/${result.id}`)
    return result
  }

  async getRouteTest(id: string, signal?: AbortSignal): Promise<RouteTestRun> {
    const response = await this.send(`/api/v1/route-tests/${id}`, { method: 'GET', signal })
    try {
      return parseRouteRun(await readJSON(response), response.status)
    } catch {
      throw malformedResponse(response.status)
    }
  }

  async listRouteTests(
    query: RouteHistoryQuery = {},
    signal?: AbortSignal,
  ): Promise<RouteHistoryPage> {
    const response = await this.send(
      optionalQuery('/api/v1/route-tests', {
        workspace_id: query.workspace_id,
        state: query.state,
        cursor: query.cursor,
        limit: query.limit === undefined ? undefined : String(query.limit),
      }),
      { method: 'GET', signal },
    )
    try {
      return parseRouteHistoryPage(await readJSON(response), response.status)
    } catch {
      throw malformedResponse(response.status)
    }
  }

  async cancelRouteTest(
    id: string,
    csrfToken: string,
    signal?: AbortSignal,
  ): Promise<RouteTestRun> {
    const response = await this.send(`/api/v1/route-tests/${id}/cancellations`, {
      method: 'POST',
      headers: jsonMutationHeaders(csrfToken),
      body: '{}',
      signal,
    })
    try {
      return parseRouteRun(await readJSON(response), response.status)
    } catch {
      throw malformedResponse(response.status)
    }
  }

	async createPublishCheck(
		workspaceId: string,
		etag: string,
		csrfToken: string,
		signal?: AbortSignal,
	): Promise<PublishCheck> {
		const response = await this.send(
			`${workspacePath(workspaceId)}/publish-checks`,
			{
				method: 'POST',
				headers: jsonMutationHeaders(csrfToken, etag),
				body: '{}',
				signal,
			},
			[422],
		)
		return parsePublishCheck(await readJSON(response), response.status)
	}

	async getPublishCheck(id: string, signal?: AbortSignal): Promise<PublishCheck> {
		const response = await this.send(`/api/v1/config/publish-checks/${id}`, {
			method: 'GET',
			signal,
		})
		return parsePublishCheck(await readJSON(response), response.status)
	}

	async createRelease(
		workspaceId: string,
		checkId: string,
		confirmName: string,
		etag: string,
		csrfToken: string,
	): Promise<Release> {
		const response = await this.send(`${workspacePath(workspaceId)}/releases`, {
			method: 'POST',
			headers: jsonMutationHeaders(csrfToken, etag),
			body: JSON.stringify({ check_id: checkId, confirm_name: confirmName }),
		})
		const release = parseRelease(await readJSON(response), response.status)
		if (response.status !== 202 || response.headers.get('Location') !== `/api/v1/config/releases/${release.id}`) {
			throw malformedResponse(response.status)
		}
		return release
	}

	async getRelease(id: string, signal?: AbortSignal): Promise<Release> {
		const response = await this.send(`/api/v1/config/releases/${id}`, {
			method: 'GET',
			signal,
		})
		return parseRelease(await readJSON(response), response.status)
	}

	async listBackups(
		options: { cursor?: string; includeDeleted?: boolean; signal?: AbortSignal } = {},
	): Promise<CursorPage<ConfigBackup>> {
		const response = await this.send(
			optionalQuery('/api/v1/config/backups', {
				cursor: options.cursor,
				include_deleted: options.includeDeleted === undefined ? undefined : String(options.includeDeleted),
			}),
			{ method: 'GET', signal: options.signal },
		)
		return parseCursorPage(await readJSON(response), response.status, parseBackup)
	}

	async getBackup(id: string, signal?: AbortSignal): Promise<ConfigBackup> {
		const response = await this.send(`/api/v1/config/backups/${id}`, { method: 'GET', signal })
		return parseBackup(await readJSON(response), response.status)
	}

	async changeBackupProtection(
		id: string,
		input: {
			expected_protected: boolean
			protected: boolean
			reason: string
			confirmation: string
		},
		csrfToken: string,
	): Promise<ConfigBackup> {
		const response = await this.send(`/api/v1/config/backups/${id}/protection`, {
			method: 'PUT', headers: jsonMutationHeaders(csrfToken), body: JSON.stringify(input),
		})
		return parseBackup(await readJSON(response), response.status)
	}

	async planBackupRetention(csrfToken: string): Promise<RetentionRun> {
		const response = await this.send('/api/v1/config/backup-retention-runs', {
			method: 'POST', headers: jsonMutationHeaders(csrfToken), body: '{}',
		})
		const run = parseRetentionRun(await readJSON(response), response.status)
		requireLocation(response, 201, `/api/v1/config/backup-retention-runs/${run.id}`)
		return run
	}

	async executeBackupRetention(
		id: string,
		confirmation: string,
		csrfToken: string,
	): Promise<RetentionRun> {
		const response = await this.send(`/api/v1/config/backup-retention-runs/${id}/executions`, {
			method: 'POST', headers: jsonMutationHeaders(csrfToken), body: JSON.stringify({ confirmation }),
		})
		const run = parseRetentionRun(await readJSON(response), response.status)
		requireLocation(response, 202, `/api/v1/config/backup-retention-runs/${run.id}`)
		return run
	}

	async getBackupRetention(id: string, signal?: AbortSignal): Promise<RetentionRun> {
		const response = await this.send(`/api/v1/config/backup-retention-runs/${id}`, {
			method: 'GET', signal,
		})
		return parseRetentionRun(await readJSON(response), response.status)
	}

	async createRestore(
		backupId: string,
		input: { attention_case_id: string; reason: string; confirm_backup_id: string },
		csrfToken: string,
	): Promise<ConfigRestore> {
		const response = await this.send(`/api/v1/config/backups/${backupId}/restores`, {
			method: 'POST', headers: jsonMutationHeaders(csrfToken), body: JSON.stringify(input),
		})
		const restore = parseRestore(await readJSON(response), response.status)
		requireLocation(response, 202, `/api/v1/config/restores/${restore.id}`)
		return restore
	}

	async getRestore(id: string, signal?: AbortSignal): Promise<ConfigRestore> {
		const response = await this.send(`/api/v1/config/restores/${id}`, { method: 'GET', signal })
		return parseRestore(await readJSON(response), response.status)
	}

	async createRestart(
		input: { attention_case_id: string; reason: string; confirmation: string },
		csrfToken: string,
	): Promise<NginxRestart> {
		const response = await this.send('/api/v1/nginx/restarts', {
			method: 'POST', headers: jsonMutationHeaders(csrfToken), body: JSON.stringify(input),
		})
		const restart = parseRestart(await readJSON(response), response.status)
		requireLocation(response, 202, `/api/v1/nginx/restarts/${restart.id}`)
		return restart
	}

	async getRestart(id: string, signal?: AbortSignal): Promise<NginxRestart> {
		const response = await this.send(`/api/v1/nginx/restarts/${id}`, { method: 'GET', signal })
		return parseRestart(await readJSON(response), response.status)
	}

	async listReleaseHistory(cursor?: string, signal?: AbortSignal): Promise<CursorPage<Release>> {
		return this.historyPage('/api/v1/config/history/releases', cursor, signal, parseRelease)
	}

	async listRestoreHistory(cursor?: string, signal?: AbortSignal): Promise<CursorPage<ConfigRestore>> {
		return this.historyPage('/api/v1/config/history/restores', cursor, signal, parseRestore)
	}

	async listRestartHistory(cursor?: string, signal?: AbortSignal): Promise<CursorPage<NginxRestart>> {
		return this.historyPage('/api/v1/config/history/restarts', cursor, signal, parseRestart)
	}

	async listAuditEvents(cursor?: string, signal?: AbortSignal): Promise<CursorPage<AuditEvent>> {
		return this.historyPage('/api/v1/config/audit-events', cursor, signal, parseAuditEvent)
	}

	async listAttentionCases(
		options: { state?: 'open' | 'resolved'; cursor?: string; signal?: AbortSignal } = {},
	): Promise<CursorPage<AttentionCase>> {
		const response = await this.send(optionalQuery('/api/v1/config/attention-cases', {
			state: options.state, cursor: options.cursor,
		}), { method: 'GET', signal: options.signal })
		return parseCursorPage(await readJSON(response), response.status, parseAttentionCase)
	}

	async getAttentionCase(id: string, signal?: AbortSignal): Promise<AttentionCase> {
		const response = await this.send(`/api/v1/config/attention-cases/${id}`, {
			method: 'GET', signal,
		})
		return parseAttentionCase(await readJSON(response), response.status)
	}

	async verifyAttentionCase(id: string, csrfToken: string): Promise<RuntimeVerification> {
		const response = await this.send(`/api/v1/config/attention-cases/${id}/verifications`, {
			method: 'POST', headers: jsonMutationHeaders(csrfToken), body: '{}',
		})
		return parseRuntimeVerification(await readJSON(response), response.status)
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

	private async historyPage<T>(
		path: string,
		cursor: string | undefined,
		signal: AbortSignal | undefined,
		parseItem: (value: unknown, status: number) => T,
	): Promise<CursorPage<T>> {
		const response = await this.send(optionalQuery(path, { cursor }), { method: 'GET', signal })
		return parseCursorPage(await readJSON(response), response.status, parseItem)
	}

	private async send(path: string, init: RequestInit, acceptedStatuses: readonly number[] = []): Promise<Response> {
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

		if (response.ok || acceptedStatuses.includes(response.status)) {
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

function optionalQuery(
	path: string,
	query: Readonly<Record<string, string | undefined>>,
): string {
	const defined = Object.entries(query).filter((entry): entry is [string, string] => entry[1] !== undefined)
	return defined.length === 0 ? path : withQuery(path, Object.fromEntries(defined))
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

function requireLocation(response: Response, status: number, location: string): void {
	if (response.status !== status || response.headers.get('Location') !== location) {
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
      'display_mode',
      'raw_content',
      'warnings',
    ]) ||
    !isRFC3339(value.generated_at) ||
    typeof value.nginx_version !== 'string' ||
    typeof value.entry_config_path !== 'string' ||
    !isOneOf(value.display_mode, ['structured', 'raw']) ||
    !Number.isSafeInteger(value.occurrence_count) ||
    (value.occurrence_count as number) < 0 ||
    !Array.isArray(value.occurrences) ||
    value.occurrences.length !== value.occurrence_count ||
    !Array.isArray(value.warnings) ||
    !value.warnings.every((warning) =>
      isOneOf(warning, [
        'NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS',
        'NGINX_CONFIG_STRUCTURE_UNVERIFIED',
      ]),
    )
  ) {
    throw malformedResponse(status)
  }

	const base = {
		generated_at: value.generated_at,
		nginx_version: value.nginx_version,
		entry_config_path: value.entry_config_path,
	}
	const warnings = [...value.warnings] as EffectiveConfigWarning[]
	if (value.display_mode === 'raw') {
		if (
			value.occurrence_count !== 0 ||
			value.occurrences.length !== 0 ||
			typeof value.raw_content !== 'string' ||
			value.raw_content === '' ||
			warnings.length === 0 ||
			new Set(warnings).size !== warnings.length
		) {
			throw malformedResponse(status)
		}
		return {
			...base,
			display_mode: 'raw',
			occurrence_count: 0,
			occurrences: [],
			raw_content: value.raw_content,
			warnings,
		}
	}
	if (value.raw_content !== null || warnings.length !== 0) {
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
		...base,
		display_mode: 'structured',
    occurrence_count: value.occurrence_count as number,
    occurrences,
		raw_content: null,
		warnings,
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
	      ['state_reason_code', 'last_release_id'],
    ) ||
    !isOpaqueID(value.id) ||
    !isBoundedString(value.name, 1, 80) ||
	    !isOneOf(value.state, ['preparing', 'ready', 'stale', 'published', 'needs_attention']) ||
	    (value.state_reason_code !== undefined &&
	      (typeof value.state_reason_code !== 'string' || !/^[a-z0-9_]*$/.test(value.state_reason_code))) ||
	    (value.last_release_id !== undefined && !isOpaqueID(value.last_release_id)) ||
	    (value.state === 'published' && value.last_release_id === undefined) ||
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
	    ...(value.last_release_id === undefined ? {} : { last_release_id: value.last_release_id }),
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

function parsePublishCheck(value: unknown, status: number): PublishCheck {
	if (
		!hasExactKeys(value, [
			'id',
			'workspace_id',
			'workspace_revision',
			'production_digest',
			'base_digest',
			'draft_digest',
			'candidate_digest',
			'manifest_version',
			'policy_version',
			'validator_version',
			'validator_build_id',
			'state',
			'diagnostic_count',
			'details',
			'started_at',
			'finished_at',
			'expires_at',
		]) ||
		!isOpaqueID(value.id) ||
		!isOpaqueID(value.workspace_id) ||
		!isIntegerInRange(value.workspace_revision, 1) ||
		!isDigest(value.production_digest) ||
		!isDigest(value.base_digest) ||
		!isDigest(value.draft_digest) ||
		!isDigest(value.candidate_digest) ||
		!isIntegerInRange(value.manifest_version, 1, 65_535) ||
		!isIntegerInRange(value.policy_version, 1, 65_535) ||
		!isIntegerInRange(value.validator_version, 1, 65_535) ||
		!isBoundedString(value.validator_build_id, 1, 128) ||
		!isOneOf(value.state, ['running', 'valid', 'invalid', 'failed']) ||
		!isIntegerInRange(value.diagnostic_count, 0, 128) ||
		!hasExactKeys(value.details, ['diagnostics']) ||
		!Array.isArray(value.details.diagnostics) ||
		value.details.diagnostics.length !== value.diagnostic_count ||
		!isRFC3339(value.started_at) ||
		!isRFC3339(value.finished_at) ||
		!isRFC3339(value.expires_at) ||
		Date.parse(value.started_at) > Date.parse(value.finished_at) ||
		Date.parse(value.finished_at) >= Date.parse(value.expires_at)
	) {
		throw malformedResponse(status)
	}
	const diagnostics = value.details.diagnostics.map((diagnostic) =>
		parseCandidateDiagnostic(diagnostic, status),
	)
	if ((value.state === 'valid' && diagnostics.length !== 0) || (value.state === 'invalid' && diagnostics.length === 0)) {
		throw malformedResponse(status)
	}
	return {
		id: value.id,
		workspace_id: value.workspace_id,
		workspace_revision: value.workspace_revision as number,
		production_digest: value.production_digest,
		base_digest: value.base_digest,
		draft_digest: value.draft_digest,
		candidate_digest: value.candidate_digest,
		manifest_version: value.manifest_version as number,
		policy_version: value.policy_version as number,
		validator_version: value.validator_version as number,
		validator_build_id: value.validator_build_id,
		state: value.state,
		diagnostic_count: value.diagnostic_count as number,
		details: { diagnostics },
		started_at: value.started_at,
		finished_at: value.finished_at,
		expires_at: value.expires_at,
	}
}

function parseCandidateDiagnostic(value: unknown, status: number): PublishCheck['details']['diagnostics'][number] {
	if (
		!hasExactKeys(value, ['code', 'path', 'line', 'summary']) ||
		!isBoundedString(value.code, 1, 64) ||
		!/^[a-z0-9_]+$/.test(value.code) ||
		(typeof value.path !== 'string' || (value.path !== '' && !isSafeRelativePath(value.path))) ||
		!isIntegerInRange(value.line, 0, 10_000_000) ||
		!isBoundedString(value.summary, 1, 512)
	) {
		throw malformedResponse(status)
	}
	return { code: value.code, path: value.path, line: value.line as number, summary: value.summary }
}

function parseRelease(value: unknown, status: number): Release {
	if (
		!hasExactKeys(
			value,
			[
				'id',
				'workspace_id',
				'check_id',
				'state',
				'stage',
				'production_digest',
				'draft_digest',
				'candidate_digest',
				'created_at',
				'updated_at',
				'stages',
			],
			['backup_id', 'last_error_code', 'finished_at'],
		) ||
		!isOpaqueID(value.id) ||
		!isOpaqueID(value.workspace_id) ||
		!isOpaqueID(value.check_id) ||
		(value.backup_id !== undefined && !isOpaqueID(value.backup_id)) ||
		!isOneOf(value.state, [
			'queued',
			'running',
			'rolling_back',
			'succeeded',
			'failed',
			'rolled_back',
			'needs_attention',
			'cancelled',
		]) ||
		!isReleaseStageName(value.stage) ||
		!isDigest(value.production_digest) ||
		!isDigest(value.draft_digest) ||
		!isDigest(value.candidate_digest) ||
		(value.last_error_code !== undefined && !isBoundedString(value.last_error_code, 1, 128)) ||
		!isRFC3339(value.created_at) ||
		!isRFC3339(value.updated_at) ||
		(value.finished_at !== undefined && !isRFC3339(value.finished_at)) ||
		!Array.isArray(value.stages) ||
		value.stages.length > 512
	) {
		throw malformedResponse(status)
	}
	const terminal = isOneOf(value.state, ['succeeded', 'failed', 'rolled_back', 'needs_attention', 'cancelled'])
	if (terminal !== (value.finished_at !== undefined)) {
		throw malformedResponse(status)
	}
	const stages = value.stages.map((stage, index) => parseReleaseStage(stage, index + 1, status))
	return {
		id: value.id,
		workspace_id: value.workspace_id,
		check_id: value.check_id,
		...(value.backup_id === undefined ? {} : { backup_id: value.backup_id }),
		state: value.state,
		stage: value.stage,
		production_digest: value.production_digest,
		draft_digest: value.draft_digest,
		candidate_digest: value.candidate_digest,
		...(value.last_error_code === undefined ? {} : { last_error_code: value.last_error_code }),
		created_at: value.created_at,
		updated_at: value.updated_at,
		...(value.finished_at === undefined ? {} : { finished_at: value.finished_at }),
		stages,
	}
}

function parseReleaseStage(value: unknown, sequence: number, status: number): ReleaseStage {
	if (
		!hasExactKeys(value, ['sequence', 'stage', 'result', 'details', 'occurred_at'], ['code']) ||
		value.sequence !== sequence ||
		!isReleaseStageName(value.stage) ||
		!isOneOf(value.result, ['pending', 'running', 'success', 'failed', 'warning']) ||
		(value.code !== undefined && !isBoundedString(value.code, 1, 128)) ||
		!isRecord(value.details) ||
		!isRFC3339(value.occurred_at)
	) {
		throw malformedResponse(status)
	}
	return {
		sequence,
		stage: value.stage,
		result: value.result,
		...(value.code === undefined ? {} : { code: value.code }),
		details: { ...value.details },
		occurred_at: value.occurred_at,
	}
}

function isReleaseStageName(value: unknown): value is ReleaseStage['stage'] {
	return isOneOf(value, [
		'queued',
		'rechecking',
		'backup_creating',
		'backup_verified',
		'candidate_validated',
		'files_applying',
		'files_applied',
		'production_validated',
		'reload_requested',
		'runtime_confirmed',
		'committed',
		'rollback_applying',
		'rollback_files_restored',
		'rollback_validated',
		'rollback_reload_requested',
		'rolled_back',
		'failed',
		'needs_attention',
	])
}

function parseCursorPage<T>(
	value: unknown,
	status: number,
	parseItem: (item: unknown, status: number) => T,
): CursorPage<T> {
	if (
		!hasExactKeys(value, ['items'], ['next_cursor']) ||
		!Array.isArray(value.items) ||
		value.items.length > 100 ||
		(value.next_cursor !== undefined &&
			(typeof value.next_cursor !== 'string' || !/^[A-Za-z0-9_-]{1,1024}$/.test(value.next_cursor)))
	) {
		throw malformedResponse(status)
	}
	return {
		items: value.items.map((item) => parseItem(item, status)),
		...(value.next_cursor === undefined ? {} : { next_cursor: value.next_cursor }),
	}
}

function parseBackup(value: unknown, status: number): ConfigBackup {
	if (
		!hasExactKeys(
			value,
			[
				'id', 'origin_type', 'origin_id', 'production_digest', 'state', 'entry_count',
				'total_bytes', 'body_present', 'protected', 'manually_protected', 'protections', 'created_at',
			],
			['release_id', 'protection_reason', 'verified_at', 'deleted_at'],
		) ||
		!isOpaqueID(value.id) ||
		!isOneOf(value.origin_type, ['release', 'restore']) ||
		!isOpaqueID(value.origin_id) ||
		(value.release_id !== undefined && !isOpaqueID(value.release_id)) ||
		(value.origin_type === 'release' && value.release_id !== value.origin_id) ||
		(value.origin_type === 'restore' && value.release_id !== undefined) ||
		!isDigest(value.production_digest) ||
		!isOneOf(value.state, ['creating', 'complete', 'invalid', 'deleting', 'deleted']) ||
		!isIntegerInRange(value.entry_count, 0, 4096) ||
		!isIntegerInRange(value.total_bytes, 0) ||
		typeof value.body_present !== 'boolean' ||
		typeof value.protected !== 'boolean' ||
		typeof value.manually_protected !== 'boolean' ||
		(value.protection_reason !== undefined && !isBoundedString(value.protection_reason, 1, 256)) ||
		!Array.isArray(value.protections) || value.protections.length > 16 ||
		!isRFC3339(value.created_at) ||
		(value.verified_at !== undefined && !isRFC3339(value.verified_at)) ||
		(value.deleted_at !== undefined && !isRFC3339(value.deleted_at)) ||
		(value.state === 'deleted') !== (value.deleted_at !== undefined) ||
		(value.state === 'deleted' && value.body_present)
	) {
		throw malformedResponse(status)
	}
	const protections = value.protections.map((reason) => parseBackupProtection(reason, status))
	return {
		id: value.id, origin_type: value.origin_type, origin_id: value.origin_id,
		...(value.release_id === undefined ? {} : { release_id: value.release_id }),
		production_digest: value.production_digest, state: value.state,
		entry_count: value.entry_count as number, total_bytes: value.total_bytes as number,
		body_present: value.body_present, protected: value.protected,
		manually_protected: value.manually_protected,
		...(value.protection_reason === undefined ? {} : { protection_reason: value.protection_reason }),
		protections, created_at: value.created_at,
		...(value.verified_at === undefined ? {} : { verified_at: value.verified_at }),
		...(value.deleted_at === undefined ? {} : { deleted_at: value.deleted_at }),
	}
}

function parseBackupProtection(value: unknown, status: number): ConfigBackup['protections'][number] {
	if (!hasExactKeys(value, ['kind', 'code']) || !isBoundedCode(value.kind) || !isBoundedCode(value.code)) {
		throw malformedResponse(status)
	}
	return { kind: value.kind, code: value.code }
}

function parseRestore(value: unknown, status: number): ConfigRestore {
	if (
		!hasExactKeys(
			value,
			['id', 'target_backup_id', 'safety_backup_id', 'state', 'stage', 'source_digest',
				'target_digest', 'reason', 'request_id', 'created_at', 'updated_at', 'stages'],
			['attention_case_id', 'last_error_code', 'finished_at'],
		) ||
		!isOpaqueID(value.id) || !isOpaqueID(value.target_backup_id) || !isOpaqueID(value.safety_backup_id) ||
		(value.attention_case_id !== undefined && !isOpaqueID(value.attention_case_id)) ||
		!isOneOf(value.state, ['queued', 'running', 'rolling_back', 'succeeded', 'failed', 'rolled_back', 'needs_attention', 'cancelled']) ||
		!isRestoreStageName(value.stage) || !isDigest(value.source_digest) || !isDigest(value.target_digest) ||
		(value.last_error_code !== undefined && !isBoundedCode(value.last_error_code)) ||
		!isBoundedString(value.reason, 1, 256) || !isRequestID(value.request_id) ||
		!isRFC3339(value.created_at) || !isRFC3339(value.updated_at) ||
		(value.finished_at !== undefined && !isRFC3339(value.finished_at)) ||
		!Array.isArray(value.stages) || value.stages.length > 512
	) {
		throw malformedResponse(status)
	}
	const terminal = isOneOf(value.state, ['succeeded', 'failed', 'rolled_back', 'needs_attention', 'cancelled'])
	if (terminal !== (value.finished_at !== undefined)) throw malformedResponse(status)
	const stages = value.stages.map((stage, index) => parseRestoreStage(stage, index + 1, status))
	return {
		id: value.id, target_backup_id: value.target_backup_id, safety_backup_id: value.safety_backup_id,
		...(value.attention_case_id === undefined ? {} : { attention_case_id: value.attention_case_id }),
		state: value.state, stage: value.stage, source_digest: value.source_digest,
		target_digest: value.target_digest,
		...(value.last_error_code === undefined ? {} : { last_error_code: value.last_error_code }),
		reason: value.reason, request_id: value.request_id, created_at: value.created_at,
		updated_at: value.updated_at,
		...(value.finished_at === undefined ? {} : { finished_at: value.finished_at }), stages,
	}
}

function parseRestoreStage(value: unknown, sequence: number, status: number): RestoreStage {
	if (!hasExactKeys(value, ['sequence', 'stage', 'result', 'details', 'occurred_at'], ['code']) ||
		value.sequence !== sequence || !isRestoreStageName(value.stage) ||
		!isOneOf(value.result, ['pending', 'running', 'success', 'failed', 'warning']) ||
		(value.code !== undefined && !isBoundedCode(value.code)) || !isRecord(value.details) ||
		!isRFC3339(value.occurred_at)) throw malformedResponse(status)
	return { sequence, stage: value.stage, result: value.result,
		...(value.code === undefined ? {} : { code: value.code }), details: { ...value.details },
		occurred_at: value.occurred_at }
}

function isRestoreStageName(value: unknown): value is RestoreStage['stage'] {
	return isOneOf(value, [
		'queued', 'target_verifying', 'target_validated', 'safety_backup_creating',
		'safety_backup_verified', 'files_restoring', 'files_restored', 'production_validated',
		'reload_requested', 'runtime_confirmed', 'succeeded', 'rollback_applying',
		'rollback_files_restored', 'rollback_validated', 'rollback_reload_requested',
		'rolled_back', 'failed', 'needs_attention',
	])
}

function parseRestart(value: unknown, status: number): NginxRestart {
	if (!hasExactKeys(value,
		['id', 'state', 'stage', 'production_digest', 'worker_count', 'reason', 'request_id',
			'created_at', 'updated_at', 'stages'],
		['attention_case_id', 'before_master_pid', 'after_master_pid', 'http_status',
			'last_error_code', 'finished_at']) ||
		!isOpaqueID(value.id) || (value.attention_case_id !== undefined && !isOpaqueID(value.attention_case_id)) ||
		!isOneOf(value.state, ['queued', 'running', 'succeeded', 'failed', 'needs_attention', 'cancelled']) ||
		!isRestartStageName(value.stage) || !isDigest(value.production_digest) ||
		(value.before_master_pid !== undefined && !isIntegerInRange(value.before_master_pid, 1)) ||
		(value.after_master_pid !== undefined && !isIntegerInRange(value.after_master_pid, 1)) ||
		!isIntegerInRange(value.worker_count, 0) ||
		(value.http_status !== undefined && !isIntegerInRange(value.http_status, 100, 599)) ||
		(value.last_error_code !== undefined && !isBoundedCode(value.last_error_code)) ||
		!isBoundedString(value.reason, 1, 256) || !isRequestID(value.request_id) ||
		!isRFC3339(value.created_at) || !isRFC3339(value.updated_at) ||
		(value.finished_at !== undefined && !isRFC3339(value.finished_at)) ||
		!Array.isArray(value.stages) || value.stages.length > 512) throw malformedResponse(status)
	const terminal = isOneOf(value.state, ['succeeded', 'failed', 'needs_attention', 'cancelled'])
	if (terminal !== (value.finished_at !== undefined)) throw malformedResponse(status)
	const stages = value.stages.map((stage, index) => parseRestartStage(stage, index + 1, status))
	return {
		id: value.id, ...(value.attention_case_id === undefined ? {} : { attention_case_id: value.attention_case_id }),
		state: value.state, stage: value.stage, production_digest: value.production_digest,
		...(value.before_master_pid === undefined ? {} : { before_master_pid: value.before_master_pid as number }),
		...(value.after_master_pid === undefined ? {} : { after_master_pid: value.after_master_pid as number }),
		worker_count: value.worker_count as number,
		...(value.http_status === undefined ? {} : { http_status: value.http_status as number }),
		...(value.last_error_code === undefined ? {} : { last_error_code: value.last_error_code }),
		reason: value.reason, request_id: value.request_id, created_at: value.created_at, updated_at: value.updated_at,
		...(value.finished_at === undefined ? {} : { finished_at: value.finished_at }), stages,
	}
}

function parseRestartStage(value: unknown, sequence: number, status: number): RestartStage {
	if (!hasExactKeys(value, ['sequence', 'stage', 'result', 'details', 'occurred_at'], ['code']) ||
		value.sequence !== sequence || !isRestartStageName(value.stage) ||
		!isOneOf(value.result, ['pending', 'running', 'success', 'failed', 'warning']) ||
		(value.code !== undefined && !isBoundedCode(value.code)) || !isRecord(value.details) ||
		!isRFC3339(value.occurred_at)) throw malformedResponse(status)
	return { sequence, stage: value.stage, result: value.result,
		...(value.code === undefined ? {} : { code: value.code }), details: { ...value.details },
		occurred_at: value.occurred_at }
}

function isRestartStageName(value: unknown): value is RestartStage['stage'] {
	return isOneOf(value, ['queued', 'production_validating', 'runtime_sampling', 'restart_requested',
		'runtime_confirming', 'succeeded', 'failed', 'needs_attention'])
}

function parseRetentionRun(value: unknown, status: number): RetentionRun {
	if (!hasExactKeys(value,
		['id', 'state', 'policy', 'backup_count', 'total_bytes', 'protected_count', 'delete_count',
			'delete_bytes', 'deleted_count', 'deleted_bytes', 'created_at', 'expires_at', 'items'],
		['last_error_code', 'started_at', 'finished_at']) || !isOpaqueID(value.id) ||
		!isOneOf(value.state, ['planned', 'executing', 'succeeded', 'failed', 'needs_attention', 'expired']) ||
		!hasExactKeys(value.policy, ['minimum_complete', 'maximum_complete', 'maximum_total_bytes', 'minimum_age_seconds']) ||
		!isIntegerInRange(value.policy.minimum_complete, 1) ||
		!isIntegerInRange(value.policy.maximum_complete, value.policy.minimum_complete as number) ||
		!isIntegerInRange(value.policy.maximum_total_bytes, 1) || !isIntegerInRange(value.policy.minimum_age_seconds, 0) ||
		!isIntegerInRange(value.backup_count, 0, 4096) || !isIntegerInRange(value.total_bytes, 0) ||
		!isIntegerInRange(value.protected_count, 0) || !isIntegerInRange(value.delete_count, 0) ||
		!isIntegerInRange(value.delete_bytes, 0) || !isIntegerInRange(value.deleted_count, 0) ||
		!isIntegerInRange(value.deleted_bytes, 0) ||
		(value.last_error_code !== undefined && !isBoundedCode(value.last_error_code)) ||
		!isRFC3339(value.created_at) || !isRFC3339(value.expires_at) ||
		(value.started_at !== undefined && !isRFC3339(value.started_at)) ||
		(value.finished_at !== undefined && !isRFC3339(value.finished_at)) ||
		!Array.isArray(value.items) || value.items.length > 4096) throw malformedResponse(status)
	const items = value.items.map((item) => parseRetentionItem(item, status))
	return {
		id: value.id, state: value.state,
		policy: {
			minimum_complete: value.policy.minimum_complete as number,
			maximum_complete: value.policy.maximum_complete as number,
			maximum_total_bytes: value.policy.maximum_total_bytes as number,
			minimum_age_seconds: value.policy.minimum_age_seconds as number,
		},
		backup_count: value.backup_count as number, total_bytes: value.total_bytes as number,
		protected_count: value.protected_count as number, delete_count: value.delete_count as number,
		delete_bytes: value.delete_bytes as number, deleted_count: value.deleted_count as number,
		deleted_bytes: value.deleted_bytes as number,
		...(value.last_error_code === undefined ? {} : { last_error_code: value.last_error_code }),
		created_at: value.created_at, expires_at: value.expires_at,
		...(value.started_at === undefined ? {} : { started_at: value.started_at }),
		...(value.finished_at === undefined ? {} : { finished_at: value.finished_at }), items,
	}
}

function parseRetentionItem(value: unknown, status: number): RetentionRun['items'][number] {
	if (!hasExactKeys(value, ['ordinal', 'backup_id', 'decision', 'reason_code', 'state',
		'snapshot_created_at', 'snapshot_total_bytes']) || !isIntegerInRange(value.ordinal, 0, 4095) ||
		!isOpaqueID(value.backup_id) || !isOneOf(value.decision, ['keep', 'delete']) ||
		!isBoundedCode(value.reason_code) || !isOneOf(value.state, ['planned', 'kept', 'deleting',
			'deleted', 'skipped_protected', 'failed', 'needs_attention']) ||
		!isRFC3339(value.snapshot_created_at) || !isIntegerInRange(value.snapshot_total_bytes, 0)) {
		throw malformedResponse(status)
	}
	return { ordinal: value.ordinal as number, backup_id: value.backup_id, decision: value.decision,
		reason_code: value.reason_code, state: value.state, snapshot_created_at: value.snapshot_created_at,
		snapshot_total_bytes: value.snapshot_total_bytes as number }
}

function parseAttentionCase(value: unknown, status: number): AttentionCase {
	if (!hasExactKeys(value, ['id', 'subject_type', 'subject_id', 'state', 'reason_code', 'opened_at'],
		['workspace_id', 'backup_id', 'resolved_at', 'resolution_type', 'resolution_id']) ||
		!isOpaqueID(value.id) || !isOneOf(value.subject_type, ['workspace', 'release', 'restore', 'restart']) ||
		!isOpaqueID(value.subject_id) || (value.workspace_id !== undefined && !isOpaqueID(value.workspace_id)) ||
		(value.backup_id !== undefined && !isOpaqueID(value.backup_id)) ||
		!isOneOf(value.state, ['open', 'resolved']) || !isBoundedCode(value.reason_code) ||
		!isRFC3339(value.opened_at) || (value.resolved_at !== undefined && !isRFC3339(value.resolved_at)) ||
		(value.resolution_type !== undefined && !isOneOf(value.resolution_type, ['restore', 'restart', 'verification'])) ||
		(value.resolution_id !== undefined && !isOpaqueID(value.resolution_id))) throw malformedResponse(status)
	const resolvedFields = value.resolved_at !== undefined && value.resolution_type !== undefined && value.resolution_id !== undefined
	if ((value.state === 'resolved') !== resolvedFields || (value.state === 'open' &&
		(value.resolved_at !== undefined || value.resolution_type !== undefined || value.resolution_id !== undefined))) {
		throw malformedResponse(status)
	}
	return { id: value.id, subject_type: value.subject_type, subject_id: value.subject_id,
		...(value.workspace_id === undefined ? {} : { workspace_id: value.workspace_id }),
		...(value.backup_id === undefined ? {} : { backup_id: value.backup_id }), state: value.state,
		reason_code: value.reason_code, opened_at: value.opened_at,
		...(value.resolved_at === undefined ? {} : { resolved_at: value.resolved_at }),
		...(value.resolution_type === undefined ? {} : { resolution_type: value.resolution_type }),
		...(value.resolution_id === undefined ? {} : { resolution_id: value.resolution_id }) }
}

function parseRuntimeVerification(value: unknown, status: number): RuntimeVerification {
	if (!hasExactKeys(value, ['id', 'attention_case_id', 'state', 'production_digest', 'worker_count',
		'request_id', 'created_at', 'finished_at'], ['master_pid', 'http_status', 'last_error_code']) ||
		!isOpaqueID(value.id) || !isOpaqueID(value.attention_case_id) ||
		!isOneOf(value.state, ['succeeded', 'failed']) || !isDigest(value.production_digest) ||
		(value.master_pid !== undefined && !isIntegerInRange(value.master_pid, 1)) ||
		!isIntegerInRange(value.worker_count, 0) ||
		(value.http_status !== undefined && !isIntegerInRange(value.http_status, 100, 599)) ||
		(value.last_error_code !== undefined && !isBoundedCode(value.last_error_code)) ||
		!isRequestID(value.request_id) || !isRFC3339(value.created_at) || !isRFC3339(value.finished_at)) {
		throw malformedResponse(status)
	}
	return { id: value.id, attention_case_id: value.attention_case_id, state: value.state,
		production_digest: value.production_digest,
		...(value.master_pid === undefined ? {} : { master_pid: value.master_pid as number }),
		worker_count: value.worker_count as number,
		...(value.http_status === undefined ? {} : { http_status: value.http_status as number }),
		...(value.last_error_code === undefined ? {} : { last_error_code: value.last_error_code }),
		request_id: value.request_id, created_at: value.created_at, finished_at: value.finished_at }
}

function parseAuditEvent(value: unknown, status: number): AuditEvent {
	if (!hasExactKeys(value, ['id', 'occurred_at', 'actor_name', 'action', 'object_type', 'object_id',
		'result', 'request_id', 'details']) || !isIntegerInRange(value.id, 1) ||
		!isRFC3339(value.occurred_at) || !isBoundedString(value.actor_name, 0, 128) ||
		!isBoundedCode(value.action, 128, /^[a-z0-9._-]+$/) ||
		!isBoundedCode(value.object_type) || !isOpaqueID(value.object_id) ||
		!isBoundedCode(value.result) || !isRequestID(value.request_id) || !isRecord(value.details) ||
		!Object.values(value.details).every((detail) => ['string', 'number', 'boolean'].includes(typeof detail))) {
		throw malformedResponse(status)
	}
	return { id: value.id as number, occurred_at: value.occurred_at, actor_name: value.actor_name,
		action: value.action, object_type: value.object_type, object_id: value.object_id,
		result: value.result, request_id: value.request_id,
		details: { ...value.details } as Record<string, string | number | boolean> }
}

function isBoundedCode(value: unknown, maximum = 128, pattern = /^[a-z0-9_]+$/): value is string {
	return isBoundedString(value, 1, maximum) && pattern.test(value)
}

function isRequestID(value: unknown): value is string {
	return typeof value === 'string' && /^[A-Za-z0-9._-]{1,64}$/.test(value)
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
		case 'CONFIG_BACKUP_PROTECTED':
			return ['backup_id']
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
				'attention_case_id',
				'backup_id',
        'body',
        'confirm_name',
        'confirm_path',
        'content',
				'confirm_backup_id',
				'confirmation',
        'destination_path',
        'group_id',
        'members',
        'name',
				'expected_protected',
        'path',
				'protected',
        'query',
        'source_path',
				'reason',
				'restart_id',
				'restore_id',
				'retention_id',
        'workspace_id',
        'username',
      ])
		case 'backup_id':
			return isOpaqueID(value)
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
  'CONFIG_PUBLISH_CHECK_NOT_FOUND',
  'CONFIG_RELEASE_NOT_FOUND',
  'CONFIG_WORKSPACE_CONFLICT',
  'CONFIG_WORKSPACE_STALE',
  'CONFIG_WORKSPACE_NEEDS_ATTENTION',
  'CONFIG_SNAPSHOT_CHANGED',
  'CONFIG_PRODUCTION_CHANGED',
  'CONFIG_BACKUP_INVALID',
  'NGINX_HEALTH_UNAVAILABLE',
  'CONFIG_RELEASE_NEEDS_ATTENTION',
  'AGENT_UNAVAILABLE',
  'CONFIG_OPERATION_TIMEOUT',
	'CONFIG_CANDIDATE_INVALID',
	'CONFIG_NO_CHANGES',
	'CONFIG_PUBLISH_CHECK_EXPIRED',
	'CONFIG_PUBLISH_IN_PROGRESS',
	'CONFIG_OPERATION_IN_PROGRESS',
	'CONFIG_BACKUP_PROTECTED',
	'CONFIG_RETENTION_PLAN_EXPIRED',
	'CONFIG_ATTENTION_UNRESOLVED',
	'CONFIG_BACKUP_TARGET_INVALID',
	'CONFIG_RESTORE_NEEDS_ATTENTION',
	'NGINX_RESTART_CONFIG_INVALID',
	'NGINX_RESTART_FAILED',
	'NGINX_RESTART_NEEDS_ATTENTION',
	'CONFIG_BACKUP_NOT_FOUND',
	'CONFIG_RETENTION_RUN_NOT_FOUND',
	'CONFIG_RESTORE_NOT_FOUND',
	'NGINX_RESTART_NOT_FOUND',
	'CONFIG_ATTENTION_CASE_NOT_FOUND',
	'STRUCTURED_PARSE_FAILED',
	'STRUCTURED_LIMIT_EXCEEDED',
	'STRUCTURED_PREVIEW_STALE',
	'STRUCTURED_CONTEXT_AMBIGUOUS',
	'STRUCTURED_EDIT_CONFLICT',
	'UPSTREAM_INVALID',
	'UPSTREAM_DUPLICATE',
	'UPSTREAM_REFERENCED',
	'UPSTREAM_REFERENCE_INCOMPLETE',
	'LOCATION_INVALID',
	'LOCATION_DUPLICATE',
	'PROXY_PASS_INVALID',
	'ROUTE_REQUEST_TOO_LARGE',
	'ROUTE_REQUEST_INVALID',
	'ROUTE_LAB_UNAVAILABLE',
	'ROUTE_TEST_NOT_FOUND',
	'ROUTE_WORKSPACE_CONFLICT',
	'ROUTE_CONFIRMATION_REQUIRED',
	'ROUTE_PROJECT_INCOMPLETE',
	'ROUTE_LISTENER_AMBIGUOUS',
	'ROUTE_LAB_BUSY',
	'ROUTE_CANDIDATE_INVALID',
	'ROUTE_SANDBOX_START_FAILED',
	'ROUTE_CLEANUP_FAILED',
	'ROUTE_REQUEST_TIMEOUT',
	'ROUTE_EVIDENCE_INCOMPLETE',
	'ROUTE_ALREADY_TERMINAL',
	'ROUTE_LIMIT_EXCEEDED',
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

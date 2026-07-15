/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import type {
  APIError,
  APIErrorEnvelope,
  EffectiveConfigOccurrence,
  EffectiveConfigResponse,
  LoginRequest,
  NginxBuild,
  NginxProcess,
  RecoveryStatus,
  SessionResponse,
  StartupValidation,
  SystemStatusResponse,
} from './types'

const sessionPath = '/api/v1/auth/session'
const systemStatusPath = '/api/v1/system/status'
const effectiveConfigPath = '/api/v1/nginx/effective-config'

export type APIRequestErrorKind = 'api' | 'malformed_response' | 'network'
export type APIErrorListener = (error: APIRequestError) => void

export class APIRequestError extends Error {
  readonly apiError?: APIError
  readonly kind: APIRequestErrorKind
  readonly retryAfterSeconds?: number
  readonly status?: number

  constructor(options: {
    kind: APIRequestErrorKind
    message: string
    status?: number
    apiError?: APIError
    retryAfterSeconds?: number
  }) {
    super(options.message)
    this.name = 'APIRequestError'
    this.kind = options.kind
    this.status = options.status
    this.apiError = options.apiError
    this.retryAfterSeconds = options.retryAfterSeconds
  }
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

async function readJSON(response: Response): Promise<unknown> {
  try {
    return await response.json()
  } catch {
    throw malformedResponse(response.status)
  }
}

function parseSessionResponse(value: unknown, status: number): SessionResponse {
  if (!isRecord(value) || !isRecord(value.user)) {
    throw malformedResponse(status)
  }
  const { user } = value
  if (
    !Number.isSafeInteger(user.id) ||
    typeof user.username !== 'string' ||
    typeof value.csrf_token !== 'string' ||
    !isRFC3339(value.idle_expires_at) ||
    !isRFC3339(value.absolute_expires_at)
  ) {
    throw malformedResponse(status)
  }
  return {
    user: { id: user.id as number, username: user.username },
    csrf_token: value.csrf_token,
    idle_expires_at: value.idle_expires_at,
    absolute_expires_at: value.absolute_expires_at,
  }
}

function parseSystemStatusResponse(value: unknown, status: number): SystemStatusResponse {
  if (
    !isRecord(value) ||
    !isRFC3339(value.sampled_at) ||
    !isRecord(value.components) ||
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
    !isRecord(value) ||
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
    !isRecord(value) ||
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
    !isRecord(value) ||
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
    !isRecord(value) ||
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
    !isRecord(value) ||
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
    !isRecord(value) ||
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

function parseAPIErrorEnvelope(value: unknown, status: number): APIErrorEnvelope {
  if (!isRecord(value) || !isRecord(value.error)) {
    throw malformedResponse(status)
  }
  const { error } = value
  if (
    typeof error.code !== 'string' ||
    typeof error.message !== 'string' ||
    typeof error.request_id !== 'string' ||
    (error.details !== undefined && !isRecord(error.details))
  ) {
    throw malformedResponse(status)
  }
  return {
    error: {
      code: error.code,
      message: error.message,
      request_id: error.request_id,
      ...(error.details === undefined ? {} : { details: error.details }),
    },
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

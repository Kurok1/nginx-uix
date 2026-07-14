/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import type { APIError, APIErrorEnvelope, LoginRequest, SessionResponse } from './types'

const sessionPath = '/api/v1/auth/session'

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

function isRFC3339(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value) &&
    !Number.isNaN(Date.parse(value))
  )
}

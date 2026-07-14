/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
export interface LoginRequest {
  username: string
  password: string
}

export interface SessionResponse {
  user: {
    id: number
    username: string
  }
  csrf_token: string
  idle_expires_at: string
  absolute_expires_at: string
}

export interface APIError {
  code: string
  message: string
  request_id: string
  details?: Readonly<Record<string, unknown>>
}

export interface APIErrorEnvelope {
  error: APIError
}

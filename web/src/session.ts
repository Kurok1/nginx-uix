/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { reactive } from 'vue'

import { apiClient, APIRequestError } from './api/client'
import type { LoginRequest, SessionResponse } from './api/types'

export type SessionPhase = 'unknown' | 'authenticated' | 'anonymous'

export interface SessionState {
  phase: SessionPhase
  session: SessionResponse | null
}

export interface SessionClient {
  getSession: () => Promise<SessionResponse>
  login: (input: LoginRequest) => Promise<SessionResponse>
  logout: (csrfToken: string) => Promise<void>
}

export interface SessionStore {
  readonly state: SessionState
  handleAPIError: (error: unknown) => boolean
  login: (input: LoginRequest) => Promise<void>
  logout: () => Promise<void>
  onExpired: (listener: () => void) => () => void
  restore: () => Promise<void>
}

export function createSessionStore(client: SessionClient): SessionStore {
  const state = reactive<SessionState>({ phase: 'unknown', session: null })
  const expiryListeners = new Set<() => void>()
  let restorePromise: Promise<void> | null = null

  function setAuthenticated(session: SessionResponse): void {
    state.phase = 'authenticated'
    state.session = session
  }

  function setAnonymous(): void {
    state.phase = 'anonymous'
    state.session = null
  }

  async function restore(): Promise<void> {
    if (state.phase !== 'unknown') {
      return
    }
    if (restorePromise !== null) {
      return restorePromise
    }
    restorePromise = (async () => {
      try {
        setAuthenticated(await client.getSession())
      } catch {
        setAnonymous()
      } finally {
        restorePromise = null
      }
    })()
    return restorePromise
  }

  async function login(input: LoginRequest): Promise<void> {
    setAuthenticated(await client.login(input))
  }

  async function logout(): Promise<void> {
    const csrfToken = state.session?.csrf_token
    if (csrfToken === undefined) {
      setAnonymous()
      return
    }
    await client.logout(csrfToken)
    setAnonymous()
  }

  function onExpired(listener: () => void): () => void {
    expiryListeners.add(listener)
    return () => expiryListeners.delete(listener)
  }

  function handleAPIError(error: unknown): boolean {
    if (!(error instanceof APIRequestError) || error.kind !== 'api') {
      return false
    }
    const code = error.apiError?.code
    if (code !== 'AUTH_SESSION_EXPIRED' && code !== 'unauthenticated') {
      return false
    }
    const wasAuthenticated = state.phase === 'authenticated'
    setAnonymous()
    if (wasAuthenticated) {
      for (const listener of expiryListeners) {
        listener()
      }
    }
    return true
  }

  return { state, restore, login, logout, onExpired, handleAPIError }
}

export const sessionStore = createSessionStore(apiClient)

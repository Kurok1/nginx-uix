/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { APIRequestError } from './api/client'
import type { LoginRequest, SessionResponse } from './api/types'
import { createSessionStore, type SessionClient } from './session'

const currentSession: SessionResponse = {
  user: { id: 7, username: 'operator' },
  csrf_token: 'csrf-token',
  idle_expires_at: '2026-07-14T20:00:00Z',
  absolute_expires_at: '2026-07-15T12:00:00Z',
}

function createClient(overrides: Partial<SessionClient> = {}): SessionClient {
  return {
    getSession: vi.fn<() => Promise<SessionResponse>>().mockResolvedValue(currentSession),
    login: vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockResolvedValue(currentSession),
    logout: vi.fn<(csrfToken: string) => Promise<void>>().mockResolvedValue(undefined),
    ...overrides,
  }
}

function createDeferred<T>(): {
  promise: Promise<T>
  resolve: (value: T) => void
} {
  let resolver: ((value: T) => void) | undefined
  const promise = new Promise<T>((resolve) => {
    resolver = resolve
  })
  return {
    promise,
    resolve(value) {
      if (resolver === undefined) {
        throw new Error('deferred resolver is unavailable')
      }
      resolver(value)
    },
  }
}

describe('in-memory session store', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
  })

  it('deduplicates cold-boot restoration and authenticates from the returned session', async () => {
    const deferred = createDeferred<SessionResponse>()
    const getSession = vi.fn<() => Promise<SessionResponse>>().mockReturnValue(deferred.promise)
    const store = createSessionStore(createClient({ getSession }))

    const firstRestore = store.restore()
    const secondRestore = store.restore()
    expect(getSession).toHaveBeenCalledTimes(1)

    deferred.resolve(currentSession)
    await Promise.all([firstRestore, secondRestore])

    expect(store.state).toEqual({ phase: 'authenticated', session: currentSession })
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
  })

  it('treats a 401 restore response as anonymous without persisting state', async () => {
    const getSession = vi.fn<() => Promise<SessionResponse>>().mockRejectedValue(
      new APIRequestError({
        kind: 'api',
        message: '需要登录',
        status: 401,
        apiError: { code: 'unauthenticated', message: '需要登录', request_id: 'request-1' },
      }),
    )
    const store = createSessionStore(createClient({ getSession }))

    await store.restore()

    expect(store.state).toEqual({ phase: 'anonymous', session: null })
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
  })

  it('logs out with the current CSRF token before clearing only in-memory state', async () => {
    const logout = vi.fn<(csrfToken: string) => Promise<void>>().mockResolvedValue(undefined)
    const store = createSessionStore(createClient({ logout }))
    await store.restore()

    await store.logout()

    expect(logout).toHaveBeenCalledWith('csrf-token')
    expect(store.state).toEqual({ phase: 'anonymous', session: null })
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
  })

  it('recognizes AUTH_SESSION_EXPIRED and clears only the reactive session', async () => {
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
    const store = createSessionStore(createClient())
    await store.restore()

    const handled = store.handleAPIError(
      new APIRequestError({
        kind: 'api',
        message: 'session expired',
        status: 401,
        apiError: { code: 'AUTH_SESSION_EXPIRED', message: 'session expired', request_id: 'request-2' },
      }),
    )

    expect(handled).toBe(true)
    expect(store.state).toEqual({ phase: 'anonymous', session: null })
    expect(storageWrite).not.toHaveBeenCalled()
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
  })
})

/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { createMemoryHistory } from 'vue-router'

import { APIClient, APIRequestError } from './api/client'
import type { LoginRequest, SessionResponse } from './api/types'
import { createAppRouter, installSessionExpiryRedirect } from './router'
import { createSessionStore, type SessionClient } from './session'
import EffectiveConfigView from './views/EffectiveConfigView.vue'

const currentSession: SessionResponse = {
  user: { id: 7, username: 'operator' },
  csrf_token: 'csrf-token',
  idle_expires_at: '2026-07-14T20:00:00Z',
  absolute_expires_at: '2026-07-15T12:00:00Z',
}

function createClient(getSession: SessionClient['getSession']): SessionClient {
  return {
    getSession,
    login: vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockResolvedValue(currentSession),
    logout: vi.fn<(csrfToken: string) => Promise<void>>().mockResolvedValue(undefined),
  }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('application router', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
  })

  it('defines only Login, Dashboard and effective-configuration page routes', () => {
    const store = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const router = createAppRouter(store, createMemoryHistory())

    expect(
      router
        .getRoutes()
        .map((route) => ({ name: route.name, path: route.path }))
        .sort((left, right) => left.path.localeCompare(right.path)),
    ).toEqual([
      { name: 'dashboard', path: '/' },
      { name: 'configuration', path: '/configuration' },
      { name: 'login', path: '/login' },
    ])
  })

  it('routes the authenticated configuration URL to the effective-config view', () => {
    const store = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const router = createAppRouter(store, createMemoryHistory())
    const configurationRoute = router
      .getRoutes()
      .find((route) => route.name === 'configuration')

    expect(configurationRoute?.components?.default).toBe(EffectiveConfigView)
  })

  it('restores a valid session on cold boot and enters Dashboard', async () => {
    const getSession = vi.fn<() => Promise<SessionResponse>>().mockResolvedValue(currentSession)
    const store = createSessionStore(createClient(getSession))
    const router = createAppRouter(store, createMemoryHistory())

    await router.push('/')

    expect(getSession).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.name).toBe('dashboard')
    expect(store.state.phase).toBe('authenticated')
  })

  it('redirects a cold-boot 401 to Login', async () => {
    const getSession = vi.fn<() => Promise<SessionResponse>>().mockRejectedValue(
      new APIRequestError({
        kind: 'api',
        message: '需要登录',
        status: 401,
        apiError: { code: 'unauthenticated', message: '需要登录', request_id: 'request-3' },
      }),
    )
    const store = createSessionStore(createClient(getSession))
    const router = createAppRouter(store, createMemoryHistory())

    await router.push('/')

    expect(router.currentRoute.value.name).toBe('login')
    expect(store.state.phase).toBe('anonymous')
  })

  it('clears memory and redirects to Login when a later API response expires the session', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(currentSession))
      .mockResolvedValueOnce(
        jsonResponse(
          {
            error: {
              code: 'AUTH_SESSION_EXPIRED',
              message: 'session expired',
              request_id: 'request-4',
            },
          },
          401,
        ),
      )
    const client = new APIClient(fetchMock)
    const store = createSessionStore(client)
    const router = createAppRouter(store, createMemoryHistory())
    const uninstall = installSessionExpiryRedirect(client, store, router)

    await router.push('/')
    await expect(client.getSession()).rejects.toMatchObject({ kind: 'api' })
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('login'))

    expect(store.state).toEqual({ phase: 'anonymous', session: null })
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
    uninstall()
  })
})

/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { reactive } from 'vue'
import { createMemoryHistory } from 'vue-router'

import { APIClient, APIRequestError } from './api/client'
import type { LoginRequest, SessionResponse } from './api/types'
import {
  createAppRouter,
  installSessionExpiryRedirect,
  installWorkspaceLeaveGuard,
} from './router'
import { createSessionStore, type SessionClient } from './session'
import type { WorkspaceStateModel, WorkspaceStore } from './workspace'
import appShellSource from './components/AppShell.vue?raw'
import globalNavSource from './components/GlobalNav.vue?raw'
import subNavSource from './components/SubNav.vue?raw'
import ConfigWorkspaceView from './views/ConfigWorkspaceView.vue'
import EffectiveConfigView from './views/EffectiveConfigView.vue'

const currentSession: SessionResponse = {
  user: { id: 7, username: 'operator', created_at: '2026-07-14T11:00:00Z' },
  csrf_token: 'csrf-token',
  created_at: '2026-07-14T12:00:00Z',
  last_seen_at: '2026-07-14T12:30:00Z',
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

function workspaceStore(dirty = false): WorkspaceStore {
  const state = reactive<WorkspaceStateModel>({
    phase: 'ready',
    workspaces: [],
    active: null,
    tree: [],
    dependencies: [],
    documents: dirty
      ? [
          {
            path: 'nginx.conf',
            serverContent: 'events {}\n',
            content: 'events { worker_connections 256; }\n',
            lineEnding: 'lf',
            contentDigest: 'a'.repeat(64),
            dirty: true,
            requiresRefresh: false,
          },
        ]
      : [],
    selectedPath: dirty ? 'nginx.conf' : null,
    activeTask: 'editor',
    search: null,
    diff: null,
    groups: null,
    pendingAction: null,
    banner: null,
  })
  return {
    state,
    hasUnsavedChanges: vi.fn(() => state.documents.some((document) => document.dirty)),
    markSessionExpired: vi.fn(),
  } as unknown as WorkspaceStore
}

describe('application router', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
  })

  it('defines Login, Dashboard, effective configuration and configuration workspaces', () => {
    const store = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const router = createAppRouter(store, createMemoryHistory())

    expect(
      router
        .getRoutes()
        .map((route) => ({ name: route.name, path: route.path }))
        .sort((left, right) => left.path.localeCompare(right.path)),
    ).toEqual([
      { name: 'dashboard', path: '/' },
      { name: 'config-workspaces', path: '/config/workspaces/:workspaceId?' },
      { name: 'configuration', path: '/configuration' },
      { name: 'login', path: '/login' },
    ])
  })

  it('routes the authenticated workspace URL to the configuration-workspace view', () => {
    const store = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const router = createAppRouter(store, createMemoryHistory())
    const workspaceRoute = router
      .getRoutes()
      .find((route) => route.name === 'config-workspaces')

    expect(workspaceRoute?.components?.default).toBe(ConfigWorkspaceView)
    expect(workspaceRoute?.meta.requiresAuth).toBe(true)
  })

  it('adds Workspaces to both navigation levels and bounds page overflow', () => {
    expect(globalNavSource.match(/to="\/config\/workspaces"/g)).toHaveLength(2)
    expect(globalNavSource).toContain('Workspaces')
    expect(globalNavSource).toContain('to="/configuration"')
    expect(subNavSource).toContain('to="/config/workspaces"')
    expect(subNavSource).toContain('Workspaces')
    expect(subNavSource).toContain('to="/configuration"')
    expect(appShellSource).toMatch(/\.app-shell\s*\{[\s\S]*overflow-x: hidden/)
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
    expect(router.currentRoute.value.query.redirect).toBe('/')
    expect(store.state.phase).toBe('anonymous')
  })

  it('confirms only when leaving a dirty workspace and owns beforeunload while dirty', async () => {
    const sessions = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const workspaces = workspaceStore(true)
    const confirmLeave = vi.fn(() => false)
    const addEventListener = vi.spyOn(window, 'addEventListener')
    const removeEventListener = vi.spyOn(window, 'removeEventListener')
    const router = createAppRouter(sessions, createMemoryHistory())
    const uninstall = installWorkspaceLeaveGuard(router, workspaces, confirmLeave)

    await router.push('/config/workspaces/one')
    await router.push('/config/workspaces/two')
    expect(confirmLeave).not.toHaveBeenCalled()
    await router.push('/')
    expect(router.currentRoute.value.fullPath).toBe('/config/workspaces/two')
    expect(confirmLeave).toHaveBeenCalledOnce()
    expect(addEventListener).toHaveBeenCalledWith('beforeunload', expect.any(Function))

    workspaces.state.documents = []
    await Promise.resolve()
    await router.push('/')
    expect(router.currentRoute.value.name).toBe('dashboard')
    expect(confirmLeave).toHaveBeenCalledOnce()
    uninstall()
    expect(removeEventListener).toHaveBeenCalledWith('beforeunload', expect.any(Function))
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
    const workspaces = workspaceStore(true)
    const uninstall = installSessionExpiryRedirect(client, store, router, workspaces)

    await router.push('/config/workspaces/workspace-id')
    await expect(client.getSession()).rejects.toMatchObject({ kind: 'api' })
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('login'))

    expect(store.state).toEqual({ phase: 'anonymous', session: null })
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
    expect(workspaces.markSessionExpired).toHaveBeenCalledOnce()
    expect(router.currentRoute.value.query.redirect).toBe('/config/workspaces/workspace-id')
    uninstall()
  })
})

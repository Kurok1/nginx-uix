/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { reactive } from 'vue'
import { createMemoryHistory } from 'vue-router'

import { APIClient, APIRequestError } from './api/client'
import type { LoginRequest, SessionResponse } from './api/types'
import { appI18n, createAppI18n } from './i18n'
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
import CertificatesView from './views/CertificatesView.vue'
import ConfigWorkspaceView from './views/ConfigWorkspaceView.vue'
import EffectiveConfigView from './views/EffectiveConfigView.vue'
import OperationsView from './views/OperationsView.vue'
import RouteLabView from './views/RouteLabView.vue'
import StructuredConfigView from './views/StructuredConfigView.vue'

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
    appI18n.global.locale.value = 'en-US'
    localStorage.clear()
    sessionStorage.clear()
  })

  it('defines Login, Dashboard, effective configuration, workspaces and recovery history', () => {
    const store = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const router = createAppRouter(store, createMemoryHistory())

    expect(
      router
        .getRoutes()
        .map((route) => ({ name: route.name, path: route.path }))
        .sort((left, right) => left.path.localeCompare(right.path)),
    ).toEqual([
      { name: 'dashboard', path: '/' },
      { name: 'certificates', path: '/certificates/:certificateId?' },
      { name: 'config-operations', path: '/config/operations' },
      { name: 'route-lab', path: '/config/route-lab' },
      { name: 'config-workspaces', path: '/config/workspaces/:workspaceId?' },
      { name: 'structured-servers', path: '/config/workspaces/:workspaceId/servers' },
      { name: 'structured-upstreams', path: '/config/workspaces/:workspaceId/upstreams' },
      { name: 'configuration', path: '/configuration' },
      { name: 'login', path: '/login' },
    ])
  })

  it('routes the authenticated recovery URL to the operations evidence view', () => {
    const store = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const router = createAppRouter(store, createMemoryHistory())
    const operationsRoute = router
      .getRoutes()
      .find((route) => route.name === 'config-operations')

    expect(operationsRoute?.components?.default).toBe(OperationsView)
    expect(operationsRoute?.meta.requiresAuth).toBe(true)
  })

  it('routes the authenticated Route Lab URL to the isolated verification view', () => {
    const store = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const router = createAppRouter(store, createMemoryHistory())
    const routeLabRoute = router
      .getRoutes()
      .find((route) => route.name === 'route-lab')

    expect(routeLabRoute?.components?.default).toBe(RouteLabView)
    expect(routeLabRoute?.meta.requiresAuth).toBe(true)
  })

  it('routes the authenticated certificate URL to the certificate automation view', () => {
    const store = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const router = createAppRouter(store, createMemoryHistory())
    const certificateRoute = router
      .getRoutes()
      .find((route) => route.name === 'certificates')

    expect(certificateRoute?.components?.default).toBe(CertificatesView)
    expect(certificateRoute?.meta.requiresAuth).toBe(true)
    const props = certificateRoute?.props.default
    if (typeof props !== 'function') {
      throw new Error('certificate route props must be a function')
    }
    expect(props({ params: { certificateId: 'a'.repeat(32) } } as never)).toEqual({
      certificateId: 'a'.repeat(32),
    })
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

  it('routes both structured workspace surfaces to the mode-bound workbench', () => {
    const store = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const router = createAppRouter(store, createMemoryHistory())
    const upstreamRoute = router
      .getRoutes()
      .find((route) => route.name === 'structured-upstreams')
    const serverRoute = router
      .getRoutes()
      .find((route) => route.name === 'structured-servers')

    expect(upstreamRoute?.components?.default).toBe(StructuredConfigView)
    expect(serverRoute?.components?.default).toBe(StructuredConfigView)
    expect(upstreamRoute?.meta.requiresAuth).toBe(true)
    expect(serverRoute?.meta.requiresAuth).toBe(true)
  })

  it('adds Workspaces, Route Lab, Certificates and Recovery & History to both navigation levels and bounds overflow', () => {
    expect(globalNavSource.match(/<LanguageSelector/g)).toHaveLength(2)
    expect(globalNavSource.match(/to="\/config\/workspaces"/g)).toHaveLength(2)
    expect(globalNavSource).toContain("t('navigation.workspaces')")
    expect(globalNavSource).toContain('to="/configuration"')
    expect(globalNavSource.match(/to="\/config\/operations"/g)).toHaveLength(2)
    expect(globalNavSource).toContain("t('navigation.recoveryHistory')")
    expect(globalNavSource.match(/to="\/config\/route-lab"/g)).toHaveLength(2)
    expect(globalNavSource).toContain("t('navigation.routeLab')")
    expect(globalNavSource.match(/to="\/certificates"/g)).toHaveLength(2)
    expect(globalNavSource).toContain("t('navigation.certificates')")
    expect(subNavSource).toContain('to="/config/workspaces"')
    expect(subNavSource).toContain("t('navigation.workspaces')")
    expect(subNavSource).toContain('to="/configuration"')
    expect(subNavSource).toContain('to="/config/operations"')
    expect(subNavSource).toContain("t('navigation.recoveryHistory')")
    expect(subNavSource).toContain('to="/config/route-lab"')
    expect(subNavSource).toContain("t('navigation.routeLab')")
    expect(subNavSource).toContain('to="/certificates"')
    expect(subNavSource).toContain("t('navigation.certificates')")
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
    expect(router.currentRoute.value.query.redirect).toBe('/?lang=en-US')
    expect(router.currentRoute.value.query.lang).toBe('en-US')
    expect(store.state.phase).toBe('anonymous')
  })

  it('keeps the selected URL locale through the authentication redirect', async () => {
    const getSession = vi.fn<() => Promise<SessionResponse>>().mockRejectedValue(
      new APIRequestError({
        kind: 'api',
        message: 'authentication required',
        status: 401,
        apiError: {
          code: 'unauthenticated',
          message: 'authentication required',
          request_id: 'request-locale',
        },
      }),
    )
    const store = createSessionStore(createClient(getSession))
    const i18n = createAppI18n('en-US')
    const router = createAppRouter(store, createMemoryHistory(), i18n)

    await router.push('/configuration?lang=zh-CN')

    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.lang).toBe('zh-CN')
    expect(router.currentRoute.value.query.redirect).toBe('/configuration?lang=zh-CN')
    expect(i18n.global.locale.value).toBe('zh-CN')
    expect(localStorage.getItem('nginx-uix.locale')).toBe('zh-CN')
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
    expect(router.currentRoute.value.fullPath).toBe('/config/workspaces/two?lang=en-US')
    expect(confirmLeave).toHaveBeenCalledOnce()
    expect(confirmLeave).toHaveBeenCalledWith(
      'Unsaved workspace text will remain only in this browser session. Leave this page?',
    )
    expect(addEventListener).toHaveBeenCalledWith('beforeunload', expect.any(Function))

    workspaces.state.documents = []
    await Promise.resolve()
    await router.push('/')
    expect(router.currentRoute.value.name).toBe('dashboard')
    expect(confirmLeave).toHaveBeenCalledOnce()
    uninstall()
    expect(removeEventListener).toHaveBeenCalledWith('beforeunload', expect.any(Function))
  })

  it('uses the active locale for the dirty-workspace leave confirmation', async () => {
    const sessions = createSessionStore(createClient(vi.fn().mockResolvedValue(currentSession)))
    const workspaces = workspaceStore(true)
    const confirmLeave = vi.fn(() => false)
    const i18n = createAppI18n('zh-CN')
    const router = createAppRouter(sessions, createMemoryHistory(), i18n)
    const uninstall = installWorkspaceLeaveGuard(router, workspaces, confirmLeave, i18n)

    await router.push('/config/workspaces/one?lang=zh-CN')
    await router.push('/?lang=zh-CN')

    expect(confirmLeave).toHaveBeenCalledWith('未保存的工作区文本将仅保留在当前浏览器会话中。是否离开此页面？')
    uninstall()
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
    expect(localStorage).toHaveLength(1)
    expect(localStorage.getItem('nginx-uix.locale')).toBe('en-US')
    expect(sessionStorage.length).toBe(0)
    expect(workspaces.markSessionExpired).toHaveBeenCalledOnce()
    expect(router.currentRoute.value.query.redirect).toBe(
      '/config/workspaces/workspace-id?lang=en-US',
    )
    uninstall()
  })
})

/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import type { LoginRequest, SessionResponse } from '../api/types'
import LoginForm from '../components/LoginForm.vue'
import UnsavedRecovery from '../components/UnsavedRecovery.vue'
import { createSessionStore, type SessionClient } from '../session'
import type { WorkspaceStateModel, WorkspaceStore } from '../workspace'
import LoginView from './LoginView.vue'
import loginViewSource from './LoginView.vue?raw'

const currentSession: SessionResponse = {
  user: { id: 7, username: 'operator', created_at: '2026-07-14T11:00:00Z' },
  csrf_token: 'csrf-token',
  created_at: '2026-07-14T12:00:00Z',
  last_seen_at: '2026-07-14T12:30:00Z',
  idle_expires_at: '2026-07-14T20:00:00Z',
  absolute_expires_at: '2026-07-15T12:00:00Z',
}

function createClient(login: SessionClient['login']): SessionClient {
  return {
    getSession: vi.fn<() => Promise<SessionResponse>>().mockResolvedValue(currentSession),
    login,
    logout: vi.fn<(csrfToken: string) => Promise<void>>().mockResolvedValue(undefined),
  }
}

function dirtyWorkspace(): WorkspaceStore {
  const state = reactive<WorkspaceStateModel>({
    phase: 'ready',
    workspaces: [],
    active: {
      id: 'workspace-id',
      name: 'Review changes',
      state: 'ready',
      production_digest: 'a'.repeat(64),
      base_digest: 'a'.repeat(64),
      draft_etag: `"draft-v1:${'a'.repeat(64)}"`,
      entry_count: 1,
      managed_bytes: 32,
      workspace_bytes: 128,
      created_by: 7,
      created_at: '2026-07-17T08:00:00Z',
      updated_at: '2026-07-17T08:01:00Z',
    },
    tree: [],
    dependencies: [],
    documents: [
      {
        path: 'nginx.conf',
        serverContent: 'events {}\n',
        content: 'events { worker_connections 256; }\n',
        lineEnding: 'lf',
        contentDigest: 'a'.repeat(64),
        dirty: true,
        requiresRefresh: true,
      },
    ],
    selectedPath: 'nginx.conf',
    activeTask: 'editor',
    search: null,
    diff: null,
    groups: null,
    pendingAction: null,
    banner: { kind: 'session_expired', message: 'Session expired.' },
  })
  return {
    state,
    markSessionRestored: vi.fn(async () => undefined),
    copyLocalContent: vi.fn(async () => true),
  } as unknown as WorkspaceStore
}

async function loginRouter() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/login', name: 'login', component: { template: '<div />' } },
      {
        path: '/config/workspaces/:workspaceId?',
        name: 'config-workspaces',
        component: { template: '<div />' },
      },
    ],
  })
  await router.push('/login?redirect=/config/workspaces/workspace-id')
  await router.isReady()
  return router
}

describe('LoginView', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('orchestrates one labelled login page around the typed LoginForm', async () => {
    const login = vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockResolvedValue(
      currentSession,
    )
    const store = createSessionStore(createClient(login))
    const wrapper = mount(LoginView, { props: { store } })

    const main = wrapper.get('main.login-view')
    expect(main.attributes('aria-labelledby')).toBe('login-title')
    expect(main.get('h1#login-title').text()).toBe('登录 Nginx UIX')
    expect(wrapper.getComponent(LoginForm).props('store')?.state).toStrictEqual(store.state)
    expect(wrapper.find('a').exists()).toBe(false)

    await wrapper.get('input[name="username"]').setValue('operator')
    await wrapper.get('input[name="password"]').setValue('secret')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(login).toHaveBeenCalledWith({ username: 'operator', password: 'secret' })
  })

  it('uses shared page tokens without storage or direct network access', () => {
    expect(loginViewSource).toContain('background: var(--color-canvas-parchment)')
    expect(loginViewSource).toContain('border-radius: var(--rounded-lg)')
    expect(loginViewSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(loginViewSource).not.toMatch(/var\([^)]*,/)
    expect(loginViewSource).not.toMatch(
      /\b(?:fetch|localStorage|sessionStorage|indexedDB|caches)\b/,
    )
  })

  it('shows dirty-memory recovery and refreshes metadata before routing back after login', async () => {
    const login = vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockResolvedValue(
      currentSession,
    )
    const store = createSessionStore(createClient(login))
    const workspace = dirtyWorkspace()
    const router = await loginRouter()
    const wrapper = mount(LoginView, {
      props: { store, workspace },
      global: { plugins: [router] },
    })

    const recovery = wrapper.getComponent(UnsavedRecovery)
    expect(recovery.props('paths')).toEqual(['nginx.conf'])
    await recovery.get('button[aria-label="Copy local content for nginx.conf"]').trigger('click')
    expect(workspace.copyLocalContent).toHaveBeenCalledWith('nginx.conf')

    await wrapper.get('input[name="username"]').setValue('operator')
    await wrapper.get('input[name="password"]').setValue('secret')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(workspace.markSessionRestored).toHaveBeenCalledOnce()
    expect(router.currentRoute.value.fullPath).toBe('/config/workspaces/workspace-id')
    expect(workspace.state.documents[0]?.content).toBe('events { worker_connections 256; }\n')
  })
})

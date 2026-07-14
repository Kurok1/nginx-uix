/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { flushPromises, mount } from '@vue/test-utils'

import type { LoginRequest, SessionResponse } from '../api/types'
import LoginForm from '../components/LoginForm.vue'
import { createSessionStore, type SessionClient } from '../session'
import LoginView from './LoginView.vue'
import loginViewSource from './LoginView.vue?raw'

const currentSession: SessionResponse = {
  user: { id: 7, username: 'operator' },
  csrf_token: 'csrf-token',
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
    expect(wrapper.getComponent(LoginForm).props('store')).toStrictEqual(store)
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
})

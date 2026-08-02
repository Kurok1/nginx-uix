/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'

import { APIRequestError } from '../api/client'
import type { LoginRequest, SessionResponse } from '../api/types'
import { appI18n } from '../i18n'
import { createSessionStore, type SessionClient, type SessionStore } from '../session'
import formFieldSource from './FormField.vue?raw'
import LoginForm from './LoginForm.vue'
import loginFormSource from './LoginForm.vue?raw'

const currentSession: SessionResponse = {
  user: { id: 7, username: 'operator', created_at: '2026-07-14T11:00:00Z' },
  csrf_token: 'csrf-token',
  created_at: '2026-07-14T12:00:00Z',
  last_seen_at: '2026-07-14T12:30:00Z',
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

function createTestRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/login', name: 'login', component: { template: '<div />' } },
    ],
  })
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

async function mountLoginForm(store: SessionStore, router = createTestRouter()) {
  await router.push('/login')
  await router.isReady()
  return {
    router,
    wrapper: mount(LoginForm, {
      props: { store },
      global: { plugins: [router] },
    }),
  }
}

describe('LoginForm', () => {
  beforeEach(() => {
    appI18n.global.locale.value = 'zh-CN'
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('renders persistent labels and the correct credential autocomplete hints', () => {
    const wrapper = mount(LoginForm)

    const username = wrapper.get('input[name="username"]')
    const password = wrapper.get('input[name="password"]')

    expect(wrapper.get('label[for="login-username"]').text()).toBe('用户名')
    expect(username.attributes('autocomplete')).toBe('username')
    expect(wrapper.get('label[for="login-password"]').text()).toBe('密码')
    expect(password.attributes('autocomplete')).toBe('current-password')
    expect(wrapper.find('a').exists()).toBe(false)
  })

  it('renders the complete form and safe credential error in English', async () => {
    appI18n.global.locale.value = 'en-US'
    const login = vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockRejectedValue(
      new APIRequestError({
        kind: 'api',
        message: 'private authentication detail',
        status: 401,
        apiError: {
          code: 'invalid_credentials',
          message: 'private authentication detail',
          request_id: 'request-english-login',
        },
      }),
    )
    const { wrapper } = await mountLoginForm(createSessionStore(createClient({ login })))

    expect(wrapper.get('label[for="login-username"]').text()).toBe('Username')
    expect(wrapper.get('label[for="login-password"]').text()).toBe('Password')
    expect(wrapper.get('button[type="submit"]').text()).toBe('Sign in')

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.get('#login-error').text()).toBe('The username or password is incorrect.')
    expect(wrapper.text()).not.toContain('private authentication detail')
  })

  it('submits through the native form without disabling the focused fields', async () => {
    const deferred = createDeferred<SessionResponse>()
    const login = vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockReturnValue(deferred.promise)
    const store = createSessionStore(createClient({ login }))
    const { wrapper } = await mountLoginForm(store)

    await wrapper.get('input[name="username"]').setValue('operator')
    await wrapper.get('input[name="password"]').setValue('secret')
    await wrapper.get('form').trigger('submit')

    expect(login).toHaveBeenCalledWith({ username: 'operator', password: 'secret' })
    expect(wrapper.get('form').attributes('aria-busy')).toBe('true')
    expect(wrapper.get('input[name="username"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('input[name="password"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('input[name="username"]').attributes('readonly')).toBeDefined()
    expect(wrapper.get('input[name="password"]').attributes('readonly')).toBeDefined()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('button[type="submit"]').text()).toBe('正在登录…')

    deferred.resolve(currentSession)
    await flushPromises()

    expect(wrapper.get('form').attributes('aria-busy')).toBe('false')
  })

  it('keeps native keyboard submission available from the password field', async () => {
    const login = vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockResolvedValue(
      currentSession,
    )
    const store = createSessionStore(createClient({ login }))
    const { wrapper } = await mountLoginForm(store)

    await wrapper.get('input[name="username"]').setValue('operator')
    const password = wrapper.get('input[name="password"]')
    await password.setValue('secret')

    const keyboardEvent = new KeyboardEvent('keydown', {
      bubbles: true,
      cancelable: true,
      key: 'Enter',
    })
    expect(password.element.dispatchEvent(keyboardEvent)).toBe(true)
    wrapper.get('form').element.requestSubmit()
    await flushPromises()

    expect(login).toHaveBeenCalledWith({ username: 'operator', password: 'secret' })
  })

  it('keeps the submit target at least 44 pixels tall', () => {
    expect(loginFormSource).toMatch(
      /\.login-form button\[type='submit'\][\s\S]*min-height: var\(--component-control-min-size\)/,
    )
  })

  it('replaces Login with Dashboard after authentication succeeds', async () => {
    const store = createSessionStore(createClient())
    const { router, wrapper } = await mountLoginForm(store)

    await wrapper.get('input[name="username"]').setValue('operator')
    await wrapper.get('input[name="password"]').setValue('secret')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(router.currentRoute.value.name).toBe('dashboard')
  })

  it('shows one generic, described invalid-credentials error without moving focus', async () => {
    const login = vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockRejectedValue(
      new APIRequestError({
        kind: 'api',
        message: 'operator account does not exist',
        status: 401,
        apiError: {
          code: 'invalid_credentials',
          message: 'operator account does not exist',
          request_id: 'request-invalid',
        },
      }),
    )
    const store = createSessionStore(createClient({ login }))
    const focus = vi.spyOn(HTMLElement.prototype, 'focus')
    const { wrapper } = await mountLoginForm(store)

    await wrapper.get('input[name="username"]').setValue('operator')
    await wrapper.get('input[name="password"]').setValue('wrong')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const error = wrapper.get('#login-error')
    expect(error.text()).toBe('用户名或密码不正确。')
    expect(error.attributes('aria-live')).toBe('polite')
    expect(error.attributes('aria-atomic')).toBe('true')
    expect(error.get('svg[aria-hidden="true"]').attributes('focusable')).toBe('false')
    expect(wrapper.text()).not.toContain('operator account does not exist')
    expect(wrapper.get('input[name="username"]').attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('input[name="password"]').attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('input[name="password"]').attributes('aria-describedby')).toContain(
      'login-error',
    )
    expect(focus).not.toHaveBeenCalled()
  })

  it('disables retries for the Retry-After countdown without repeatedly announcing timer ticks', async () => {
    vi.useFakeTimers({ toFake: ['setInterval'] })
    const login = vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockRejectedValue(
      new APIRequestError({
        kind: 'api',
        message: 'internal throttle detail',
        status: 429,
        apiError: {
          code: 'rate_limited',
          message: 'internal throttle detail',
          request_id: 'request-limited',
        },
        retryAfterSeconds: 3,
      }),
    )
    const store = createSessionStore(createClient({ login }))
    const { wrapper } = await mountLoginForm(store)

    await wrapper.get('input[name="username"]').setValue('operator')
    await wrapper.get('input[name="password"]').setValue('wrong')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.get('#login-error').text()).toBe('登录尝试过于频繁，请稍后重试。')
    expect(wrapper.get('#login-retry-status').text()).toBe('3 秒后可重试。')
    expect(wrapper.get('#login-retry-status').attributes('aria-live')).toBe('off')
    expect(wrapper.get('input[name="password"]').attributes('aria-describedby')).toBe(
      'login-error login-retry-status',
    )
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('button[type="submit"]').text()).toBe('3 秒后重试')

    await wrapper.get('form').trigger('submit')
    expect(login).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('#login-retry-status').text()).toBe('2 秒后可重试。')

    await vi.advanceTimersByTimeAsync(2000)
    expect(wrapper.find('#login-error').exists()).toBe(false)
    expect(wrapper.find('#login-retry-status').exists()).toBe(false)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('button[type="submit"]').text()).toBe('登录')
  })

  it('stops the Retry-After timer when the form unmounts', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const clearInterval = vi.spyOn(globalThis, 'clearInterval')
    const login = vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockRejectedValue(
      new APIRequestError({
        kind: 'api',
        message: 'internal throttle detail',
        status: 429,
        apiError: {
          code: 'rate_limited',
          message: 'internal throttle detail',
          request_id: 'request-limited',
        },
        retryAfterSeconds: 30,
      }),
    )
    const store = createSessionStore(createClient({ login }))
    const { wrapper } = await mountLoginForm(store)

    await wrapper.get('form').trigger('submit')
    await flushPromises()
    wrapper.unmount()

    expect(clearInterval).toHaveBeenCalledTimes(1)
  })

  it('shows a generic backend-unavailable message without leaking network details', async () => {
    const login = vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockRejectedValue(
      new APIRequestError({
        kind: 'network',
        message: 'connect ECONNREFUSED 127.0.0.1:9000',
      }),
    )
    const store = createSessionStore(createClient({ login }))
    const { wrapper } = await mountLoginForm(store)

    await wrapper.get('input[name="username"]').setValue('operator')
    await wrapper.get('input[name="password"]').setValue('secret')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const error = wrapper.get('#login-error')
    expect(error.text()).toBe('登录服务暂时不可用，请稍后重试。')
    expect(error.attributes('aria-live')).toBe('polite')
    expect(error.attributes('aria-atomic')).toBe('true')
    expect(wrapper.text()).not.toContain('ECONNREFUSED')
    expect(wrapper.get('input[name="password"]').attributes('aria-describedby')).toBe(
      'login-error',
    )
    expect(wrapper.get('input[name="password"]').attributes('aria-invalid')).toBeUndefined()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  })

  it('maps form controls and feedback to shared design tokens', () => {
    expect(formFieldSource).toContain('min-height: var(--component-control-min-size)')
    expect(formFieldSource).toContain('border-color: var(--color-ink-muted-48)')
    expect(formFieldSource).toContain('border-color: var(--color-status-error-foreground)')
    expect(loginFormSource).toContain('background: var(--color-primary)')
    expect(loginFormSource).toContain('color: var(--color-status-error-foreground)')
    expect(`${formFieldSource}\n${loginFormSource}`).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(`${formFieldSource}\n${loginFormSource}`).not.toMatch(/var\([^)]*,/)
  })
})

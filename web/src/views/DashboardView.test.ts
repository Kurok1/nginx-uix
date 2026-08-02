/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import { APIClient, APIRequestError } from '../api/client'
import type {
  LoginRequest,
  SessionResponse,
  SystemComponents,
  SystemStatusResponse,
} from '../api/types'
import { appI18n } from '../i18n'
import ComponentHealth from '../components/ComponentHealth.vue'
import componentHealthSource from '../components/ComponentHealth.vue?raw'
import processMetricsSource from '../components/ProcessMetrics.vue?raw'
import runtimeStatusSource from '../components/RuntimeStatus.vue?raw'
import validationResultSource from '../components/ValidationResult.vue?raw'
import { installSessionExpiryRedirect } from '../router'
import { createSessionStore, type SessionClient } from '../session'
import DashboardView from './DashboardView.vue'
import dashboardSource from './DashboardView.vue?raw'

const currentSession: SessionResponse = {
  user: { id: 7, username: 'operator', created_at: '2026-07-15T11:00:00Z' },
  csrf_token: 'csrf-token',
  created_at: '2026-07-15T12:00:00Z',
  last_seen_at: '2026-07-15T12:30:00Z',
  idle_expires_at: '2026-07-15T20:00:00Z',
  absolute_expires_at: '2026-07-16T12:00:00Z',
}

const healthyStatus: SystemStatusResponse = {
  sampled_at: '2026-07-15T08:30:00Z',
  components: {
    ui: 'healthy',
    agent: 'healthy',
    nginx: 'running',
  },
  master: {
    pid: 42,
    role: 'master',
    started_at: '2026-07-15T08:00:00Z',
  },
  workers: [
    {
      pid: 43,
      role: 'worker',
      started_at: '2026-07-15T08:00:01Z',
    },
    {
      pid: 44,
      role: 'worker',
      started_at: '2026-07-15T08:00:01Z',
    },
  ],
  build: {
    version: '1.30.3',
    configure_arguments: ['--with-http_ssl_module', '--with-http_v2_module'],
  },
  startup_validation: {
    valid: true,
    checked_at: '2026-07-15T07:59:58Z',
    exit_code: 0,
    diagnostic: 'nginx: configuration file /etc/nginx/nginx.conf test is successful',
  },
  recovery: {
    count: 1,
    last_result: 'restarting',
    permanent: false,
  },
  issues: ['NGINX_RECOVERING'],
}

function statusWithComponents(components: Partial<SystemComponents>): SystemStatusResponse {
  return {
    ...healthyStatus,
    components: { ...healthyStatus.components, ...components },
  }
}

function createStatusClient(
  getSystemStatus: (signal?: AbortSignal) => Promise<SystemStatusResponse>,
) {
  return { getSystemStatus }
}

function createDeferred<T>(): {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (reason: unknown) => void
} {
  let resolver: ((value: T) => void) | undefined
  let rejecter: ((reason: unknown) => void) | undefined
  const promise = new Promise<T>((resolve, reject) => {
    resolver = resolve
    rejecter = reject
  })
  return {
    promise,
    resolve(value) {
      if (resolver === undefined) {
        throw new Error('deferred resolver is unavailable')
      }
      resolver(value)
    },
    reject(reason) {
      if (rejecter === undefined) {
        throw new Error('deferred rejecter is unavailable')
      }
      rejecter(reason)
    },
  }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function componentByName(wrapper: VueWrapper, name: string): VueWrapper {
  const component = wrapper
    .findAllComponents(ComponentHealth)
    .find((candidate) => candidate.props('name') === name)
  if (component === undefined) {
    throw new Error(`missing component health card: ${name}`)
  }
  return component
}

describe('DashboardView', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
    localStorage.clear()
    sessionStorage.clear()
  })

  it('loads immediately and renders the complete verified runtime hierarchy', async () => {
    const getSystemStatus = vi
      .fn<(signal?: AbortSignal) => Promise<SystemStatusResponse>>()
      .mockResolvedValue(healthyStatus)
    const wrapper = mount(DashboardView, {
      props: { client: createStatusClient(getSystemStatus) },
    })

    expect(getSystemStatus).toHaveBeenCalledTimes(1)
    expect(getSystemStatus.mock.calls[0]?.[0]).toBeInstanceOf(AbortSignal)
    await flushPromises()

    expect(wrapper.get('h1').text()).toBe('运行状态')
    expect(componentByName(wrapper, 'UI').text()).toContain('正常')
    expect(componentByName(wrapper, 'Agent').text()).toContain('正常')
    expect(componentByName(wrapper, 'Nginx').text()).toContain('运行中')
    expect(wrapper.text()).toContain('42')
    expect(wrapper.text()).toContain('43、44')
    expect(wrapper.text()).toContain('1.30.3')
    expect(wrapper.get('.process-metrics__arguments').findAll('li').map((item) => item.text())).toEqual(
      ['--with-http_ssl_module', '--with-http_v2_module'],
    )
    expect(wrapper.text()).not.toContain('PID 路径')
    expect(wrapper.text()).not.toContain('二进制路径')
    expect(wrapper.text()).not.toContain('/run/nginx.pid')
    expect(wrapper.text()).not.toContain('/usr/sbin/nginx')
    expect(wrapper.get('.validation-result__diagnostic').text()).toBe(
      healthyStatus.startup_validation?.diagnostic,
    )
    expect(wrapper.text()).toContain('正在恢复')
    expect(wrapper.text()).toContain('NGINX_RECOVERING')
    expect(wrapper.get('time.dashboard__sample-time').attributes('datetime')).toBe(
      healthyStatus.sampled_at,
    )
  })

  it('renders runtime evidence and status descriptions in English', async () => {
    appI18n.global.locale.value = 'en-US'
    const wrapper = mount(DashboardView, {
      props: {
        client: createStatusClient(vi.fn().mockResolvedValue(healthyStatus)),
      },
    })
    await flushPromises()

    expect(wrapper.get('h1').text()).toBe('Runtime status')
    expect(componentByName(wrapper, 'UI').text()).toContain('Healthy')
    expect(componentByName(wrapper, 'Nginx').text()).toContain('Running')
    expect(wrapper.text()).toContain('Process metrics')
    expect(wrapper.text()).toContain('Startup validation')
    expect(wrapper.text()).toContain('Automatic recovery')
    expect(wrapper.text()).toContain('Recovering')
    expect(wrapper.text()).not.toContain('运行状态')
  })

  it('polls every five seconds', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const getSystemStatus = vi
      .fn<(signal?: AbortSignal) => Promise<SystemStatusResponse>>()
      .mockResolvedValue(healthyStatus)
    const wrapper = mount(DashboardView, {
      props: { client: createStatusClient(getSystemStatus) },
    })
    await flushPromises()

    await vi.advanceTimersByTimeAsync(4999)
    expect(getSystemStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(getSystemStatus).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(5000)
    expect(getSystemStatus).toHaveBeenCalledTimes(3)

    wrapper.unmount()
  })

  it('does not overlap scheduled requests while one is pending', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const first = createDeferred<SystemStatusResponse>()
    const getSystemStatus = vi
      .fn<(signal?: AbortSignal) => Promise<SystemStatusResponse>>()
      .mockReturnValueOnce(first.promise)
      .mockResolvedValue(healthyStatus)
    const wrapper = mount(DashboardView, {
      props: { client: createStatusClient(getSystemStatus) },
    })

    await vi.advanceTimersByTimeAsync(15_000)
    expect(getSystemStatus).toHaveBeenCalledTimes(1)

    first.resolve(healthyStatus)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    expect(getSystemStatus).toHaveBeenCalledTimes(2)

    wrapper.unmount()
  })

  it('supports one manual refresh and creates a new AbortController for it', async () => {
    const getSystemStatus = vi
      .fn<(signal?: AbortSignal) => Promise<SystemStatusResponse>>()
      .mockResolvedValue(healthyStatus)
    const wrapper = mount(DashboardView, {
      props: { client: createStatusClient(getSystemStatus) },
    })
    await flushPromises()

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(getSystemStatus).toHaveBeenCalledTimes(2)
    expect(getSystemStatus.mock.calls[0]?.[0]).not.toBe(getSystemStatus.mock.calls[1]?.[0])
    expect(wrapper.get('[aria-live="polite"]').text()).toContain('已刷新')
  })

  it('aborts the pending request and clears polling when unmounted', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const clearInterval = vi.spyOn(globalThis, 'clearInterval')
    let requestSignal: AbortSignal | undefined
    const getSystemStatus = vi.fn<(signal?: AbortSignal) => Promise<SystemStatusResponse>>(
      (signal) => {
        requestSignal = signal
        return new Promise((_resolve, reject) => {
          signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
        })
      },
    )
    const wrapper = mount(DashboardView, {
      props: { client: createStatusClient(getSystemStatus) },
    })

    wrapper.unmount()
    await flushPromises()

    expect(requestSignal?.aborted).toBe(true)
    expect(clearInterval).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['Nginx', { nginx: 'degraded' }, '降级'],
    ['Nginx', { nginx: 'stopped' }, '已停止'],
    ['Nginx', { nginx: 'unknown' }, '未知'],
    ['Agent', { agent: 'unavailable' }, '不可用'],
  ] as const)('renders %s state as %s', async (componentName, components, expected) => {
    const getSystemStatus = vi
      .fn<(signal?: AbortSignal) => Promise<SystemStatusResponse>>()
      .mockResolvedValue(statusWithComponents(components))
    const wrapper = mount(DashboardView, {
      props: { client: createStatusClient(getSystemStatus) },
    })
    await flushPromises()

    expect(componentByName(wrapper, componentName).text()).toContain(expected)
    if (expected === '未知') {
      expect(componentByName(wrapper, componentName).text()).not.toContain('已停止')
    }
    wrapper.unmount()
  })

  it('renders nullable evidence as unable to confirm rather than zero', async () => {
    const unknownStatus: SystemStatusResponse = {
      ...statusWithComponents({ nginx: 'unknown' }),
      master: null,
      workers: [],
      build: null,
      startup_validation: null,
      recovery: null,
      issues: ['AGENT_UNAVAILABLE'],
    }
    const getSystemStatus = vi
      .fn<(signal?: AbortSignal) => Promise<SystemStatusResponse>>()
      .mockResolvedValue(unknownStatus)
    const wrapper = mount(DashboardView, {
      props: { client: createStatusClient(getSystemStatus) },
    })
    await flushPromises()

    expect(wrapper.text().match(/无法确认/g)?.length).toBeGreaterThanOrEqual(4)
    expect(wrapper.text()).not.toContain('Master PID0')
    expect(componentByName(wrapper, 'Nginx').text()).toContain('未知')
  })

  it('keeps only the in-memory last success and marks it stale after polling fails', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const getSystemStatus = vi
      .fn<(signal?: AbortSignal) => Promise<SystemStatusResponse>>()
      .mockResolvedValueOnce(healthyStatus)
      .mockRejectedValueOnce(
        new APIRequestError({
          kind: 'network',
          message: 'connect ECONNREFUSED private-host:9000',
        }),
      )
    const wrapper = mount(DashboardView, {
      props: { client: createStatusClient(getSystemStatus) },
    })
    await flushPromises()

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()

    expect(wrapper.text()).toContain('旧数据')
    expect(wrapper.text()).toContain('1.30.3')
    expect(wrapper.get('time.dashboard__sample-time').attributes('datetime')).toBe(
      healthyStatus.sampled_at,
    )
    expect(wrapper.text()).toContain('刷新失败，正在显示上一次成功获取的数据。')
    expect(wrapper.text()).not.toContain('ECONNREFUSED')
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)

    wrapper.unmount()
  })

  it('starts empty after a full remount when no request succeeds', async () => {
    const getSystemStatus = vi
      .fn<(signal?: AbortSignal) => Promise<SystemStatusResponse>>()
      .mockRejectedValue(new APIRequestError({ kind: 'network', message: 'private network detail' }))
    const wrapper = mount(DashboardView, {
      props: { client: createStatusClient(getSystemStatus) },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('暂时无法获取运行状态。')
    expect(wrapper.text()).not.toContain('旧数据')
    expect(wrapper.text()).not.toContain('1.30.3')
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
  })

  it('reuses the shared session-expiry redirect for an expired status request', async () => {
    const sessionClient: SessionClient = {
      getSession: vi.fn<() => Promise<SessionResponse>>().mockResolvedValue(currentSession),
      login: vi.fn<(input: LoginRequest) => Promise<SessionResponse>>().mockResolvedValue(currentSession),
      logout: vi.fn<(csrfToken: string) => Promise<void>>().mockResolvedValue(undefined),
    }
    const store = createSessionStore(sessionClient)
    await store.restore()
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(
        {
          error: {
            code: 'AUTH_SESSION_EXPIRED',
            message: 'session expired',
            request_id: 'request-expired',
          },
        },
        401,
      ),
    )
    const client = new APIClient(fetchMock)
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'dashboard', component: { template: '<div />' } },
        { path: '/login', name: 'login', component: { template: '<div />' } },
      ],
    })
    await router.push('/')
    const uninstall = installSessionExpiryRedirect(client, store, router)

    const wrapper = mount(DashboardView, {
      props: { client },
      global: { plugins: [router] },
    })
    await flushPromises()

    expect(router.currentRoute.value.name).toBe('login')
    expect(store.state).toEqual({ phase: 'anonymous', session: null })
    wrapper.unmount()
    uninstall()
  })

  it('exposes refresh as the sole action and sends no unsafe method', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(healthyStatus))
    const client = new APIClient(fetchMock)
    const wrapper = mount(DashboardView, { props: { client } })
    await flushPromises()

    expect(wrapper.findAll('button').map((button) => button.text())).toEqual(['刷新状态'])
    expect(wrapper.findAll('button').some((button) => /启动|停止|重载|重启|start|stop|reload|restart/i.test(button.text()))).toBe(false)

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock.mock.calls.map((call) => call[1]?.method)).toEqual(['GET', 'GET'])
  })

  it('uses responsive grids, shared tokens, a 44-pixel action and no forbidden chrome', () => {
    const source = [
      dashboardSource,
      componentHealthSource,
      runtimeStatusSource,
      processMetricsSource,
      validationResultSource,
    ].join('\n')

    expect(dashboardSource).toContain('grid-template-columns: repeat(3, minmax(0, 1fr))')
    expect(dashboardSource).toContain('@media (max-width: 833px)')
    expect(dashboardSource).toContain('grid-template-columns: repeat(2, minmax(0, 1fr))')
    expect(dashboardSource).toContain('@media (max-width: 640px)')
    expect(dashboardSource).toContain('grid-template-columns: minmax(0, 1fr)')
    expect(dashboardSource).toContain('min-height: var(--component-control-min-size)')
    expect(source).toContain('min-width: 0')
    expect(source).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(source).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(source).not.toContain('box-shadow')
    expect(source).not.toMatch(/font-weight:\s*500\b/)
    expect(source).not.toMatch(/\b(?:fetch|localStorage|sessionStorage|indexedDB|caches)\b/)
  })
})

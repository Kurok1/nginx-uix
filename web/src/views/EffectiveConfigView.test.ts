/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import { APIClient, APIRequestError } from '../api/client'
import type {
  EffectiveConfigResponse,
	LoginRequest,
	SessionResponse,
	StructuredEffectiveConfigResponse,
} from '../api/types'
import { installSessionExpiryRedirect } from '../router'
import { createSessionStore, type SessionClient } from '../session'
import EffectiveConfigView from './EffectiveConfigView.vue'
import effectiveConfigSource from './EffectiveConfigView.vue?raw'

const baseStylesSource = readFileSync(resolve(process.cwd(), 'src/styles/base.css'), 'utf8')

const currentSession: SessionResponse = {
  user: { id: 7, username: 'operator', created_at: '2026-07-15T11:00:00Z' },
  csrf_token: 'csrf-token',
  created_at: '2026-07-15T12:00:00Z',
  last_seen_at: '2026-07-15T12:30:00Z',
  idle_expires_at: '2026-07-15T20:00:00Z',
  absolute_expires_at: '2026-07-16T12:00:00Z',
}

const repeatedConfig: StructuredEffectiveConfigResponse = {
  generated_at: '2026-07-15T08:31:00Z',
  nginx_version: '1.30.3',
  entry_config_path: '/etc/nginx/nginx.conf',
	display_mode: 'structured',
  occurrence_count: 3,
  occurrences: [
    {
      id: 'occurrence-000001',
      load_order: 1,
      path: '/etc/nginx/nginx.conf',
      content: 'events {}\nhttp {\n  include /etc/nginx/conf.d/*.conf;\n}\n',
    },
    {
      id: 'occurrence-000002',
      load_order: 2,
      path: '/etc/nginx/conf.d/site.conf',
      content: 'server { listen 80; }\n',
    },
    {
      id: 'occurrence-000003',
      load_order: 3,
      path: '/etc/nginx/conf.d/site.conf',
      content: 'server { listen 8080; }\n',
    },
  ],
	raw_content: null,
	warnings: [],
}

const emptyConfig: StructuredEffectiveConfigResponse = {
  ...repeatedConfig,
  occurrence_count: 0,
  occurrences: [],
}

const rawConfig: EffectiveConfigResponse = {
	generated_at: '2026-07-15T08:32:00Z',
	nginx_version: '1.30.3',
	entry_config_path: '/etc/nginx/nginx.conf',
	display_mode: 'raw',
	occurrence_count: 0,
	occurrences: [],
	raw_content: '# configuration file /etc/nginx/nginx.conf:\nevents {}\n',
	warnings: ['NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS'],
}

function createConfigClient(
  getEffectiveConfig: (signal?: AbortSignal) => Promise<EffectiveConfigResponse>,
) {
  return { getEffectiveConfig }
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

function apiFailure(code: string, status: number): APIRequestError {
  return new APIRequestError({
    kind: 'api',
    message: 'private upstream diagnostic must not render',
    status,
    apiError: {
      code,
      message: 'private upstream diagnostic must not render',
      request_id: `request-${code.toLowerCase()}`,
    },
  })
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('EffectiveConfigView', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    localStorage.clear()
    sessionStorage.clear()
  })

  it('loads immediately with a caller-owned AbortSignal and no premature empty state', async () => {
    const deferred = createDeferred<EffectiveConfigResponse>()
    const getEffectiveConfig = vi
      .fn<(signal?: AbortSignal) => Promise<EffectiveConfigResponse>>()
      .mockReturnValue(deferred.promise)
    const wrapper = mount(EffectiveConfigView, {
      props: { client: createConfigClient(getEffectiveConfig) },
    })

    expect(getEffectiveConfig).toHaveBeenCalledTimes(1)
    expect(getEffectiveConfig.mock.calls[0]?.[0]).toBeInstanceOf(AbortSignal)
    await wrapper.vm.$nextTick()
    expect(wrapper.get('.effective-config__snapshot').attributes('aria-busy')).toBe('true')
    expect(wrapper.text()).toContain('正在加载生效配置…')
    expect(wrapper.text()).not.toContain('当前没有加载到配置文件')

    deferred.resolve(repeatedConfig)
    await flushPromises()
    wrapper.unmount()
  })

  it('renders response metadata, ordered occurrences and independently selects a repeated path', async () => {
    const getEffectiveConfig = vi
      .fn<(signal?: AbortSignal) => Promise<EffectiveConfigResponse>>()
      .mockResolvedValue(repeatedConfig)
    const wrapper = mount(EffectiveConfigView, {
      props: { client: createConfigClient(getEffectiveConfig) },
    })
    await flushPromises()

    expect(wrapper.get('h1').text()).toBe('生效配置')
    expect(wrapper.text()).toContain('Nginx 版本：1.30.3')
    expect(wrapper.text()).toContain('入口配置：/etc/nginx/nginx.conf')
    expect(wrapper.text()).toContain('加载项：3')
    expect(wrapper.get('time').attributes('datetime')).toBe(repeatedConfig.generated_at)
    expect(wrapper.get('code').element.textContent).toBe(repeatedConfig.occurrences[0]?.content)

    await wrapper.get('[data-id="occurrence-000003"]').trigger('click')

    expect(wrapper.get('code').element.textContent).toBe(repeatedConfig.occurrences[2]?.content)
    expect(wrapper.get('[data-id="occurrence-000003"]').attributes('aria-current')).toBe('true')
    expect(wrapper.get('[data-id="occurrence-000002"]').attributes('aria-current')).toBeUndefined()
  })

  it('renders a specific empty result without mounting a file list or viewer', async () => {
    const wrapper = mount(EffectiveConfigView, {
      props: {
        client: createConfigClient(vi.fn().mockResolvedValue(emptyConfig)),
      },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('当前没有加载到配置文件。')
    expect(wrapper.find('.config-file-list').exists()).toBe(false)
    expect(wrapper.find('.read-only-code-viewer').exists()).toBe(false)
  })

	it('renders raw fallback with an actionable warning and no invented file list', async () => {
		const wrapper = mount(EffectiveConfigView, {
			props: { client: createConfigClient(vi.fn().mockResolvedValue(rawConfig)) },
		})
		await flushPromises()

		expect(wrapper.text()).toContain('结构未验证')
		expect(wrapper.text()).toContain('NGINX_UIX_EFFECTIVE_CONFIG_ROOTS')
		expect(wrapper.text()).toContain('展示模式：原始输出')
		expect(wrapper.text()).toContain('原始 Nginx 输出')
		expect(wrapper.text()).toContain('nginx -T 标准输出')
		expect(wrapper.get('.read-only-code-viewer__content code').element.textContent).toBe(
			rawConfig.raw_content,
		)
		expect(wrapper.find('.config-file-list').exists()).toBe(false)
		expect(wrapper.text()).not.toContain('当前没有加载到配置文件')
	})

  it.each([
    ['NGINX_CONFIG_INVALID', 422, 'Nginx 配置当前无效，无法读取生效配置。'],
    ['NGINX_COMMAND_TIMEOUT', 504, '读取生效配置超时，请稍后重试。'],
    ['NGINX_OUTPUT_TOO_LARGE', 502, '生效配置超过安全读取限制，无法在此显示。'],
    ['AGENT_UNAVAILABLE', 503, '本地 Agent 暂时不可用，无法读取生效配置。'],
  ])('renders the safe %s state without internal diagnostics', async (code, status, expected) => {
    const wrapper = mount(EffectiveConfigView, {
      props: {
        client: createConfigClient(vi.fn().mockRejectedValue(apiFailure(code, status))),
      },
    })
    await flushPromises()

    expect(wrapper.text()).toContain(expected)
    expect(wrapper.text()).not.toContain('private upstream')
    expect(wrapper.text()).not.toContain('旧数据')
    expect(wrapper.find('code').exists()).toBe(false)
  })

  it('renders valid configuration without consulting Nginx running state', async () => {
    const getSystemStatus = vi.fn().mockResolvedValue({ components: { nginx: 'stopped' } })
    const client = {
      getEffectiveConfig: vi.fn().mockResolvedValue(repeatedConfig),
      getSystemStatus,
    }
    const wrapper = mount(EffectiveConfigView, { props: { client } })
    await flushPromises()

    expect(wrapper.get('code').element.textContent).toBe(repeatedConfig.occurrences[0]?.content)
    expect(getSystemStatus).not.toHaveBeenCalled()
  })

  it('refreshes on demand with a new AbortSignal and replaces the snapshot', async () => {
    const refreshed: StructuredEffectiveConfigResponse = {
      ...repeatedConfig,
      generated_at: '2026-07-15T08:35:00Z',
      occurrence_count: 1,
      occurrences: [repeatedConfig.occurrences[2]],
    }
    const getEffectiveConfig = vi
      .fn<(signal?: AbortSignal) => Promise<EffectiveConfigResponse>>()
      .mockResolvedValueOnce(repeatedConfig)
      .mockResolvedValueOnce(refreshed)
    const wrapper = mount(EffectiveConfigView, {
      props: { client: createConfigClient(getEffectiveConfig) },
    })
    await flushPromises()

    await wrapper.get('.effective-config__refresh').trigger('click')
    await flushPromises()

    expect(getEffectiveConfig).toHaveBeenCalledTimes(2)
    expect(getEffectiveConfig.mock.calls[0]?.[0]).not.toBe(getEffectiveConfig.mock.calls[1]?.[0])
    expect(wrapper.get('time').attributes('datetime')).toBe(refreshed.generated_at)
    expect(wrapper.get('code').element.textContent).toBe(refreshed.occurrences[0]?.content)
    expect(wrapper.get('[aria-live="polite"]').text()).toContain('已刷新生效配置')
  })

  it('keeps only the in-memory last success and marks it stale after refresh fails', async () => {
    const getEffectiveConfig = vi
      .fn<(signal?: AbortSignal) => Promise<EffectiveConfigResponse>>()
      .mockResolvedValueOnce(repeatedConfig)
      .mockRejectedValueOnce(apiFailure('AGENT_UNAVAILABLE', 503))
    const wrapper = mount(EffectiveConfigView, {
      props: { client: createConfigClient(getEffectiveConfig) },
    })
    await flushPromises()

    await wrapper.get('.effective-config__refresh').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('旧数据')
    expect(wrapper.text()).toContain('刷新失败，正在显示上一次成功获取的数据。')
    expect(wrapper.get('code').element.textContent).toBe(repeatedConfig.occurrences[0]?.content)
    expect(wrapper.text()).not.toContain('private upstream')
  })

  it('starts empty on a fresh mount when no request succeeds', async () => {
    localStorage.setItem('previous-config', repeatedConfig.occurrences[0]?.content ?? '')
    sessionStorage.setItem('previous-config', repeatedConfig.occurrences[1]?.content ?? '')
    const wrapper = mount(EffectiveConfigView, {
      props: {
        client: createConfigClient(vi.fn().mockRejectedValue(new TypeError('private detail'))),
      },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('暂时无法读取生效配置。')
    expect(wrapper.text()).not.toContain('旧数据')
    expect(wrapper.find('code').exists()).toBe(false)
  })

  it('reuses the shared session-expiry redirect for an expired config request', async () => {
    const sessionClient: SessionClient = {
      getSession: vi.fn<() => Promise<SessionResponse>>().mockResolvedValue(currentSession),
      login: vi
        .fn<(input: LoginRequest) => Promise<SessionResponse>>()
        .mockResolvedValue(currentSession),
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
        { path: '/configuration', name: 'configuration', component: { template: '<div />' } },
        { path: '/login', name: 'login', component: { template: '<div />' } },
      ],
    })
    await router.push('/configuration')
    const uninstall = installSessionExpiryRedirect(client, store, router)

    const wrapper = mount(EffectiveConfigView, {
      props: { client },
      global: { plugins: [router] },
    })
    await flushPromises()

    expect(router.currentRoute.value.name).toBe('login')
    expect(store.state).toEqual({ phase: 'anonymous', session: null })
    wrapper.unmount()
    uninstall()
  })

  it('aborts its pending request when unmounted', async () => {
    let requestSignal: AbortSignal | undefined
    const getEffectiveConfig = vi.fn<(signal?: AbortSignal) => Promise<EffectiveConfigResponse>>(
      (signal) => {
        requestSignal = signal
        return new Promise((_resolve, reject) => {
          signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
        })
      },
    )
    const wrapper = mount(EffectiveConfigView, {
      props: { client: createConfigClient(getEffectiveConfig) },
    })

    wrapper.unmount()
    await flushPromises()

    expect(requestSignal?.aborted).toBe(true)
  })

  it('does not persist configuration through Storage, Cache Storage or IndexedDB', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    const openCache = vi.fn()
    const openDatabase = vi.fn()
    vi.stubGlobal('caches', { open: openCache })
    vi.stubGlobal('indexedDB', { open: openDatabase })
    const wrapper = mount(EffectiveConfigView, {
      props: { client: createConfigClient(vi.fn().mockResolvedValue(repeatedConfig)) },
    })
    await flushPromises()

    await wrapper.get('[data-id="occurrence-000003"]').trigger('click')
    await wrapper.get('button[aria-pressed]').trigger('click')
    await wrapper.get('.effective-config__refresh').trigger('click')
    await flushPromises()

    expect(setItem).not.toHaveBeenCalled()
    expect(openCache).not.toHaveBeenCalled()
    expect(openDatabase).not.toHaveBeenCalled()
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
  })

  it('uses a bounded responsive layout and contains no direct request or unsafe action', () => {
    expect(baseStylesSource).toMatch(/html,\s*body,\s*#app\s*{[^}]*max-width:\s*100%/s)
    expect(baseStylesSource).not.toMatch(/overflow-x:\s*hidden/)
    expect(effectiveConfigSource).toContain('grid-template-columns: minmax(0, 320px) minmax(0, 1fr)')
    expect(effectiveConfigSource).toContain('@media (max-width: 833px)')
    expect(effectiveConfigSource).toContain('grid-template-columns: minmax(0, 1fr)')
    expect(effectiveConfigSource).toContain('min-height: var(--component-control-min-size)')
    expect(effectiveConfigSource).toContain('min-width: 0')
    expect(effectiveConfigSource).not.toMatch(/\b(?:fetch|XMLHttpRequest|localStorage|sessionStorage|indexedDB|caches)\b/)
    expect(effectiveConfigSource).not.toMatch(/\b(?:POST|PUT|PATCH|DELETE)\b/)
    expect(effectiveConfigSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(effectiveConfigSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(effectiveConfigSource).not.toContain('box-shadow')
    expect(effectiveConfigSource).not.toMatch(/font-weight:\s*500\b/)
  })
})

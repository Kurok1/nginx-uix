/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { APIClient, APIRequestError } from './client'

const sessionPayload = {
  user: { id: 7, username: 'operator' },
  csrf_token: 'csrf-token',
  idle_expires_at: '2026-07-14T20:00:00Z',
  absolute_expires_at: '2026-07-15T12:00:00Z',
}

const systemStatusPayload = {
  sampled_at: '2026-07-15T08:30:00Z',
  components: {
    ui: 'healthy',
    agent: 'healthy',
    nginx: 'degraded',
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
  ],
  build: {
    version: '1.30.3',
    configure_arguments: ['--with-http_ssl_module', '--with-http_v2_module'],
  },
  startup_validation: {
    valid: true,
    checked_at: '2026-07-15T07:59:58Z',
    exit_code: 0,
    diagnostic: 'syntax is ok',
  },
  recovery: {
    count: 1,
    last_result: 'restarting',
    permanent: false,
  },
  issues: ['NGINX_RECOVERING'],
}

const effectiveConfigPayload = {
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

const rawEffectiveConfigPayload = {
  generated_at: '2026-07-15T08:32:00Z',
  nginx_version: '1.30.3',
  entry_config_path: '/etc/nginx/nginx.conf',
  display_mode: 'raw',
  occurrence_count: 0,
  occurrences: [],
  raw_content: '# configuration file /etc/nginx/nginx.conf:\nevents {}\n',
  warnings: ['NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS'],
}

function jsonResponse(body: unknown, status = 200, headers?: HeadersInit): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

function requestAt(fetchMock: ReturnType<typeof vi.fn<typeof fetch>>, index = 0): [RequestInfo | URL, RequestInit] {
  const call = fetchMock.mock.calls[index]
  if (call === undefined) {
    throw new Error(`missing fetch call at index ${index}`)
  }
  return [call[0], call[1] ?? {}]
}

describe('APIClient', () => {
  it('posts the exact login DTO to a same-origin relative URL without CSRF', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(sessionPayload))
    const client = new APIClient(fetchMock)

    await expect(client.login({ username: 'operator', password: 'secret' })).resolves.toEqual(sessionPayload)

    const [url, init] = requestAt(fetchMock)
    const headers = new Headers(init.headers)
    expect(url).toBe('/api/v1/auth/session')
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('same-origin')
    expect(init.cache).toBe('no-store')
    expect(headers.get('Content-Type')).toBe('application/json')
    expect(headers.has('X-CSRF-Token')).toBe(false)
    expect(init.body).toBe('{"username":"operator","password":"secret"}')
  })

  it('restores the session without adding JSON or CSRF headers to a safe request', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(sessionPayload))
    const client = new APIClient(fetchMock)

    await expect(client.getSession()).resolves.toEqual(sessionPayload)

    const [url, init] = requestAt(fetchMock)
    const headers = new Headers(init.headers)
    expect(url).toBe('/api/v1/auth/session')
    expect(init.method).toBe('GET')
    expect(init.credentials).toBe('same-origin')
    expect(init.cache).toBe('no-store')
    expect(headers.has('Content-Type')).toBe(false)
    expect(headers.has('X-CSRF-Token')).toBe(false)
    expect(init.body).toBeUndefined()
  })

  it('gets and strictly parses runtime status with the caller AbortSignal', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(systemStatusPayload))
    const client = new APIClient(fetchMock)
    const controller = new AbortController()

    await expect(client.getSystemStatus(controller.signal)).resolves.toEqual(systemStatusPayload)

    const [url, init] = requestAt(fetchMock)
    const headers = new Headers(init.headers)
    expect(url).toBe('/api/v1/system/status')
    expect(init.method).toBe('GET')
    expect(init.signal).toBe(controller.signal)
    expect(init.credentials).toBe('same-origin')
    expect(init.cache).toBe('no-store')
    expect(headers.has('Content-Type')).toBe(false)
    expect(headers.has('X-CSRF-Token')).toBe(false)
    expect(init.body).toBeUndefined()
  })

  it('gets ordered effective-config occurrences with the caller AbortSignal', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(effectiveConfigPayload))
    const client = new APIClient(fetchMock)
    const controller = new AbortController()

    await expect(client.getEffectiveConfig(controller.signal)).resolves.toEqual(
      effectiveConfigPayload,
    )

    const [url, init] = requestAt(fetchMock)
    const headers = new Headers(init.headers)
    expect(url).toBe('/api/v1/nginx/effective-config')
    expect(init.method).toBe('GET')
    expect(init.signal).toBe(controller.signal)
    expect(init.credentials).toBe('same-origin')
    expect(init.cache).toBe('no-store')
    expect(headers.has('Content-Type')).toBe(false)
    expect(headers.has('X-CSRF-Token')).toBe(false)
    expect(init.body).toBeUndefined()
  })

  it('accepts a raw effective-config fallback without inventing file occurrences', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(rawEffectiveConfigPayload))
    const client = new APIClient(fetchMock)

    await expect(client.getEffectiveConfig()).resolves.toEqual(rawEffectiveConfigPayload)
  })

  it.each([
    ['invalid generation time', { ...effectiveConfigPayload, generated_at: 'not-a-time' }],
    ['invalid occurrence count', { ...effectiveConfigPayload, occurrence_count: 2 }],
    [
      'duplicate response-scoped id',
      {
        ...effectiveConfigPayload,
        occurrences: effectiveConfigPayload.occurrences.map((occurrence, index) =>
          index === 2 ? { ...occurrence, id: 'occurrence-000002' } : occurrence,
        ),
      },
    ],
    [
      'non-sequential load order',
      {
        ...effectiveConfigPayload,
        occurrences: effectiveConfigPayload.occurrences.map((occurrence, index) =>
          index === 1 ? { ...occurrence, load_order: 3 } : occurrence,
        ),
      },
    ],
    [
      'invalid occurrence content',
      {
        ...effectiveConfigPayload,
        occurrences: effectiveConfigPayload.occurrences.map((occurrence, index) =>
          index === 0 ? { ...occurrence, content: 7 } : occurrence,
        ),
      },
    ],
    ['structured mode with raw content', { ...effectiveConfigPayload, raw_content: 'events {}' }],
    [
      'structured mode with warning',
      { ...effectiveConfigPayload, warnings: ['NGINX_CONFIG_STRUCTURE_UNVERIFIED'] },
    ],
    [
      'raw mode with occurrences',
      {
        ...rawEffectiveConfigPayload,
        occurrence_count: 1,
        occurrences: [effectiveConfigPayload.occurrences[0]],
      },
    ],
    ['raw mode without content', { ...rawEffectiveConfigPayload, raw_content: null }],
    ['raw mode without warning', { ...rawEffectiveConfigPayload, warnings: [] }],
    ['raw mode with unknown warning', { ...rawEffectiveConfigPayload, warnings: ['UNKNOWN'] }],
  ])('rejects effective configuration with %s', async (_name, payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))
    const client = new APIClient(fetchMock)

    await expect(client.getEffectiveConfig()).rejects.toMatchObject({
      kind: 'malformed_response',
      status: 200,
    })
  })

  it.each([
    ['invalid sample time', { ...systemStatusPayload, sampled_at: 'not-a-time' }],
    [
      'invalid component state',
      { ...systemStatusPayload, components: { ...systemStatusPayload.components, nginx: 'paused' } },
    ],
    [
      'invalid process role',
      { ...systemStatusPayload, master: { ...systemStatusPayload.master, role: 'worker' } },
    ],
    [
      'invalid build arguments',
      {
        ...systemStatusPayload,
        build: { ...systemStatusPayload.build, configure_arguments: ['--valid', 7] },
      },
    ],
    [
      'invalid validation exit code',
      {
        ...systemStatusPayload,
        startup_validation: { ...systemStatusPayload.startup_validation, exit_code: 1.5 },
      },
    ],
    [
      'invalid recovery result',
      { ...systemStatusPayload, recovery: { ...systemStatusPayload.recovery, last_result: 'retried' } },
    ],
    ['invalid issues', { ...systemStatusPayload, issues: ['AGENT_UNAVAILABLE', 9] }],
  ])('rejects runtime status with %s', async (_name, payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))
    const client = new APIClient(fetchMock)

    await expect(client.getSystemStatus()).rejects.toMatchObject({
      kind: 'malformed_response',
      status: 200,
    })
  })

  it('uses the current CSRF token only for authenticated logout and accepts 204', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 204 }))
    const client = new APIClient(fetchMock)

    await expect(client.logout('current-csrf-token')).resolves.toBeUndefined()

    const [url, init] = requestAt(fetchMock)
    const headers = new Headers(init.headers)
    expect(url).toBe('/api/v1/auth/session')
    expect(init.method).toBe('DELETE')
    expect(init.credentials).toBe('same-origin')
    expect(init.cache).toBe('no-store')
    expect(headers.get('X-CSRF-Token')).toBe('current-csrf-token')
    expect(headers.has('Content-Type')).toBe(false)
    expect(init.body).toBeUndefined()
  })

  it('parses a stable API error envelope and Retry-After header', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(
        {
          error: {
            code: 'rate_limited',
            message: '登录尝试过于频繁',
            request_id: 'request-42',
            details: { retry_after_seconds: 91 },
          },
        },
        429,
        { 'Retry-After': '91' },
      ),
    )
    const client = new APIClient(fetchMock)

    await expect(client.login({ username: 'operator', password: 'wrong' })).rejects.toMatchObject({
      kind: 'api',
      status: 429,
      apiError: {
        code: 'rate_limited',
        message: '登录尝试过于频繁',
        request_id: 'request-42',
        details: { retry_after_seconds: 91 },
      },
      retryAfterSeconds: 91,
    })
  })

  it.each([
    ['invalid JSON', new Response('{not-json', { status: 200 })],
    ['invalid success DTO', jsonResponse({ csrf_token: 'missing-user' })],
    ['invalid error envelope', jsonResponse({ message: 'not-an-envelope' }, 500)],
  ])('returns a typed malformed-response error for %s', async (_name, response) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(response)
    const client = new APIClient(fetchMock)

    await expect(client.getSession()).rejects.toMatchObject({
      kind: 'malformed_response',
      status: response.status,
    })
  })

  it('wraps a network failure without leaking an untyped fetch exception', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockRejectedValue(new TypeError('fetch failed'))
    const client = new APIClient(fetchMock)

    const request = client.getSession()
    await expect(request).rejects.toBeInstanceOf(APIRequestError)
    await expect(request).rejects.toMatchObject({ kind: 'network', status: undefined })
  })
})

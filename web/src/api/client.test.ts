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

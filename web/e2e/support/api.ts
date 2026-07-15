/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import AxeBuilder from '@axe-core/playwright'
import { expect, type BrowserContext, type Page, type Request } from '@playwright/test'

export const appOrigin = 'http://127.0.0.1:4173'
export const sessionCookieName = 'nginx_uix_session'
export const sessionCookieValue = 'e2e-session'
export const csrfToken = 'e2e-csrf-token'

interface PublicSessionDTO {
  user: {
    id: number
    username: string
    created_at: string
  }
  csrf_token: string
  created_at: string
  last_seen_at: string
  idle_expires_at: string
  absolute_expires_at: string
}

export interface PublicSystemStatusDTO {
  sampled_at: string
  components: {
    ui: 'healthy'
    agent: 'healthy' | 'unavailable'
    nginx: 'running' | 'degraded' | 'stopped' | 'unknown'
  }
  master: PublicProcessDTO | null
  workers: PublicProcessDTO[]
  build: {
    version: string
    configure_arguments: string[]
  } | null
  startup_validation: {
    valid: boolean
    checked_at: string
    exit_code: number | null
    diagnostic: string
  } | null
  recovery: {
    count: number
    last_result: '' | 'restarting' | 'invalid_config' | 'permanent_failure'
    permanent: boolean
  } | null
  issues: string[]
}

interface PublicProcessDTO {
  pid: number
  role: 'master' | 'worker'
  started_at: string
}

interface PublicEffectiveConfigDTO {
  generated_at: string
  nginx_version: string
  entry_config_path: '/etc/nginx/nginx.conf'
	display_mode: 'structured' | 'raw'
  occurrence_count: number
  occurrences: Array<{
    id: string
    load_order: number
    path: string
    content: string
  }>
	raw_content: string | null
	warnings: Array<
		| 'NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS'
		| 'NGINX_CONFIG_STRUCTURE_UNVERIFIED'
	>
}

interface PublicErrorEnvelope {
  error: {
    code: string
    message: string
    request_id: string
    details?: Readonly<Record<string, unknown>>
  }
}

export const authenticatedSession = {
  user: {
    id: 1,
    username: 'admin',
    created_at: '2026-07-14T08:00:00Z',
  },
  csrf_token: csrfToken,
  created_at: '2026-07-14T08:00:00Z',
  last_seen_at: '2026-07-14T08:01:00Z',
  idle_expires_at: '2026-07-14T08:31:00Z',
  absolute_expires_at: '2026-07-15T08:00:00Z',
} satisfies PublicSessionDTO

export const healthyStatus = {
  sampled_at: '2026-07-14T08:02:00Z',
  components: {
    ui: 'healthy',
    agent: 'healthy',
    nginx: 'running',
  },
  master: {
    pid: 101,
    role: 'master',
    started_at: '2026-07-14T08:00:30Z',
  },
  workers: [
    { pid: 102, role: 'worker', started_at: '2026-07-14T08:00:31Z' },
    { pid: 103, role: 'worker', started_at: '2026-07-14T08:00:31Z' },
  ],
  build: {
    version: 'nginx/1.30.3',
    configure_arguments: ['--prefix=/etc/nginx', '--with-http_ssl_module'],
  },
  startup_validation: {
    valid: true,
    checked_at: '2026-07-14T08:00:29Z',
    exit_code: 0,
    diagnostic: 'nginx: configuration file /etc/nginx/nginx.conf test is successful',
  },
  recovery: {
    count: 1,
    last_result: 'restarting',
    permanent: false,
  },
  issues: [],
} satisfies PublicSystemStatusDTO

const longDirective = `add_header X-E2E-Long-Directive "${'segment-'.repeat(90)}";`

export const repeatedEffectiveConfig = {
  generated_at: '2026-07-14T08:03:00Z',
  nginx_version: 'nginx/1.30.3',
  entry_config_path: '/etc/nginx/nginx.conf',
	display_mode: 'structured',
  occurrence_count: 3,
  occurrences: [
    {
      id: 'occurrence-1',
      load_order: 1,
      path: '/etc/nginx/nginx.conf',
      content: `events {}\nhttp {\n  include conf.d/*.conf;\n  ${longDirective}\n}\n`,
    },
    {
      id: 'occurrence-2',
      load_order: 2,
      path: '/etc/nginx/conf.d/repeated.conf',
      content: 'server {\n  listen 80;\n  server_name first.example.test;\n}\n',
    },
    {
      id: 'occurrence-3',
      load_order: 3,
      path: '/etc/nginx/conf.d/repeated.conf',
      content: 'server {\n  listen 8080;\n  server_name second.example.test;\n}\n',
    },
  ],
	raw_content: null,
	warnings: [],
} satisfies PublicEffectiveConfigDTO

export const rawEffectiveConfig = {
	generated_at: '2026-07-14T08:04:00Z',
	nginx_version: 'nginx/1.30.3',
	entry_config_path: '/etc/nginx/nginx.conf',
	display_mode: 'raw',
	occurrence_count: 0,
	occurrences: [],
	raw_content: '# configuration file /etc/nginx/nginx.conf:\nevents {}\n',
	warnings: ['NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS'],
} satisfies PublicEffectiveConfigDTO

export function apiError(
  code: string,
  message: string,
  requestID = 'request-e2e',
  details?: Readonly<Record<string, unknown>>,
): PublicErrorEnvelope {
  return {
    error: {
      code,
      message,
      request_id: requestID,
      ...(details === undefined ? {} : { details }),
    },
  }
}

export interface MockResponse {
  status: number
  body?: unknown
  headers?: Readonly<Record<string, string>>
}

type Endpoint = 'session' | 'login' | 'logout' | 'status' | 'effectiveConfig'

export type MockResponses = Partial<
  Readonly<Record<Endpoint, MockResponse | readonly MockResponse[]>>
>

export interface RecordedRequest {
  endpoint: Endpoint
  method: string
  url: string
  headers: Readonly<Record<string, string>>
  postData: string | null
}

export interface APIHarness {
  calls: RecordedRequest[]
  assertContract: () => void
  callsFor: (endpoint: Endpoint) => RecordedRequest[]
}

export async function installAPIMocks(page: Page, mocks: MockResponses): Promise<APIHarness> {
  const queues = createQueues(mocks)
  const calls: RecordedRequest[] = []
  const violations: string[] = []

  await page.route(`${appOrigin}/api/v1/**`, async (route) => {
    const request = route.request()
    const endpoint = resolveEndpoint(request)
    const url = new URL(request.url())

    if (url.origin !== appOrigin || url.search !== '' || url.hash !== '') {
      violations.push(`unexpected API URL: ${request.url()}`)
    }
    if (endpoint === null) {
      violations.push(`unexpected API request: ${request.method()} ${url.pathname}`)
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify(apiError('E2E_UNEXPECTED_REQUEST', 'Unexpected request')),
      })
      return
    }

    const headers = request.headers()
    calls.push({
      endpoint,
      method: request.method(),
      url: request.url(),
      headers,
      postData: request.postData(),
    })
    validateVisibleRequestContract(endpoint, headers, violations)

    const response = takeResponse(queues, endpoint)
    if (response === undefined) {
      violations.push(`no fixture for ${endpoint}: ${request.method()} ${url.pathname}`)
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify(apiError('E2E_MISSING_FIXTURE', 'Missing fixture')),
      })
      return
    }

    await route.fulfill({
      status: response.status,
      ...(response.status === 204
        ? {}
        : {
            contentType: 'application/json',
            body: JSON.stringify(response.body),
          }),
      headers: { 'Cache-Control': 'no-store', ...response.headers },
    })
  })

  return {
    calls,
    assertContract: () => expect(violations, 'browser-visible API contract violations').toEqual([]),
    callsFor: (endpoint) => calls.filter((call) => call.endpoint === endpoint),
  }
}

export async function setAuthenticatedCookie(context: BrowserContext): Promise<void> {
  await context.addCookies([
    {
      name: sessionCookieName,
      value: sessionCookieValue,
      url: appOrigin,
      httpOnly: true,
      sameSite: 'Strict',
      secure: false,
    },
  ])
}

export async function assertNoApplicationStorage(page: Page): Promise<void> {
  const storage = await page.evaluate(async () => ({
    localStorage: Object.keys(localStorage),
    sessionStorage: Object.keys(sessionStorage),
    cacheStorage: await caches.keys(),
    indexedDB: (await indexedDB.databases()).map((database) => database.name ?? ''),
  }))
  expect(storage).toEqual({
    localStorage: [],
    sessionStorage: [],
    cacheStorage: [],
    indexedDB: [],
  })
}

export async function assertNoAxeViolations(page: Page): Promise<void> {
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'])
    .analyze()
  expect(results.violations).toEqual([])
}

export function expectSameOriginCookie(call: RecordedRequest): void {
  expect(call.headers.cookie).toContain(`${sessionCookieName}=${sessionCookieValue}`)
}

function createQueues(mocks: MockResponses): Map<Endpoint, MockResponse[]> {
  const queues = new Map<Endpoint, MockResponse[]>()
  for (const endpoint of Object.keys(mocks) as Endpoint[]) {
    const response = mocks[endpoint]
    if (response !== undefined) {
      queues.set(endpoint, Array.isArray(response) ? [...response] : [response])
    }
  }
  return queues
}

function takeResponse(
  queues: Map<Endpoint, MockResponse[]>,
  endpoint: Endpoint,
): MockResponse | undefined {
  const queue = queues.get(endpoint)
  if (queue === undefined || queue.length === 0) {
    return undefined
  }
  if (queue.length === 1) {
    return queue[0]
  }
  return queue.shift()
}

function resolveEndpoint(request: Request): Endpoint | null {
  const key = `${request.method()} ${new URL(request.url()).pathname}`
  switch (key) {
    case 'GET /api/v1/auth/session':
      return 'session'
    case 'POST /api/v1/auth/session':
      return 'login'
    case 'DELETE /api/v1/auth/session':
      return 'logout'
    case 'GET /api/v1/system/status':
      return 'status'
    case 'GET /api/v1/nginx/effective-config':
      return 'effectiveConfig'
    default:
      return null
  }
}

function validateVisibleRequestContract(
  endpoint: Endpoint,
  headers: Readonly<Record<string, string>>,
  violations: string[],
): void {
  const csrf = headers['x-csrf-token']
  if (endpoint === 'login') {
    if (headers.origin !== appOrigin) {
      violations.push(`login Origin was ${String(headers.origin)}`)
    }
    if (headers['content-type'] !== 'application/json') {
      violations.push(`login Content-Type was ${String(headers['content-type'])}`)
    }
    if (csrf !== undefined) {
      violations.push('login unexpectedly sent a CSRF token')
    }
    return
  }
  if (endpoint === 'logout') {
    if (headers.origin !== appOrigin) {
      violations.push(`logout Origin was ${String(headers.origin)}`)
    }
    if (csrf !== csrfToken) {
      violations.push(`logout CSRF token was ${String(csrf)}`)
    }
    return
  }
  if (csrf !== undefined) {
    violations.push(`${endpoint} GET unexpectedly sent a CSRF token`)
  }
}

/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import AxeBuilder from '@axe-core/playwright'
import { expect, type BrowserContext, type Page, type Request, type Route } from '@playwright/test'

import type {
  ConfigDependency,
  ConfigFile,
  ConfigGroup,
  ConfigTreeNode,
  DiffResponse,
  FileMutationResponse,
  GroupCollection,
  GroupMutationRequest,
  SearchResponse,
  WorkspaceDetail,
  WorkspaceState,
} from '../../src/api/types'

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

export interface WorkspaceAPIRequest {
  method: string
  path: string
  query: string
  ifMatch: string | null
  csrf: string | null
  body: string | null
  headers: Readonly<Record<string, string>>
}

export interface WorkspaceAPIFixture {
  workspaceId: string
  currentDraftETag: () => string
  commitExternalDraftMutation: () => WorkspaceDetail
  currentGroupsETag: () => string
  forceConflict: () => void
  setWorkspaceState: (state: WorkspaceState) => void
  setAgentUnavailable: (unavailable?: boolean) => void
  expireSession: () => void
  requests: () => ReadonlyArray<WorkspaceAPIRequest>
  assertContract: () => void
}

export async function installWorkspaceAPIFixture(
  page: Page,
  options: { seedWorkspace?: boolean } = {},
): Promise<WorkspaceAPIFixture> {
  const workspaceId = '0123456789abcdef0123456789abcdef'
  const publishedReleaseId = '22222222222222222222222222222222'
  const groupId = 'fedcba9876543210fedcba9876543210'
  const baseFiles = new Map<string, string>([
    [
      'nginx.conf',
      `events {}\nhttp {\n  include conf.d/*.conf;\n  add_header X-E2E-Long "${'segment-'.repeat(100)}";\n}\n`,
    ],
    ['conf.d/site.conf', 'server {\n  listen 80;\n}\n'],
  ])
  const files = new Map(baseFiles)
  if (options.seedWorkspace === true) {
    files.set(
      'conf.d/site.conf',
      `server {\n  listen 8080;\n  add_header X-E2E-Seed "${'draft-segment-'.repeat(80)}";\n}\n`,
    )
  }
  const groups: ConfigGroup[] = []
  const captured: WorkspaceAPIRequest[] = []
  const violations: string[] = []
  let draftRevision = 1
  let groupsRevision = 1
  let workspaceState: WorkspaceState = 'ready'
  let workspaceName = 'E2E workspace'
  let workspaceExists = options.seedWorkspace ?? false
  let sessionExpired = false
  let agentUnavailable = false
  let conflictNext = false

  const draftETag = () => `"draft-v1:${revisionDigest(draftRevision)}"`
  const groupsETag = () => `"groups-v1:${revisionDigest(groupsRevision)}"`

  function workspace(): WorkspaceDetail {
    const managedBytes = [...files.values()].reduce(
      (total, content) => total + new TextEncoder().encode(content).length,
      0,
    )
    return {
      id: workspaceId,
      name: workspaceName,
      state: workspaceState,
      ...(workspaceState === 'ready' ? {} : { state_reason_code: `fixture_${workspaceState}` }),
      ...(workspaceState === 'published' ? { last_release_id: publishedReleaseId } : {}),
      production_digest: revisionDigest(100),
      base_digest: revisionDigest(101),
      draft_etag: draftETag(),
      entry_count: files.size + 1,
      managed_bytes: managedBytes,
      workspace_bytes: managedBytes * 2 + 4096,
      created_by: 1,
      created_at: '2026-07-17T08:00:00Z',
      updated_at: `2026-07-17T08:00:${String(draftRevision).padStart(2, '0')}Z`,
    }
  }

  function fileNode(path: string, content: string): ConfigTreeNode {
    const original = baseFiles.get(path)
    return {
      path,
      name: path.split('/').at(-1) ?? path,
      entry_type: 'regular',
      managed: true,
      read_only: false,
      status_reason_code: 'managed_text',
      size_bytes: new TextEncoder().encode(content).length,
      content_digest: contentDigest(content),
      diff_status: original === undefined ? 'created' : original === content ? 'unchanged' : 'modified',
    }
  }

  function treeEntries(): ConfigTreeNode[] {
    return [
      {
        path: 'conf.d',
        name: 'conf.d',
        entry_type: 'directory',
        managed: false,
        read_only: true,
        status_reason_code: 'directory',
      },
      ...[...files.entries()]
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([path, content]) => fileNode(path, content)),
    ]
  }

  const dependencies: ConfigDependency[] = [
    {
      source: 'nginx.conf',
      line: 3,
      column: 3,
      display_value: 'conf.d/*.conf',
      target: 'conf.d/site.conf',
      status: 'resolved',
      cycle: false,
    },
  ]

  await page.route(`${appOrigin}/api/v1/**`, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const headers = request.headers()
    captured.push({
      method: request.method(),
      path: url.pathname,
      query: url.search,
      ifMatch: headers['if-match'] ?? null,
      csrf: headers['x-csrf-token'] ?? null,
      body: request.postData(),
      headers,
    })

    if (url.origin !== appOrigin || url.hash !== '') {
      return rejectUnexpected(route, violations, `unexpected API URL: ${request.url()}`)
    }

    const isLogin = request.method() === 'POST' && url.pathname === '/api/v1/auth/session'
    if (!isLogin && !hasSessionCookie(headers.cookie)) {
      violations.push(`missing HttpOnly session cookie: ${request.method()} ${url.pathname}`)
      return fulfillError(route, 401, 'unauthenticated', 'Authentication required')
    }
    if (!isLogin && sessionExpired) {
      return fulfillError(route, 401, 'AUTH_SESSION_EXPIRED', 'Session expired')
    }

    if (url.pathname === '/api/v1/auth/session') {
      return handleWorkspaceSessionRoute(route, request, url, headers, violations, () => {
        sessionExpired = false
      })
    }
    if (agentUnavailable && url.pathname.startsWith('/api/v1/config/')) {
      return fulfillError(route, 503, 'AGENT_UNAVAILABLE', 'Configuration Agent is unavailable')
    }

    const handled = await handleConfigRoute(route, request, url, headers)
    if (!handled) {
      await rejectUnexpected(route, violations, `unexpected API request: ${request.method()} ${url.pathname}${url.search}`)
    }
  })

  async function handleConfigRoute(
    route: Route,
    request: Request,
    url: URL,
    headers: Readonly<Record<string, string>>,
  ): Promise<boolean> {
    const method = request.method()
    if (url.pathname === '/api/v1/config/workspaces') {
      if (method === 'GET' && requireQuery(url, [], violations)) {
        await fulfillJSON(route, 200, { workspaces: workspaceExists ? [workspace()] : [] })
        return true
      }
      if (method === 'POST') {
        if (!requireMutationHeaders(headers, violations, false)) return rejectHandled(route)
        const body = strictBody(request, ['name'], violations)
        if (body === null || typeof body.name !== 'string') return rejectHandled(route)
        workspaceName = body.name
        workspaceExists = true
        await fulfillJSON(route, 201, workspace(), { ETag: draftETag() })
        return true
      }
      return false
    }

    if (url.pathname === `/api/v1/config/workspaces/${workspaceId}`) {
      if (!workspaceExists) {
        await fulfillError(route, 404, 'CONFIG_WORKSPACE_NOT_FOUND', 'Workspace not found')
        return true
      }
      if (method === 'GET' && requireQuery(url, [], violations)) {
        await fulfillJSON(route, 200, workspace(), { ETag: draftETag() })
        return true
      }
      if (method === 'DELETE') {
        if (!(await requireDraftMutation(route, request, headers))) return true
        const body = strictBody(request, ['confirm_name'], violations)
        if (body === null || body.confirm_name !== workspaceName) return rejectHandled(route)
        workspaceExists = false
        await route.fulfill({ status: 204, headers: { 'Cache-Control': 'no-store' } })
        return true
      }
      return false
    }

    const filesPath = `/api/v1/config/workspaces/${workspaceId}/files`
    if (url.pathname === filesPath) {
      if (method === 'GET') {
        const query = exactOptionalQuery(url, 'path', violations)
        if (query === false) return rejectHandled(route)
        if (query === undefined) {
          await fulfillJSON(route, 200, { entries: treeEntries(), dependencies, draft_etag: draftETag() }, { ETag: draftETag() })
          return true
        }
        const content = files.get(query)
        if (content === undefined) {
          await fulfillError(route, 404, 'CONFIG_WORKSPACE_NOT_FOUND', 'File not found')
          return true
        }
        const result: ConfigFile = {
          path: query,
          content,
          size_bytes: new TextEncoder().encode(content).length,
          content_digest: contentDigest(content),
          line_ending: lineEnding(content),
          draft_etag: draftETag(),
        }
        await fulfillJSON(route, 200, result, { ETag: draftETag() })
        return true
      }
      if (method === 'POST') {
        if (!(await requireDraftMutation(route, request, headers))) return true
        const body = strictBody(request, ['path', 'content'], violations)
        if (body === null || typeof body.path !== 'string' || typeof body.content !== 'string' || files.has(body.path)) return rejectHandled(route)
        files.set(body.path, body.content)
        rotateDraft()
        await fulfillMutation(route, 201, fileNode(body.path, body.content))
        return true
      }
      const path = exactRequiredQuery(url, 'path', violations)
      if (path === null) return rejectHandled(route)
      if (!(await requireDraftMutation(route, request, headers))) return true
      if (method === 'PUT') {
        const body = strictBody(request, ['content'], violations)
        if (body === null || typeof body.content !== 'string' || !files.has(path)) return rejectHandled(route)
        files.set(path, body.content)
        rotateDraft()
        await fulfillMutation(route, 200, fileNode(path, body.content))
        return true
      }
      if (method === 'PATCH') {
        const body = strictBody(request, ['destination_path'], violations)
        const content = files.get(path)
        if (body === null || typeof body.destination_path !== 'string' || content === undefined || files.has(body.destination_path)) return rejectHandled(route)
        files.delete(path)
        files.set(body.destination_path, content)
        rotateDraft()
        await fulfillMutation(route, 200, fileNode(body.destination_path, content))
        return true
      }
      if (method === 'DELETE') {
        const body = strictBody(request, ['confirm_path'], violations)
        if (body === null || body.confirm_path !== path || !files.has(path)) return rejectHandled(route)
        files.delete(path)
        rotateDraft()
        await fulfillMutation(route, 200)
        return true
      }
      return false
    }

    if (url.pathname === `${filesPath}/copies` && method === 'POST') {
      if (!requireQuery(url, [], violations) || !(await requireDraftMutation(route, request, headers))) return true
      const body = strictBody(request, ['source_path', 'destination_path'], violations)
      const source = typeof body?.source_path === 'string' ? files.get(body.source_path) : undefined
      if (body === null || typeof body.destination_path !== 'string' || source === undefined || files.has(body.destination_path)) return rejectHandled(route)
      files.set(body.destination_path, source)
      rotateDraft()
      await fulfillMutation(route, 201, fileNode(body.destination_path, source))
      return true
    }

    if (url.pathname === `${filesPath}/search` && method === 'GET') {
      const query = exactRequiredQuery(url, 'query', violations)
      if (query === null) return rejectHandled(route)
      const matches: SearchResponse['matches'] = []
      for (const [path, content] of [...files].sort(([left], [right]) => left.localeCompare(right))) {
        for (const [index, line] of content.split('\n').entries()) {
          const column = line.indexOf(query)
          if (column >= 0) matches.push({ path, line: index + 1, column: column + 1, snippet: line.slice(0, 240) })
        }
      }
      await fulfillJSON(route, 200, { matches, complete: true } satisfies SearchResponse)
      return true
    }

    if (url.pathname === `/api/v1/config/workspaces/${workspaceId}/diff` && method === 'GET') {
      const selected = exactOptionalQuery(url, 'path', violations)
      if (selected === false) return rejectHandled(route)
      const allPaths = [...new Set([...baseFiles.keys(), ...files.keys()])].sort()
      const paths = selected === undefined ? allPaths : [selected]
      const summaries = paths.map((path) => {
        const before = baseFiles.get(path)
        const after = files.get(path)
        const status = before === undefined ? 'created' : after === undefined ? 'deleted' : before === after ? 'unchanged' : 'modified'
        return { path, status, added_lines: status === 'unchanged' ? 0 : 1, removed_lines: status === 'unchanged' || status === 'created' ? 0 : 1 }
      }) satisfies DiffResponse['files']
      await fulfillJSON(route, 200, {
        files: summaries,
        complete: true,
        reason: '',
        patch: summaries.some(({ status }) => status !== 'unchanged')
          ? `@@ fixture diff @@\n-${'before-'.repeat(80)}\n+${'after-'.repeat(80)}\n`
          : '',
      } satisfies DiffResponse)
      return true
    }

    if (url.pathname === '/api/v1/config/groups') {
      if (method === 'GET') {
        const selected = exactOptionalQuery(url, 'workspace_id', violations)
        if (selected === false || (selected !== undefined && selected !== workspaceId)) return rejectHandled(route)
        await fulfillGroups(route, 200)
        return true
      }
      if (method === 'POST') {
        if (!requireMutationHeaders(headers, violations, true)) return rejectHandled(route)
        const input = strictGroupBody(request, violations)
        if (input === null) return rejectHandled(route)
        groups.splice(0, groups.length, groupFromInput(input, groupId))
        groupsRevision += 1
        await fulfillGroups(route, 201)
        return true
      }
      return false
    }

    if (url.pathname === `/api/v1/config/groups/${groupId}`) {
      if (!requireMutationHeaders(headers, violations, true)) return rejectHandled(route)
      if (method === 'PUT') {
        const input = strictGroupBody(request, violations)
        if (input === null || groups.length === 0) return rejectHandled(route)
        groups.splice(0, groups.length, groupFromInput(input, groupId))
        groupsRevision += 1
        await fulfillGroups(route, 200)
        return true
      }
      if (method === 'DELETE') {
        const body = strictBody(request, ['confirm_name'], violations)
        if (body === null || body.confirm_name !== groups[0]?.name) return rejectHandled(route)
        groups.splice(0)
        groupsRevision += 1
        await fulfillGroups(route, 200)
        return true
      }
      return false
    }
    return false
  }

  async function requireDraftMutation(
    route: Route,
    request: Request,
    headers: Readonly<Record<string, string>>,
  ): Promise<boolean> {
    if (!requireMutationHeaders(headers, violations, false)) {
      await rejectHandled(route)
      return false
    }
    if (workspaceState === 'stale') {
      await fulfillError(route, 409, 'CONFIG_WORKSPACE_STALE', 'Workspace is stale')
      return false
    }
    if (workspaceState === 'needs_attention') {
      await fulfillError(route, 409, 'CONFIG_WORKSPACE_NEEDS_ATTENTION', 'Workspace needs attention')
      return false
    }
    if (conflictNext) {
      conflictNext = false
      rotateDraft()
      await fulfillError(route, 409, 'CONFIG_WORKSPACE_CONFLICT', 'Workspace changed', { current_etag: draftETag() })
      return false
    }
    if (request.headers()['if-match'] !== draftETag()) {
      await fulfillError(route, 409, 'CONFIG_WORKSPACE_CONFLICT', 'Workspace changed', { current_etag: draftETag() })
      return false
    }
    return true
  }

  function requireMutationHeaders(
    headers: Readonly<Record<string, string>>,
    errors: string[],
    groupMutation: boolean,
  ): boolean {
    let valid = true
    if (headers.origin !== appOrigin) {
      errors.push(`mutation Origin was ${String(headers.origin)}`)
      valid = false
    }
    if (headers['content-type'] !== 'application/json') {
      errors.push(`mutation Content-Type was ${String(headers['content-type'])}`)
      valid = false
    }
    if (headers['x-csrf-token'] !== csrfToken) {
      errors.push(`mutation CSRF token was ${String(headers['x-csrf-token'])}`)
      valid = false
    }
    const expected = groupMutation ? groupsETag() : undefined
    if (groupMutation && headers['if-match'] !== expected) {
      errors.push(`group If-Match was ${String(headers['if-match'])}, expected ${expected}`)
      valid = false
    }
    return valid
  }

  function rotateDraft(): void {
    draftRevision += 1
  }

  async function fulfillMutation(route: Route, status: number, entry?: ConfigTreeNode): Promise<void> {
    const result: FileMutationResponse = { workspace: workspace(), ...(entry === undefined ? {} : { entry }), draft_etag: draftETag() }
    await fulfillJSON(route, status, result, { ETag: draftETag() })
  }

  async function fulfillGroups(route: Route, status: number): Promise<void> {
    const result: GroupCollection = { groups: [...groups], groups_etag: groupsETag() }
    await fulfillJSON(route, status, result, { ETag: groupsETag() })
  }

  return {
    workspaceId,
    currentDraftETag: draftETag,
    commitExternalDraftMutation: () => {
      rotateDraft()
      return workspace()
    },
    currentGroupsETag: groupsETag,
    forceConflict: () => {
      conflictNext = true
    },
    setWorkspaceState: (state) => {
      workspaceState = state
    },
    setAgentUnavailable: (unavailable = true) => {
      agentUnavailable = unavailable
    },
    expireSession: () => {
      sessionExpired = true
    },
    requests: () => captured,
    assertContract: () => expect(violations, 'workspace API contract violations').toEqual([]),
  }
}

const workspaceSession = {
  user: { id: 1, username: 'admin', created_at: '2026-07-17T08:00:00Z' },
  csrf_token: csrfToken,
  created_at: '2026-07-17T08:00:00Z',
  last_seen_at: '2026-07-17T08:01:00Z',
  idle_expires_at: '2026-07-17T08:31:00Z',
  absolute_expires_at: '2026-07-18T08:00:00Z',
}

async function handleWorkspaceSessionRoute(
  route: Route,
  request: Request,
  url: URL,
  headers: Readonly<Record<string, string>>,
  violations: string[],
  restoreSession: () => void,
): Promise<void> {
  if (!requireQuery(url, [], violations)) {
    await rejectHandled(route)
    return
  }
  if (request.method() === 'GET') {
    await fulfillJSON(route, 200, workspaceSession)
    return
  }
  if (request.method() === 'POST') {
    if (headers.origin !== appOrigin || headers['content-type'] !== 'application/json' || headers['x-csrf-token'] !== undefined) {
      violations.push('login headers did not match the public contract')
      await rejectHandled(route)
      return
    }
    const body = strictBody(request, ['username', 'password'], violations)
    if (body === null || typeof body.username !== 'string' || typeof body.password !== 'string') {
      await rejectHandled(route)
      return
    }
    restoreSession()
    await fulfillJSON(route, 200, workspaceSession)
    return
  }
  if (request.method() === 'DELETE') {
    if (headers.origin !== appOrigin || headers['x-csrf-token'] !== csrfToken) {
      violations.push('logout headers did not match the public contract')
      await rejectHandled(route)
      return
    }
    await route.fulfill({ status: 204, headers: { 'Cache-Control': 'no-store' } })
    return
  }
  await rejectUnexpected(route, violations, `unexpected session method: ${request.method()}`)
}

function hasSessionCookie(cookie: string | undefined): boolean {
  return cookie
    ?.split(';')
    .map((part) => part.trim())
    .includes(`${sessionCookieName}=${sessionCookieValue}`) === true
}

function revisionDigest(revision: number): string {
  return revision.toString(16).padStart(64, '0')
}

function contentDigest(content: string): string {
  let value = 0
  for (const byte of new TextEncoder().encode(content)) {
    value = (value * 33 + byte) % 0x7fffffff
  }
  return revisionDigest(value)
}

function lineEnding(content: string): ConfigFile['line_ending'] {
  const hasCRLF = content.includes('\r\n')
  const rest = content.replaceAll('\r\n', '')
  const hasLF = rest.includes('\n')
  const hasCR = rest.includes('\r')
  if (!hasCRLF && !hasLF && !hasCR) return 'none'
  if (hasCRLF && !hasLF && !hasCR) return 'crlf'
  if (!hasCRLF && hasLF && !hasCR) return 'lf'
  return 'mixed'
}

function strictBody(
  request: Request,
  keys: readonly string[],
  violations: string[],
): Record<string, unknown> | null {
  const raw = request.postData()
  if (raw === null) {
    violations.push(`missing JSON body: ${request.method()} ${request.url()}`)
    return null
  }
  try {
    const value: unknown = JSON.parse(raw)
    if (typeof value !== 'object' || value === null || Array.isArray(value)) {
      violations.push(`non-object JSON body: ${request.method()} ${request.url()}`)
      return null
    }
    const record = value as Record<string, unknown>
    const actual = Object.keys(record).sort()
    const expected = [...keys].sort()
    if (JSON.stringify(actual) !== JSON.stringify(expected)) {
      violations.push(`unexpected JSON keys: ${actual.join(',')}; expected ${expected.join(',')}`)
      return null
    }
    return record
  } catch {
    violations.push(`invalid JSON body: ${request.method()} ${request.url()}`)
    return null
  }
}

function strictGroupBody(request: Request, violations: string[]): GroupMutationRequest | null {
  const body = strictBody(request, ['name', 'sort_order', 'members'], violations)
  if (
    body === null ||
    typeof body.name !== 'string' ||
    !Number.isSafeInteger(body.sort_order) ||
    !Array.isArray(body.members) ||
    !body.members.every((member) => typeof member === 'string') ||
    new Set(body.members).size !== body.members.length
  ) {
    violations.push('invalid group mutation body')
    return null
  }
  return { name: body.name, sort_order: body.sort_order as number, members: [...body.members] }
}

function groupFromInput(input: GroupMutationRequest, id: string): ConfigGroup {
  return {
    id,
    name: input.name,
    sort_order: input.sort_order,
    members: [...input.members],
    missing: [],
    created_by: 1,
    created_at: '2026-07-17T08:00:00Z',
    updated_at: '2026-07-17T08:01:00Z',
  }
}

function requireQuery(url: URL, keys: readonly string[], violations: string[]): boolean {
  const actual = [...url.searchParams.keys()].sort()
  const expected = [...keys].sort()
  const valid =
    JSON.stringify(actual) === JSON.stringify(expected) &&
    keys.every((key) => url.searchParams.getAll(key).length === 1)
  if (!valid) {
    violations.push(`unexpected query ${url.search}; expected ${expected.join(',')}`)
  }
  return valid
}

function exactOptionalQuery(
  url: URL,
  key: string,
  violations: string[],
): string | undefined | false {
  if (url.search === '') return undefined
  if (!requireQuery(url, [key], violations)) return false
  return url.searchParams.get(key) ?? false
}

function exactRequiredQuery(url: URL, key: string, violations: string[]): string | null {
  if (!requireQuery(url, [key], violations)) return null
  const value = url.searchParams.get(key)
  if (value === null || value === '') {
    violations.push(`empty required query: ${key}`)
    return null
  }
  return value
}

async function fulfillJSON(
  route: Route,
  status: number,
  body: unknown,
  headers: Readonly<Record<string, string>> = {},
): Promise<void> {
  await route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
    headers: { 'Cache-Control': 'no-store', 'X-Request-ID': 'request-workspace-e2e', ...headers },
  })
}

async function fulfillError(
  route: Route,
  status: number,
  code: string,
  message: string,
  details?: Readonly<Record<string, unknown>>,
): Promise<void> {
  await fulfillJSON(route, status, apiError(code, message, 'request-workspace-e2e', details))
}

async function rejectUnexpected(route: Route, violations: string[], message: string): Promise<void> {
  violations.push(message)
  await fulfillError(route, 500, 'internal_error', 'Unexpected fixture request')
}

async function rejectHandled(route: Route): Promise<false> {
  await fulfillError(route, 400, 'invalid_request', 'Fixture rejected request')
  return false
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

export async function assertOnlyLocalePreferenceStorage(page: Page): Promise<void> {
  const storage = await page.evaluate(async () => ({
    localStorage: Object.fromEntries(Object.entries(localStorage)),
    sessionStorage: Object.keys(sessionStorage),
    cacheStorage: await caches.keys(),
    indexedDB: (await indexedDB.databases()).map((database) => database.name ?? ''),
  }))
  expect(storage).toMatchObject({
    sessionStorage: [],
    cacheStorage: [],
    indexedDB: [],
  })
  expect(Object.keys(storage.localStorage)).toEqual(['nginx-uix.locale'])
  expect(['zh-CN', 'en-US']).toContain(storage.localStorage['nginx-uix.locale'])
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

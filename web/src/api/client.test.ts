/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { APIClient, APIRequestError } from './client'

const sessionPayload = {
  user: { id: 7, username: 'operator', created_at: '2026-07-14T11:00:00Z' },
  csrf_token: 'csrf-token',
  created_at: '2026-07-14T12:00:00Z',
  last_seen_at: '2026-07-14T12:30:00Z',
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
}

const workspaceID = '0123456789abcdef0123456789abcdef'
const groupID = 'fedcba9876543210fedcba9876543210'
const digestA = 'a'.repeat(64)
const digestB = 'b'.repeat(64)
const draftETagA = `"draft-v1:${digestA}"`
const draftETagB = `"draft-v1:${digestB}"`
const groupsETagA = `"groups-v1:${digestA}"`
const groupsETagB = `"groups-v1:${digestB}"`
const checkID = '11111111111111111111111111111111'
const releaseID = '22222222222222222222222222222222'
const backupID = '33333333333333333333333333333333'

function workspaceFixture(digest = digestA) {
  return {
    id: workspaceID,
    name: 'Primary workspace',
    state: 'ready',
    production_digest: digestA,
    base_digest: digestA,
    draft_etag: `"draft-v1:${digest}"`,
    entry_count: 2,
    managed_bytes: 128,
    workspace_bytes: 512,
    created_by: 7,
    created_at: '2026-07-17T08:00:00Z',
    updated_at: '2026-07-17T08:01:00Z',
  }
}

function treeNodeFixture() {
  return {
    path: 'conf.d/site.conf',
    name: 'site.conf',
    entry_type: 'regular',
    managed: true,
    read_only: false,
    status_reason_code: 'managed_text',
    size_bytes: 10,
    content_digest: digestA,
    diff_status: 'modified',
    dependency_status: 'resolved',
    dependency_target_count: 1,
    dependency_cycle: false,
  }
}

function treeFixture(digest = digestA) {
  return {
    entries: [treeNodeFixture()],
    dependencies: [
      {
        source: 'nginx.conf',
        line: 4,
        column: 3,
        display_value: 'conf.d/*.conf',
        target: 'conf.d/site.conf',
        status: 'resolved',
        cycle: false,
      },
    ],
    draft_etag: `"draft-v1:${digest}"`,
  }
}

function configFileFixture(digest = digestA) {
  return {
    path: 'conf.d/site.conf',
    content: 'server {}\n',
    size_bytes: 10,
    content_digest: digestA,
    line_ending: 'lf',
    draft_etag: `"draft-v1:${digest}"`,
  }
}

function fileMutationFixture(digest = digestB) {
  return {
    workspace: workspaceFixture(digest),
    entry: treeNodeFixture(),
    draft_etag: `"draft-v1:${digest}"`,
  }
}

function groupCollectionFixture(digest = digestA) {
  return {
    groups: [
      {
        id: groupID,
        name: 'Sites',
        sort_order: 1,
        members: ['conf.d/site.conf'],
        missing: [],
        created_by: 7,
        created_at: '2026-07-17T08:00:00Z',
        updated_at: '2026-07-17T08:01:00Z',
      },
    ],
    groups_etag: `"groups-v1:${digest}"`,
  }
}

function publishCheckFixture(state: 'valid' | 'invalid' = 'valid') {
  return {
    id: checkID,
    workspace_id: workspaceID,
    workspace_revision: 2,
    production_digest: digestA,
    base_digest: digestA,
    draft_digest: digestB,
    candidate_digest: state === 'valid' ? digestB : '0'.repeat(64),
    manifest_version: 1,
    policy_version: 1,
    validator_version: 1,
    validator_build_id: 'build-id',
    state,
    diagnostic_count: state === 'valid' ? 0 : 1,
    details: {
      diagnostics:
        state === 'valid'
          ? []
          : [{ code: 'syntax_error', path: 'conf.d/site.conf', line: 4, summary: '配置语法无效' }],
    },
    started_at: '2026-07-18T04:00:00Z',
    finished_at: '2026-07-18T04:00:01Z',
    expires_at: '2026-07-18T04:10:01Z',
  }
}

function releaseFixture(state: 'queued' | 'succeeded' = 'queued') {
  const terminal = state === 'succeeded'
  return {
    id: releaseID,
    workspace_id: workspaceID,
    check_id: checkID,
    backup_id: terminal ? backupID : undefined,
    state,
    stage: terminal ? 'committed' : 'queued',
    production_digest: digestA,
    draft_digest: digestB,
    candidate_digest: digestB,
    created_at: '2026-07-18T04:01:00Z',
    updated_at: terminal ? '2026-07-18T04:01:10Z' : '2026-07-18T04:01:00Z',
    finished_at: terminal ? '2026-07-18T04:01:10Z' : undefined,
    stages: terminal
      ? [
          {
            sequence: 1,
            stage: 'queued',
            result: 'pending',
            details: {},
            occurred_at: '2026-07-18T04:01:00Z',
          },
          {
            sequence: 2,
            stage: 'committed',
            result: 'success',
            details: {},
            occurred_at: '2026-07-18T04:01:10Z',
          },
        ]
      : [],
  }
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
  it('rejects a manually constructed API error with an unknown stable code', () => {
    expect(
      () =>
        new APIRequestError({
          kind: 'api',
          message: 'unknown API code',
          status: 500,
          apiError: { code: 'UNKNOWN_UPSTREAM', message: 'unknown API code', request_id: 'request-0' },
        }),
    ).toThrow(TypeError)
  })

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

  it.each([
    ['missing session creation time', { ...sessionPayload, created_at: undefined }],
    ['invalid session creation time', { ...sessionPayload, created_at: 'yesterday' }],
    ['invalid session last-seen time', { ...sessionPayload, last_seen_at: 'recently' }],
    [
      'missing user creation time',
      { ...sessionPayload, user: { id: sessionPayload.user.id, username: sessionPayload.user.username } },
    ],
    ['invalid user creation time', { ...sessionPayload, user: { ...sessionPayload.user, created_at: 'earlier' } }],
  ])('rejects a session with %s', async (_name, payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))

    await expect(new APIClient(fetchMock).getSession()).rejects.toMatchObject({
      kind: 'malformed_response',
      status: 200,
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

describe('configuration APIClient surface', () => {
	 it('creates valid and invalid persisted publish checks with exact mutation headers', async () => {
		const fetchMock = vi
			.fn<typeof fetch>()
			.mockResolvedValueOnce(jsonResponse(publishCheckFixture(), 201))
			.mockResolvedValueOnce(jsonResponse(publishCheckFixture('invalid'), 422))
		const client = new APIClient(fetchMock)

		await expect(client.createPublishCheck(workspaceID, draftETagB, 'csrf-1')).resolves.toEqual(
			publishCheckFixture(),
		)
		await expect(client.createPublishCheck(workspaceID, draftETagB, 'csrf-1')).resolves.toEqual(
			publishCheckFixture('invalid'),
		)
		const [url, init] = requestAt(fetchMock)
		const headers = new Headers(init.headers)
		expect(url).toBe(`/api/v1/config/workspaces/${workspaceID}/publish-checks`)
		expect(init.method).toBe('POST')
		expect(headers.get('If-Match')).toBe(draftETagB)
		expect(headers.get('X-CSRF-Token')).toBe('csrf-1')
		expect(init.body).toBe('{}')
	})

	it('queues and reads one release with strict stage parsing', async () => {
		const queued = releaseFixture()
		const succeeded = releaseFixture('succeeded')
		const fetchMock = vi
			.fn<typeof fetch>()
			.mockResolvedValueOnce(
				jsonResponse(queued, 202, { Location: `/api/v1/config/releases/${releaseID}` }),
			)
			.mockResolvedValueOnce(jsonResponse(succeeded))
		const client = new APIClient(fetchMock)

		await expect(
			client.createRelease(workspaceID, checkID, 'Primary workspace', draftETagB, 'csrf-1'),
		).resolves.toEqual(queued)
		await expect(client.getRelease(releaseID)).resolves.toEqual(succeeded)
		const [url, init] = requestAt(fetchMock)
		expect(url).toBe(`/api/v1/config/workspaces/${workspaceID}/releases`)
		expect(init.body).toBe(
			`{"check_id":"${checkID}","confirm_name":"Primary workspace"}`,
		)
		expect(requestAt(fetchMock, 1)[0]).toBe(`/api/v1/config/releases/${releaseID}`)
	})

  it('lists workspaces from the closed workspace-list envelope', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ workspaces: [workspaceFixture()] }),
    )
    const client = new APIClient(fetchMock)
    const controller = new AbortController()

    await expect(client.listWorkspaces(controller.signal)).resolves.toEqual([workspaceFixture()])

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe('/api/v1/config/workspaces')
    expect(init.method).toBe('GET')
    expect(init.signal).toBe(controller.signal)
    expect(init.credentials).toBe('same-origin')
    expect(init.cache).toBe('no-store')
  })

  it('creates a workspace with CSRF and returns the matching response ETag', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(workspaceFixture(), 201, { ETag: draftETagA }),
    )
    const client = new APIClient(fetchMock)
    const controller = new AbortController()

    await expect(client.createWorkspace('Primary workspace', 'csrf-1', controller.signal)).resolves.toEqual(
      workspaceFixture(),
    )

    const [url, init] = requestAt(fetchMock)
    const headers = new Headers(init.headers)
    expect(url).toBe('/api/v1/config/workspaces')
    expect(init.method).toBe('POST')
    expect(init.signal).toBe(controller.signal)
    expect(headers.get('Content-Type')).toBe('application/json')
    expect(headers.get('X-CSRF-Token')).toBe('csrf-1')
    expect(headers.has('If-Match')).toBe(false)
    expect(init.body).toBe('{"name":"Primary workspace"}')
  })

  it('gets one workspace by its opaque ID', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(workspaceFixture(), 200, { ETag: draftETagA }),
    )
    const client = new APIClient(fetchMock)

    await expect(client.getWorkspace(workspaceID)).resolves.toEqual(workspaceFixture())

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe(`/api/v1/config/workspaces/${workspaceID}`)
    expect(init.method).toBe('GET')
  })

  it('deletes a named workspace with one strong If-Match and CSRF token', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 204 }))
    const client = new APIClient(fetchMock)

    await expect(
      client.deleteWorkspace(workspaceID, 'Primary workspace', draftETagA, 'csrf-1'),
    ).resolves.toBeUndefined()

    const [url, init] = requestAt(fetchMock)
    const headers = new Headers(init.headers)
    expect(url).toBe(`/api/v1/config/workspaces/${workspaceID}`)
    expect(init.method).toBe('DELETE')
    expect(headers.get('If-Match')).toBe(draftETagA)
    expect(headers.get('X-CSRF-Token')).toBe('csrf-1')
    expect(init.body).toBe('{"confirm_name":"Primary workspace"}')
  })

  it('gets the configuration tree and verifies its response ETag', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(treeFixture(), 200, { ETag: draftETagA }),
    )
    const client = new APIClient(fetchMock)

    await expect(client.getConfigTree(workspaceID)).resolves.toEqual(treeFixture())

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe(`/api/v1/config/workspaces/${workspaceID}/files`)
    expect(init.method).toBe('GET')
  })

  it('gets one config file using an encoded relative-path query', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(configFileFixture(), 200, { ETag: draftETagA }),
    )
    const client = new APIClient(fetchMock)

    await expect(client.getConfigFile(workspaceID, 'conf.d/site name.conf')).resolves.toEqual(
      configFileFixture(),
    )

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe(`/api/v1/config/workspaces/${workspaceID}/files?path=conf.d%2Fsite+name.conf`)
    expect(init.method).toBe('GET')
  })

  it('creates one config file with JSON, CSRF and a strong If-Match', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(fileMutationFixture(), 201, { ETag: draftETagB }),
    )
    const client = new APIClient(fetchMock)

    await expect(
      client.createConfigFile(workspaceID, 'conf.d/new.conf', 'server {}\n', draftETagA, 'csrf-1'),
    ).resolves.toEqual(fileMutationFixture())

    const [url, init] = requestAt(fetchMock)
    const headers = new Headers(init.headers)
    expect(url).toBe(`/api/v1/config/workspaces/${workspaceID}/files`)
    expect(init.method).toBe('POST')
    expect(headers.get('If-Match')).toBe(draftETagA)
    expect(headers.get('X-CSRF-Token')).toBe('csrf-1')
    expect(init.body).toBe('{"path":"conf.d/new.conf","content":"server {}\\n"}')
  })

  it('sends CSRF and one strong If-Match then returns the response ETag', async () => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      expect(init?.method).toBe('PUT')
      expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('csrf-1')
      expect(new Headers(init?.headers).get('If-Match')).toBe(draftETagA)
      return jsonResponse(fileMutationFixture(), 200, { ETag: draftETagB })
    })
    const client = new APIClient(fetcher)

    const result = await client.replaceConfigFile(
      workspaceID,
      'conf.d/site.conf',
      'server {}\n',
      draftETagA,
      'csrf-1',
    )

    expect(result.draft_etag).toBe(draftETagB)
    expect(requestAt(fetcher)[0]).toBe(
      `/api/v1/config/workspaces/${workspaceID}/files?path=conf.d%2Fsite.conf`,
    )
  })

  it('copies a config file using exact source and destination fields', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(fileMutationFixture(), 201, { ETag: draftETagB }),
    )
    const client = new APIClient(fetchMock)

    await client.copyConfigFile(
      workspaceID,
      'conf.d/site.conf',
      'conf.d/site-copy.conf',
      draftETagA,
      'csrf-1',
    )

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe(`/api/v1/config/workspaces/${workspaceID}/files/copies`)
    expect(init.method).toBe('POST')
    expect(init.body).toBe(
      '{"source_path":"conf.d/site.conf","destination_path":"conf.d/site-copy.conf"}',
    )
  })

  it('renames a config file selected by an encoded path query', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(fileMutationFixture(), 200, { ETag: draftETagB }),
    )
    const client = new APIClient(fetchMock)

    await client.renameConfigFile(
      workspaceID,
      'conf.d/site old.conf',
      'conf.d/site.conf',
      draftETagA,
      'csrf-1',
    )

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe(
      `/api/v1/config/workspaces/${workspaceID}/files?path=conf.d%2Fsite+old.conf`,
    )
    expect(init.method).toBe('PATCH')
    expect(init.body).toBe('{"destination_path":"conf.d/site.conf"}')
  })

  it('deletes one config file only after exact path confirmation', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ ...fileMutationFixture(), entry: undefined }, 200, { ETag: draftETagB }),
    )
    const client = new APIClient(fetchMock)

    await client.deleteConfigFile(
      workspaceID,
      'conf.d/site.conf',
      'conf.d/site.conf',
      draftETagA,
      'csrf-1',
    )

    const [, init] = requestAt(fetchMock)
    expect(init.method).toBe('DELETE')
    expect(init.body).toBe('{"confirm_path":"conf.d/site.conf"}')
  })

  it('searches config files with an encoded literal query and AbortSignal', async () => {
    const payload = {
      matches: [{ path: 'conf.d/site.conf', line: 1, column: 8, snippet: 'server name' }],
      complete: true,
    }
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))
    const client = new APIClient(fetchMock)
    const controller = new AbortController()

    await expect(client.searchConfigFiles(workspaceID, 'server name/值', controller.signal)).resolves.toEqual(
      payload,
    )

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe(
      `/api/v1/config/workspaces/${workspaceID}/files/search?query=server+name%2F%E5%80%BC`,
    )
    expect(init.signal).toBe(controller.signal)
  })

  it('gets a single-file diff with an encoded optional path', async () => {
    const payload = {
      files: [{ path: 'conf.d/site.conf', status: 'modified', added_lines: 1, removed_lines: 1 }],
      complete: true,
      reason: '',
      patch: '@@ -1 +1 @@\n',
    }
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))
    const client = new APIClient(fetchMock)

    await expect(client.getConfigDiff(workspaceID, 'conf.d/site.conf')).resolves.toEqual(payload)

    expect(requestAt(fetchMock)[0]).toBe(
      `/api/v1/config/workspaces/${workspaceID}/diff?path=conf.d%2Fsite.conf`,
    )
  })

  it('lists groups for an encoded optional workspace view and verifies the ETag', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(groupCollectionFixture(), 200, { ETag: groupsETagA }),
    )
    const client = new APIClient(fetchMock)

    await expect(client.listConfigGroups(workspaceID)).resolves.toEqual(groupCollectionFixture())

    expect(requestAt(fetchMock)[0]).toBe(`/api/v1/config/groups?workspace_id=${workspaceID}`)
  })

  it('creates a logical group using the group collection ETag', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(groupCollectionFixture(digestB), 201, { ETag: groupsETagB }),
    )
    const client = new APIClient(fetchMock)
    const input = { name: 'Sites', sort_order: 1, members: ['conf.d/site.conf'] }

    await expect(client.createConfigGroup(input, groupsETagA, 'csrf-1')).resolves.toEqual(
      groupCollectionFixture(digestB),
    )

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe('/api/v1/config/groups')
    expect(init.method).toBe('POST')
    expect(new Headers(init.headers).get('If-Match')).toBe(groupsETagA)
    expect(init.body).toBe(JSON.stringify(input))
  })

  it('replaces a logical group by its opaque ID', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(groupCollectionFixture(digestB), 200, { ETag: groupsETagB }),
    )
    const client = new APIClient(fetchMock)
    const input = { name: 'Sites', sort_order: 2, members: [] }

    await client.replaceConfigGroup(groupID, input, groupsETagA, 'csrf-1')

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe(`/api/v1/config/groups/${groupID}`)
    expect(init.method).toBe('PUT')
    expect(init.body).toBe(JSON.stringify(input))
  })

  it('deletes a logical group only after exact name confirmation', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ groups: [], groups_etag: groupsETagB }, 200, { ETag: groupsETagB }),
    )
    const client = new APIClient(fetchMock)

    await client.deleteConfigGroup(groupID, 'Sites', groupsETagA, 'csrf-1')

    const [url, init] = requestAt(fetchMock)
    expect(url).toBe(`/api/v1/config/groups/${groupID}`)
    expect(init.method).toBe('DELETE')
    expect(init.body).toBe('{"confirm_name":"Sites"}')
  })
})

describe('configuration API runtime validation', () => {
  it.each([
    ['workspace ID', { ...workspaceFixture(), id: 'not-an-id' }],
    ['workspace digest', { ...workspaceFixture(), production_digest: digestA.toUpperCase() }],
    ['workspace ETag', { ...workspaceFixture(), draft_etag: `draft-v1:${digestA}` }],
    ['workspace timestamp', { ...workspaceFixture(), updated_at: 'yesterday' }],
    ['workspace state', { ...workspaceFixture(), state: 'archived' }],
    ['workspace extra property', { ...workspaceFixture(), internal_path: '/secret' }],
  ])('rejects a malformed %s', async (_name, workspace) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ workspaces: [workspace] }),
    )

    await expect(new APIClient(fetchMock).listWorkspaces()).rejects.toMatchObject({
      kind: 'malformed_response',
      status: 200,
    })
  })

  it.each([
    ['null workspace array item', { workspaces: [null] }],
    ['unknown workspace-list property', { workspaces: [], cursor: 'next' }],
    ['too many workspaces', { workspaces: Array.from({ length: 9 }, () => workspaceFixture()) }],
  ])('rejects %s', async (_name, payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))

    await expect(new APIClient(fetchMock).listWorkspaces()).rejects.toMatchObject({
      kind: 'malformed_response',
    })
  })

  it.each([
    ['unknown node state', { ...treeFixture(), entries: [{ ...treeNodeFixture(), diff_status: 'renamed' }] }],
    ['null node', { ...treeFixture(), entries: [null] }],
    ['invalid node digest', { ...treeFixture(), entries: [{ ...treeNodeFixture(), content_digest: 'abc' }] }],
    ['extra node property', { ...treeFixture(), entries: [{ ...treeNodeFixture(), mode: 0o644 }] }],
    ['unknown dependency state', { ...treeFixture(), dependencies: [{ ...treeFixture().dependencies[0], status: 'loaded' }] }],
    ['null dependency', { ...treeFixture(), dependencies: [null] }],
  ])('rejects a tree with %s', async (_name, payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(payload, 200, { ETag: draftETagA }),
    )

    await expect(new APIClient(fetchMock).getConfigTree(workspaceID)).rejects.toMatchObject({
      kind: 'malformed_response',
    })
  })

  it.each([
    ['search null match', { matches: [null], complete: true }],
    ['search zero line', { matches: [{ path: 'a.conf', line: 0, column: 1, snippet: 'x' }], complete: true }],
    ['search extra property', { matches: [], complete: true, query: 'secret' }],
  ])('rejects malformed %s', async (_name, payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))

    await expect(new APIClient(fetchMock).searchConfigFiles(workspaceID, 'x')).rejects.toMatchObject({
      kind: 'malformed_response',
    })
  })

  it.each([
    ['diff null summary', { files: [null], complete: true, reason: '', patch: '' }],
    [
      'diff unknown state',
      {
        files: [{ path: 'a.conf', status: 'renamed', added_lines: 1, removed_lines: 1 }],
        complete: true,
        reason: '',
        patch: '',
      },
    ],
    ['diff unknown reason', { files: [], complete: false, reason: 'truncated', patch: '' }],
    ['diff extra property', { files: [], complete: true, reason: '', patch: '', digest: digestA }],
  ])('rejects malformed %s', async (_name, payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))

    await expect(new APIClient(fetchMock).getConfigDiff(workspaceID)).rejects.toMatchObject({
      kind: 'malformed_response',
    })
  })

  it.each([
    ['group ID', { ...groupCollectionFixture(), groups: [{ ...groupCollectionFixture().groups[0], id: 'bad' }] }],
    ['group time', { ...groupCollectionFixture(), groups: [{ ...groupCollectionFixture().groups[0], created_at: 'bad' }] }],
    ['duplicate members', { ...groupCollectionFixture(), groups: [{ ...groupCollectionFixture().groups[0], members: ['a.conf', 'a.conf'] }] }],
    ['null group', { ...groupCollectionFixture(), groups: [null] }],
    ['group extra property', { ...groupCollectionFixture(), revision: 1 }],
  ])('rejects malformed %s', async (_name, payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse(payload, 200, { ETag: groupsETagA }),
    )

    await expect(new APIClient(fetchMock).listConfigGroups()).rejects.toMatchObject({
      kind: 'malformed_response',
    })
  })

  it.each([
    [
      'workspace',
      () =>
        new APIClient(
          vi.fn<typeof fetch>().mockResolvedValue(
            jsonResponse(workspaceFixture(), 200, { ETag: draftETagB }),
          ),
        ).getWorkspace(workspaceID),
    ],
    [
      'file mutation',
      () =>
        new APIClient(
          vi.fn<typeof fetch>().mockResolvedValue(
            jsonResponse(fileMutationFixture(), 200, { ETag: draftETagA }),
          ),
        ).replaceConfigFile(workspaceID, 'a.conf', 'x', draftETagA, 'csrf-1'),
    ],
    [
      'group collection',
      () =>
        new APIClient(
          vi.fn<typeof fetch>().mockResolvedValue(
            jsonResponse(groupCollectionFixture(), 200, { ETag: groupsETagB }),
          ),
        ).listConfigGroups(),
    ],
  ])('rejects a %s header/DTO ETag mismatch', async (_name, request) => {
    await expect(request()).rejects.toMatchObject({ kind: 'malformed_response' })
  })

  const errorCodes = [
    'invalid_request',
    'invalid_credentials',
    'unauthenticated',
    'origin_rejected',
    'csrf_rejected',
    'rate_limited',
    'unsupported_media_type',
    'service_unavailable',
    'internal_error',
    'AUTH_SESSION_EXPIRED',
    'NGINX_CONFIG_INVALID',
    'NGINX_COMMAND_TIMEOUT',
    'NGINX_OUTPUT_TOO_LARGE',
    'CONFIG_PATH_INVALID',
    'CONFIG_ENTRY_NOT_MANAGED',
    'CONFIG_LIMIT_EXCEEDED',
    'CONFIG_WORKSPACE_NOT_FOUND',
    'CONFIG_PUBLISH_CHECK_NOT_FOUND',
    'CONFIG_RELEASE_NOT_FOUND',
    'CONFIG_WORKSPACE_CONFLICT',
    'CONFIG_WORKSPACE_STALE',
    'CONFIG_WORKSPACE_NEEDS_ATTENTION',
    'CONFIG_SNAPSHOT_CHANGED',
    'CONFIG_PRODUCTION_CHANGED',
    'CONFIG_BACKUP_INVALID',
    'NGINX_HEALTH_UNAVAILABLE',
    'CONFIG_RELEASE_NEEDS_ATTENTION',
    'AGENT_UNAVAILABLE',
    'CONFIG_OPERATION_TIMEOUT',
  ] as const

  it.each(errorCodes)('accepts the stable %s error code', async (code) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ error: { code, message: 'safe', request_id: 'request-1' } }, 409),
    )

    await expect(new APIClient(fetchMock).listWorkspaces()).rejects.toMatchObject({
      kind: 'api',
      apiError: { code },
    })
  })

  it.each([
    ['invalid_request', { field: 'body' }],
    ['rate_limited', { retry_after_seconds: 3 }],
    ['CONFIG_PATH_INVALID', { path: 'conf.d/site.conf', field: 'path' }],
    ['CONFIG_ENTRY_NOT_MANAGED', { path: 'conf.d/site.conf' }],
    ['CONFIG_LIMIT_EXCEEDED', { limit_name: 'file_bytes', limit_value: 10, actual: 11 }],
    ['CONFIG_WORKSPACE_CONFLICT', { current_etag: draftETagA, field: 'path' }],
  ])('accepts whitelisted details for %s', async (code, details) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      jsonResponse({ error: { code, message: 'safe', request_id: 'request-1', details } }, 409),
    )

    await expect(new APIClient(fetchMock).listWorkspaces()).rejects.toMatchObject({
      kind: 'api',
      apiError: { code, details },
    })
  })

  it.each([
    ['unknown error code', { error: { code: 'SURPRISE', message: 'safe', request_id: 'request-1' } }],
    ['unknown detail key', { error: { code: 'CONFIG_LIMIT_EXCEEDED', message: 'safe', request_id: 'request-1', details: { secret: '/etc/nginx' } } }],
    ['invalid detail value', { error: { code: 'CONFIG_LIMIT_EXCEEDED', message: 'safe', request_id: 'request-1', details: { actual: null } } }],
    ['extra error property', { error: { code: 'internal_error', message: 'safe', request_id: 'request-1', stack: 'secret' } }],
    ['extra envelope property', { error: { code: 'internal_error', message: 'safe', request_id: 'request-1' }, trace: 'secret' }],
  ])('rejects an error envelope with %s', async (_name, payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload, 500))

    await expect(new APIClient(fetchMock).listWorkspaces()).rejects.toMatchObject({
      kind: 'malformed_response',
    })
  })

  it.each([
    ['session', () => new APIClient(vi.fn<typeof fetch>().mockResolvedValue(jsonResponse({ ...sessionPayload, extra: true }))).getSession()],
    ['runtime', () => new APIClient(vi.fn<typeof fetch>().mockResolvedValue(jsonResponse({ ...systemStatusPayload, extra: true }))).getSystemStatus()],
    ['effective config', () => new APIClient(vi.fn<typeof fetch>().mockResolvedValue(jsonResponse({ ...effectiveConfigPayload, extra: true }))).getEffectiveConfig()],
  ])('rejects unknown properties on the closed %s schema', async (_name, request) => {
    await expect(request()).rejects.toMatchObject({ kind: 'malformed_response' })
  })
})

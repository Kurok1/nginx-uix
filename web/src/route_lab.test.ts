/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */
import type {
  RouteAnalysis,
  RouteHistoryPage,
  RouteTestRequest,
  RouteTestRun,
} from './api/route_lab'
import type { WorkspaceDetail } from './api/types'
import {
  createRouteLabStore,
  ROUTE_SIDE_EFFECT_CONFIRMATION,
  type RouteLabClient,
  type RouteLabEventStream,
} from './route_lab'
import type { SessionStore } from './session'

const workspace: WorkspaceDetail = {
  id: '0123456789abcdef0123456789abcdef',
  name: 'Route draft',
  state: 'ready',
  production_digest: 'a'.repeat(64),
  base_digest: 'a'.repeat(64),
  draft_etag: `"draft-v1:${'b'.repeat(64)}"`,
  entry_count: 2,
  managed_bytes: 128,
  workspace_bytes: 512,
  created_by: 7,
  created_at: '2026-07-21T08:00:00Z',
  updated_at: '2026-07-21T08:01:00Z',
}

const analysis: RouteAnalysis = {
  complete: true,
  normalized_uri: '/api',
  predicted_server_route_id: `srv_${'1'.repeat(32)}`,
  predicted_location_route_id: `loc_${'2'.repeat(32)}`,
  runtime_redirect_possible: false,
  servers: [],
  locations: [],
}

const request: RouteTestRequest = {
  scheme: 'http',
  host: 'example.test',
  port: 80,
  sni: '',
  method: 'GET',
  uri: '/api',
  query: '',
  headers: [],
  body: '',
  timeout_ms: 5000,
  assertions: { status_code: 200, contains_text: '', forbidden_text: '' },
  confirmation: '',
}

const emptyDigest = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
const queued: RouteTestRun = {
  id: '11111111111111111111111111111111',
  workspace_id: workspace.id,
  workspace_revision: 2,
  workspace_etag: workspace.draft_etag,
  production_digest: workspace.production_digest,
  draft_digest: 'b'.repeat(64),
  state: 'queued',
  stage: 'queued',
  safe_request: {
    scheme: request.scheme,
    host: request.host,
    port: request.port,
    sni: request.sni,
    method: request.method,
    uri: request.uri,
    query: request.query,
    headers: [],
    sensitive_header_names: [],
    body_bytes: 0,
    body_digest: emptyDigest,
    timeout_ms: request.timeout_ms,
    assertions: request.assertions,
    side_effecting: false,
    replayable: true,
  },
  static_analysis: analysis,
  replayable: true,
  side_effecting: false,
  body_bytes: 0,
  body_digest: emptyDigest,
  sensitive_header_names: [],
  created_at: '2026-07-21T08:02:00Z',
  updated_at: '2026-07-21T08:02:00Z',
  stages: [],
}

class FakeStream implements RouteLabEventStream {
  readonly listeners = new Map<string, Set<EventListener>>()
  closed = false

  addEventListener(type: string, listener: EventListener): void {
    const listeners = this.listeners.get(type) ?? new Set<EventListener>()
    listeners.add(listener)
    this.listeners.set(type, listeners)
  }

  close(): void {
    this.closed = true
  }

  emit(type: string): void {
    for (const listener of this.listeners.get(type) ?? []) listener(new Event(type))
  }
}

function sessionFixture(): SessionStore {
  return {
    state: {
      phase: 'authenticated',
      session: {
        user: { id: 7, username: 'operator', created_at: '2026-07-21T08:00:00Z' },
        csrf_token: 'csrf-token',
        created_at: '2026-07-21T08:00:00Z',
        last_seen_at: '2026-07-21T08:00:00Z',
        idle_expires_at: '2026-07-21T16:00:00Z',
        absolute_expires_at: '2026-07-22T08:00:00Z',
      },
    },
    handleAPIError: () => false,
    login: async () => undefined,
    logout: async () => undefined,
    onExpired: () => () => undefined,
    restore: async () => undefined,
  }
}

function clientFixture(): RouteLabClient & {
  analyses: number
  queued: RouteTestRequest[]
  reads: number
  cancellations: number
} {
  return {
    analyses: 0,
    queued: [],
    reads: 0,
    cancellations: 0,
    async analyzeRoute() {
      this.analyses += 1
      return analysis
    },
    async createRouteTest(_workspaceId, input) {
      this.queued.push(input)
      return {
        ...queued,
        safe_request: {
          ...queued.safe_request,
          method: input.method,
          body_bytes: new TextEncoder().encode(input.body).length,
          side_effecting: input.method === 'POST' || input.body !== '',
          replayable: input.body === '',
        },
        method: input.method,
      } as RouteTestRun
    },
    async getRouteTest() {
      this.reads += 1
      return queued
    },
    async listRouteTests(): Promise<RouteHistoryPage> {
      return { runs: [queued] }
    },
    async cancelRouteTest() {
      this.cancellations += 1
      return { ...queued, cancel_requested_at: '2026-07-21T08:02:01Z' }
    },
  }
}

describe('Route Lab store', () => {
  it('exposes a stable error code when route analysis fails', async () => {
    const client = clientFixture()
    client.analyzeRoute = async () => Promise.reject(new Error('raw upstream failure'))
    const store = createRouteLabStore(client, sessionFixture(), () => new FakeStream())

    await expect(store.analyze(workspace, request)).rejects.toThrow('raw upstream failure')
    expect(store.state.error).toBe('analysis_failed')
    store.dispose()
  })

  it('deduplicates analysis and requires exact confirmation for side-effecting input', async () => {
    const client = clientFixture()
    const store = createRouteLabStore(client, sessionFixture(), () => new FakeStream())

    const first = store.analyze(workspace, request)
    const second = store.analyze(workspace, request)
    await expect(first).resolves.toEqual(analysis)
    await expect(second).resolves.toEqual(analysis)
    expect(client.analyses).toBe(1)

    const post = { ...request, method: 'POST' as const }
    await expect(store.queue(workspace, post, '')).rejects.toThrow('exact confirmation')
    expect(client.queued).toHaveLength(0)

    await store.queue(workspace, post, ROUTE_SIDE_EFFECT_CONFIRMATION)
    expect(client.queued[0]?.confirmation).toBe(ROUTE_SIDE_EFFECT_CONFIRMATION)
    store.dispose()
  })

  it('reconnects after stream errors without cancelling and closes only after persisted terminal evidence', async () => {
    const client = clientFixture()
    const stream = new FakeStream()
    const store = createRouteLabStore(client, sessionFixture(), (url) => {
      expect(url).toBe(`/api/v1/route-tests/${queued.id}/events`)
      return stream
    })

    await store.queue(workspace, request, '')
    stream.emit('open')
    expect(store.state.stream).toBe('live')
    stream.emit('error')
    expect(store.state.stream).toBe('reconnecting')
    expect(client.cancellations).toBe(0)

    client.getRouteTest = async () => ({
      ...queued,
      state: 'succeeded',
      stage: 'completed',
      finished_at: '2026-07-21T08:02:02Z',
    })
    stream.emit('terminal')
    await vi.waitFor(() => expect(store.state.activeRun?.state).toBe('succeeded'))
    expect(stream.closed).toBe(true)
    expect(store.state.stream).toBe('closed')
    store.dispose()
  })

  it('requests cancellation explicitly and retains the run until the server records a terminal state', async () => {
    const client = clientFixture()
    const store = createRouteLabStore(client, sessionFixture(), () => new FakeStream())

    await store.queue(workspace, request, '')
    await store.cancel()

    expect(client.cancellations).toBe(1)
    expect(store.state.activeRun?.cancel_requested_at).toBe('2026-07-21T08:02:01Z')
    expect(store.state.activeRun?.state).toBe('queued')
    expect(store.state.stream).not.toBe('closed')
    store.dispose()
  })
})

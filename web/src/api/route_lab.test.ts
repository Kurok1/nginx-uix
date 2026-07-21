/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */
import { APIClient } from './client'
import {
  parseRouteAnalysis,
  parseRouteHistoryPage,
  parseRouteRun,
  replayRouteRequest,
  type RouteTestRequest,
} from './route_lab'

const workspaceID = '0123456789abcdef0123456789abcdef'
const runID = '11111111111111111111111111111111'
const serverRouteID = `srv_${'2'.repeat(32)}`
const locationRouteID = `loc_${'3'.repeat(32)}`
const digestA = 'a'.repeat(64)
const digestB = 'b'.repeat(64)
const draftETag = `"draft-v1:${digestB}"`
const emptyDigest = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'

const source = {
  path: 'conf.d/site.conf',
  start_line: 2,
  start_column: 3,
  end_line: 8,
  end_column: 4,
}

function analysisFixture() {
  return {
    complete: true,
    normalized_uri: '/api/users',
    predicted_server_route_id: serverRouteID,
    predicted_location_route_id: locationRouteID,
    runtime_redirect_possible: false,
    servers: [
      {
        route_id: serverRouteID,
        source,
        listeners: [
          {
            address: '',
            port: 80,
            ssl: false,
            default_server: true,
            derived: false,
            supported: true,
          },
        ],
        server_names: ['example.test'],
        disposition: 'selected',
        reason: 'server_name_exact',
      },
    ],
    locations: [
      {
        route_id: locationRouteID,
        parent_route_id: serverRouteID,
        source,
        matcher_type: 'prefix',
        matcher: '/api',
        depth: 0,
        disposition: 'selected',
        reason: 'location_longest_prefix',
      },
    ],
  }
}

function safeRequestFixture() {
  return {
    scheme: 'http',
    host: 'example.test',
    port: 80,
    sni: '',
    method: 'GET',
    uri: '/api/users',
    query: 'page=1',
    headers: [{ name: 'Accept', value: 'application/json' }],
    sensitive_header_names: ['Authorization'],
    body_bytes: 0,
    body_digest: emptyDigest,
    timeout_ms: 5000,
    assertions: {
      status_code: 200,
      contains_text: 'users',
      forbidden_text: 'error',
    },
    side_effecting: false,
    replayable: false,
  }
}

function runFixture() {
  return {
    id: runID,
    workspace_id: workspaceID,
    workspace_revision: 2,
    workspace_etag: draftETag,
    production_digest: digestA,
    draft_digest: digestB,
    candidate_digest: digestB,
    state: 'succeeded',
    stage: 'completed',
    safe_request: safeRequestFixture(),
    static_analysis: analysisFixture(),
    terminal_result: {
      agent_result: {
        candidate_digest: digestB,
        routes: [
          {
            route_id: serverRouteID,
            node_id: '2'.repeat(32),
            parent_route_id: '',
            kind: 'server',
            matcher_type: 'unknown',
            matcher: 'example.test',
            source,
          },
          {
            route_id: locationRouteID,
            node_id: '3'.repeat(32),
            parent_route_id: serverRouteID,
            kind: 'location',
            matcher_type: 'prefix',
            matcher: '/api',
            source,
          },
        ],
        response: {
          status_code: 200,
          headers: [{ name: 'Content-Type', value: 'application/json' }],
          body_snippet: '{"users":[]}',
          body_bytes: 12,
          body_digest: 'c'.repeat(64),
          body_truncated: false,
          snippet_omitted: false,
          duration_ms: 18,
          assertions: {
            passed: true,
            complete: true,
            results: [
              { kind: 'status_code', passed: true, complete: true },
              { kind: 'contains_text', passed: true, complete: true },
              { kind: 'forbidden_text', passed: true, complete: true },
            ],
          },
        },
        evidence: {
          server_route_id: serverRouteID,
          route_id: locationRouteID,
          final_uri: '/api/users',
          upstream: 'http://backend',
          upstream_status: '200',
          status_code: 200,
          request_time_ms: 16,
        },
        cleanup: { master_reaped: true, port_closed: true, stage_removed: true },
        diagnostics: [],
      },
    },
    replayable: false,
    side_effecting: false,
    body_bytes: 0,
    body_digest: emptyDigest,
    sensitive_header_names: ['Authorization'],
    created_at: '2026-07-21T08:00:00Z',
    updated_at: '2026-07-21T08:00:01Z',
    started_at: '2026-07-21T08:00:00Z',
    finished_at: '2026-07-21T08:00:01Z',
    stages: [
      {
        sequence: 1,
        stage: 'completed',
        result: 'success',
        details: {},
        occurred_at: '2026-07-21T08:00:01Z',
      },
    ],
  }
}

const request: RouteTestRequest = {
  scheme: 'http',
  host: 'example.test',
  port: 80,
  sni: '',
  method: 'GET',
  uri: '/api/users',
  query: 'page=1',
  headers: [{ name: 'Accept', value: 'application/json' }],
  body: '',
  timeout_ms: 5000,
  assertions: { status_code: 200, contains_text: 'users', forbidden_text: 'error' },
  confirmation: '',
}

describe('Route Lab API boundary', () => {
  it('parses distinct static and runtime evidence and rejects unknown fields', () => {
    const analysis = parseRouteAnalysis(analysisFixture(), 200)
    const run = parseRouteRun(runFixture(), 200)

    expect(analysis.predicted_location_route_id).toBe(locationRouteID)
    expect(run.terminal_result?.agent_result.evidence.route_id).toBe(locationRouteID)
    expect(run.terminal_result?.agent_result.cleanup).toEqual({
      master_reaped: true,
      port_closed: true,
      stage_removed: true,
    })

    expect(() => parseRouteAnalysis({ ...analysisFixture(), raw_config: 'secret' }, 200)).toThrowError(
      expect.objectContaining({ kind: 'malformed_response' }),
    )
  })

  it('does not reconstruct omitted bodies or sensitive header values when copying history', () => {
    const run = parseRouteRun(runFixture(), 200)
    const copied = replayRouteRequest(run)

    expect(copied).toMatchObject({
      method: 'GET',
      uri: '/api/users',
      body: '',
      headers: [{ name: 'Accept', value: 'application/json' }],
    })
    expect(copied?.headers.some(({ name }) => name === 'Authorization')).toBe(false)
    expect(copied?.confirmation).toBe('')
    expect(run.replayable).toBe(false)
  })

  it('parses stable history pages and rejects repeated or malformed cursors at the client boundary', () => {
    expect(parseRouteHistoryPage({ runs: [runFixture()], next_cursor: 'opaque_cursor' }, 200)).toMatchObject({
      runs: [{ id: runID }],
      next_cursor: 'opaque_cursor',
    })
    expect(() => parseRouteHistoryPage({ runs: [], next_cursor: '../cursor' }, 200)).toThrowError(
      expect.objectContaining({ kind: 'malformed_response' }),
    )
  })

  it('sends CSRF and strong ETag headers and validates the queued resource location', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(analysisFixture()), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ...runFixture(), state: 'queued', stage: 'queued', terminal_result: undefined }), {
          status: 202,
          headers: {
            'Content-Type': 'application/json',
            Location: `/api/v1/route-tests/${runID}`,
          },
        }),
      )
    const client = new APIClient(fetcher)

    await client.analyzeRoute(workspaceID, request, draftETag, 'csrf-token')
    await client.createRouteTest(workspaceID, request, draftETag, 'csrf-token')

    expect(fetcher).toHaveBeenNthCalledWith(
      1,
      `/api/v1/config/workspaces/${workspaceID}/route-analyses`,
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({
          'If-Match': draftETag,
          'X-CSRF-Token': 'csrf-token',
        }),
      }),
    )
    expect(fetcher).toHaveBeenNthCalledWith(
      2,
      `/api/v1/config/workspaces/${workspaceID}/route-tests`,
      expect.objectContaining({ credentials: 'same-origin' }),
    )
  })
})

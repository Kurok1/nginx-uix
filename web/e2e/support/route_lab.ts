/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */
import { expect, type Page, type Route } from '@playwright/test'

import type {
  RouteAnalysis,
  RouteHeader,
  RouteRunState,
  RouteTestRequest,
  RouteTestRun,
} from '../../src/api/route_lab'
import {
  appOrigin,
  csrfToken,
  sessionCookieName,
  sessionCookieValue,
  type WorkspaceAPIFixture,
} from './api'

export const routeSideEffectConfirmation = 'RUN SIDE-EFFECTING REQUEST'

const runID = '9'.repeat(32)
const serverRouteID = `srv_${'1'.repeat(32)}`
const predictedLocationID = `loc_${'2'.repeat(32)}`
const observedLocationID = `loc_${'3'.repeat(32)}`
const productionDigest = 'a'.repeat(64)
const draftDigest = 'b'.repeat(64)
const candidateDigest = 'c'.repeat(64)
const emptyDigest = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'

export interface RouteLabAPIRequest {
  method: string
  path: string
  headers: Readonly<Record<string, string>>
  body: unknown
}

export interface RouteLabAPIFixture {
  runId: string
  callsFor: (method: string, path: string) => readonly RouteLabAPIRequest[]
  assertContract: () => void
}

export async function installRouteLabAPIFixture(
  page: Page,
  workspace: WorkspaceAPIFixture,
  options: {
    reconnectBeforeTerminal?: boolean
    runningUntilCancelled?: boolean
  } = {},
): Promise<RouteLabAPIFixture> {
  const basePath = `/api/v1/config/workspaces/${workspace.workspaceId}`
  const requests: RouteLabAPIRequest[] = []
  const violations: string[] = []
  let input: RouteTestRequest | null = null
  let state: RouteRunState = 'queued'
  let runCreated = false
  let eventConnections = 0
  let cancelRequestedAt = ''
  let releaseCancellation: (() => void) | null = null
  const cancellationGate = new Promise<void>((resolve) => {
    releaseCancellation = resolve
  })

  await page.route(`${appOrigin}/api/v1/**`, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()
    const path = url.pathname
    const handled =
      path === `${basePath}/route-analyses` ||
      path === `${basePath}/route-tests` ||
      path === '/api/v1/route-tests' ||
      path === `/api/v1/route-tests/${runID}` ||
      path === `/api/v1/route-tests/${runID}/events` ||
      path === `/api/v1/route-tests/${runID}/cancellations`
    if (!handled) {
      await route.fallback()
      return
    }

    const headers = request.headers()
    const body = parseBody(request.postData(), violations)
    requests.push({ method, path, headers, body })
    if (!hasSessionCookie(headers.cookie)) {
      violations.push(`route request omitted session cookie: ${method} ${path}`)
      await fulfillJSON(route, 401, apiError('unauthenticated', 'Authentication required'))
      return
    }

    if (path === `${basePath}/route-analyses` && method === 'POST') {
      if (!validateWorkspaceMutation(headers, workspace, violations)) {
        await reject(route)
        return
      }
      const candidate = parseRouteRequest(body, violations)
      if (candidate === null || candidate.confirmation !== '') {
        await reject(route)
        return
      }
      input = candidate
      await fulfillJSON(route, 200, analysis())
      return
    }

    if (path === `${basePath}/route-tests` && method === 'POST') {
      if (!validateWorkspaceMutation(headers, workspace, violations)) {
        await reject(route)
        return
      }
      const candidate = parseRouteRequest(body, violations)
      if (candidate === null) {
        await reject(route)
        return
      }
      const sideEffecting = candidate.body !== '' || !['GET', 'HEAD', 'OPTIONS'].includes(candidate.method)
      if (
        sideEffecting && candidate.confirmation !== routeSideEffectConfirmation ||
        !sideEffecting && candidate.confirmation !== ''
      ) {
        violations.push('route queue confirmation did not match the request risk')
        await reject(route)
        return
      }
      input = candidate
      runCreated = true
      state = 'queued'
      await fulfillJSON(route, 202, runProjection(workspace, candidate, state), {
        Location: `/api/v1/route-tests/${runID}`,
      })
      return
    }

    if (path === '/api/v1/route-tests' && method === 'GET') {
      if (![...url.searchParams.keys()].every((key) => key === 'limit') || url.searchParams.get('limit') !== '20') {
        violations.push(`unexpected route history query: ${url.search}`)
        await reject(route)
        return
      }
      await fulfillJSON(route, 200, { runs: [] })
      return
    }

    if (path === `/api/v1/route-tests/${runID}` && method === 'GET') {
      if (url.search !== '' || body !== null || input === null || !runCreated) {
        violations.push('invalid route run read')
        await reject(route)
        return
      }
      await fulfillJSON(route, 200, runProjection(workspace, input, state, cancelRequestedAt))
      return
    }

    if (path === `/api/v1/route-tests/${runID}/events` && method === 'GET') {
      if (url.search !== '' || body !== null || input === null || !runCreated) {
        violations.push('invalid route event stream request')
        await reject(route)
        return
      }
      eventConnections += 1
      if (options.runningUntilCancelled === true) {
        state = 'running'
        await cancellationGate
        await new Promise((resolve) => setTimeout(resolve, 75))
        state = 'cancelled'
        await fulfillEvents(route, terminalEvent('cancelled', 2))
        return
      }
      if (options.reconnectBeforeTerminal === true && eventConnections === 1) {
        state = 'running'
        await fulfillEvents(route, `retry: 20\nid: 1\nevent: stage\ndata: {}\n\n`)
        return
      }
      if (options.reconnectBeforeTerminal === true && eventConnections > 1) {
        if (headers['last-event-id'] !== undefined && headers['last-event-id'] !== '1') {
          violations.push(`SSE reconnect Last-Event-ID was ${String(headers['last-event-id'])}`)
        }
      }
      state = 'succeeded'
      await fulfillEvents(route, terminalEvent('completed', options.reconnectBeforeTerminal === true ? 2 : 1))
      return
    }

    if (path === `/api/v1/route-tests/${runID}/cancellations` && method === 'POST') {
      if (!validateCancellation(headers, violations) || !isEmptyObject(body) || input === null) {
        await reject(route)
        return
      }
      cancelRequestedAt = '2026-07-21T08:00:01Z'
      const projection = runProjection(workspace, input, 'running', cancelRequestedAt)
      releaseCancellation?.()
      await fulfillJSON(route, 202, projection)
      return
    }

    violations.push(`unexpected Route Lab API request: ${method} ${path}`)
    await reject(route)
  })

  return {
    runId: runID,
    callsFor: (method, path) => requests.filter((request) => request.method === method && request.path === path),
    assertContract: () => expect(violations, 'Route Lab API contract violations').toEqual([]),
  }
}

function analysis(): RouteAnalysis {
  const source = routeSource()
  return {
    complete: true,
    normalized_uri: '/api/users',
    predicted_server_route_id: serverRouteID,
    predicted_location_route_id: predictedLocationID,
    runtime_redirect_possible: true,
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
        route_id: predictedLocationID,
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

function runProjection(
  workspace: WorkspaceAPIFixture,
  input: RouteTestRequest,
  state: RouteRunState,
  cancelRequestedAt = '',
): RouteTestRun {
  const sensitiveHeaderNames = input.headers
    .filter(({ name }) => isSensitiveHeader(name))
    .map(({ name }) => name)
    .sort()
  const safeHeaders = input.headers
    .filter(({ name }) => !isSensitiveHeader(name))
    .map((header) => ({ ...header }))
  const bodyBytes = new TextEncoder().encode(input.body).length
  const sideEffecting = bodyBytes > 0 || !['GET', 'HEAD', 'OPTIONS'].includes(input.method)
  const replayable = bodyBytes === 0 && sensitiveHeaderNames.length === 0
  const safeRequest = {
    scheme: input.scheme,
    host: input.host,
    port: input.port,
    sni: input.sni,
    method: input.method,
    uri: input.uri,
    query: input.query,
    headers: safeHeaders,
    sensitive_header_names: sensitiveHeaderNames,
    body_bytes: bodyBytes,
    body_digest: bodyBytes === 0 ? emptyDigest : 'd'.repeat(64),
    timeout_ms: input.timeout_ms,
    assertions: { ...input.assertions },
    side_effecting: sideEffecting,
    replayable,
  }
  const stage = state === 'succeeded'
    ? 'completed'
    : state === 'cancelled'
      ? 'cancelled'
      : state === 'running'
        ? 'requesting'
        : 'queued'
  const stages: RouteTestRun['stages'] = state === 'queued'
    ? []
    : [
        {
          sequence: 1,
          stage,
          result: state === 'succeeded' ? 'success' : state === 'cancelled' ? 'warning' : 'running',
          details: {},
          occurred_at: '2026-07-21T08:00:01Z',
        },
      ]
  return {
    id: runID,
    workspace_id: workspace.workspaceId,
    workspace_revision: 2,
    workspace_etag: workspace.currentDraftETag(),
    production_digest: productionDigest,
    draft_digest: draftDigest,
    ...(state === 'succeeded' ? { candidate_digest: candidateDigest } : {}),
    state,
    stage,
    safe_request: safeRequest,
    static_analysis: analysis(),
    ...(state === 'succeeded' ? { terminal_result: terminalResult(input, safeHeaders) } : {}),
    replayable,
    side_effecting: sideEffecting,
    body_bytes: bodyBytes,
    body_digest: safeRequest.body_digest,
    sensitive_header_names: sensitiveHeaderNames,
    ...(cancelRequestedAt === '' ? {} : { cancel_requested_at: cancelRequestedAt }),
    created_at: '2026-07-21T08:00:00Z',
    updated_at: state === 'queued' ? '2026-07-21T08:00:00Z' : '2026-07-21T08:00:01Z',
    ...(state === 'queued' ? {} : { started_at: '2026-07-21T08:00:00Z' }),
    ...(state === 'succeeded' || state === 'cancelled'
      ? { finished_at: '2026-07-21T08:00:01Z' }
      : {}),
    stages,
  }
}

function terminalResult(input: RouteTestRequest, safeHeaders: RouteHeader[]): NonNullable<RouteTestRun['terminal_result']> {
  const assertionResults: NonNullable<RouteTestRun['terminal_result']>['agent_result']['response']['assertions']['results'] = []
  if (input.assertions.status_code !== 0) {
    assertionResults.push({ kind: 'status_code', passed: input.assertions.status_code === 200, complete: true })
  }
  if (input.assertions.contains_text !== '') {
    assertionResults.push({ kind: 'contains_text', passed: true, complete: true })
  }
  if (input.assertions.forbidden_text !== '') {
    assertionResults.push({ kind: 'forbidden_text', passed: true, complete: true })
  }
  return {
    agent_result: {
      candidate_digest: candidateDigest,
      routes: [
        {
          route_id: serverRouteID,
          node_id: '1'.repeat(32),
          parent_route_id: '',
          kind: 'server',
          matcher_type: 'unknown',
          matcher: 'example.test',
          source: routeSource(),
        },
        {
          route_id: observedLocationID,
          node_id: '3'.repeat(32),
          parent_route_id: serverRouteID,
          kind: 'location',
          matcher_type: 'prefix',
          matcher: '/api/users',
          source: routeSource(),
        },
      ],
      response: {
        status_code: 200,
        headers: [{ name: 'Content-Type', value: 'application/json' }, ...safeHeaders],
        body_snippet: '{"users":[]}',
        body_bytes: 12,
        body_digest: 'e'.repeat(64),
        body_truncated: false,
        snippet_omitted: false,
        duration_ms: 18,
        assertions: {
          passed: assertionResults.every(({ passed }) => passed),
          complete: assertionResults.every(({ complete }) => complete),
          results: assertionResults,
        },
      },
      evidence: {
        server_route_id: serverRouteID,
        route_id: observedLocationID,
        final_uri: '/api/users',
        upstream: 'http://backend',
        upstream_status: '200',
        status_code: 200,
        request_time_ms: 16,
      },
      cleanup: { master_reaped: true, port_closed: true, stage_removed: true },
      diagnostics: [],
    },
  }
}

function routeSource() {
  return {
    path: 'conf.d/site.conf',
    start_line: 3,
    start_column: 3,
    end_line: 8,
    end_column: 4,
  }
}

function parseRouteRequest(value: unknown, violations: string[]): RouteTestRequest | null {
  const expectedKeys = [
    'scheme',
    'host',
    'port',
    'sni',
    'method',
    'uri',
    'query',
    'headers',
    'body',
    'timeout_ms',
    'assertions',
    'confirmation',
  ].sort()
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    violations.push('Route Lab request was not an object')
    return null
  }
  const record = value as Record<string, unknown>
  if (JSON.stringify(Object.keys(record).sort()) !== JSON.stringify(expectedKeys)) {
    violations.push(`Route Lab request keys were ${Object.keys(record).sort().join(',')}`)
    return null
  }
  return record as unknown as RouteTestRequest
}

function parseBody(value: string | null, violations: string[]): unknown {
  if (value === null) return null
  try {
    return JSON.parse(value) as unknown
  } catch {
    violations.push('Route Lab request body was invalid JSON')
    return null
  }
}

function validateWorkspaceMutation(
  headers: Readonly<Record<string, string>>,
  workspace: WorkspaceAPIFixture,
  violations: string[],
): boolean {
  let valid = true
  if (headers.origin !== appOrigin) {
    violations.push(`Route Lab mutation Origin was ${String(headers.origin)}`)
    valid = false
  }
  if (headers['content-type'] !== 'application/json') {
    violations.push(`Route Lab mutation Content-Type was ${String(headers['content-type'])}`)
    valid = false
  }
  if (headers['x-csrf-token'] !== csrfToken) {
    violations.push(`Route Lab mutation CSRF was ${String(headers['x-csrf-token'])}`)
    valid = false
  }
  if (headers['if-match'] !== workspace.currentDraftETag()) {
    violations.push(`Route Lab mutation If-Match was ${String(headers['if-match'])}`)
    valid = false
  }
  return valid
}

function validateCancellation(
  headers: Readonly<Record<string, string>>,
  violations: string[],
): boolean {
  let valid = true
  if (headers.origin !== appOrigin || headers['content-type'] !== 'application/json') {
    violations.push('Route Lab cancellation Origin or Content-Type was invalid')
    valid = false
  }
  if (headers['x-csrf-token'] !== csrfToken) {
    violations.push(`Route Lab cancellation CSRF was ${String(headers['x-csrf-token'])}`)
    valid = false
  }
  if (headers['if-match'] !== undefined) {
    violations.push('Route Lab cancellation unexpectedly sent If-Match')
    valid = false
  }
  return valid
}

function isSensitiveHeader(name: string): boolean {
  const lower = name.toLowerCase()
  return lower === 'authorization' || lower === 'cookie' || lower.includes('token') || lower.includes('secret')
}

function hasSessionCookie(cookie: string | undefined): boolean {
  return cookie
    ?.split(';')
    .map((part) => part.trim())
    .includes(`${sessionCookieName}=${sessionCookieValue}`) === true
}

function isEmptyObject(value: unknown): boolean {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && Object.keys(value).length === 0
}

function terminalEvent(stage: 'completed' | 'cancelled', sequence: number): string {
  return `id: ${sequence}\nevent: terminal\ndata: ${JSON.stringify({ sequence, stage })}\n\n`
}

async function fulfillEvents(route: Route, body: string): Promise<void> {
  await route.fulfill({
    status: 200,
    contentType: 'text/event-stream',
    body,
    headers: { 'Cache-Control': 'no-store', 'X-Accel-Buffering': 'no' },
  })
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
    headers: { 'Cache-Control': 'no-store', 'X-Request-ID': 'request-route-lab-e2e', ...headers },
  })
}

async function reject(route: Route): Promise<void> {
  await fulfillJSON(route, 422, apiError('ROUTE_REQUEST_INVALID', 'Route Lab fixture rejected request'))
}

function apiError(code: string, message: string) {
  return { error: { code, message, request_id: 'request-route-lab-e2e' } }
}

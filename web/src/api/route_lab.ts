/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */
export type RouteScheme = 'http' | 'https'
export type RouteMethod = 'GET' | 'HEAD' | 'OPTIONS' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
export type RouteCandidateDisposition = 'selected' | 'matched' | 'excluded' | 'indeterminate'
export type RouteMatcherType =
  | 'unknown'
  | 'exact'
  | 'prefix'
  | 'prefix_priority'
  | 'regex'
  | 'regex_insensitive'
  | 'named'
export type RouteCandidateReason =
  | 'listener_mismatch'
  | 'listener_unsupported'
  | 'listener_default'
  | 'server_name_exact'
  | 'server_name_leading_wildcard'
  | 'server_name_trailing_wildcard'
  | 'server_name_regex'
  | 'server_name_lower_priority'
  | 'server_name_indeterminate'
  | 'location_exact'
  | 'location_longest_prefix'
  | 'location_prefix_priority'
  | 'location_regex'
  | 'location_shorter_prefix'
  | 'location_prefix_no_match'
  | 'location_regex_no_match'
  | 'location_earlier_regex_selected'
  | 'location_named_not_initial'
  | 'location_parent_matched'
  | 'location_parent_not_selected'
  | 'location_regex_indeterminate'
  | 'location_uri_normalization_indeterminate'
export type RouteRunState = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'timed_out'
export type RouteRunStageName =
  | 'queued'
  | 'preparing'
  | 'validating'
  | 'starting'
  | 'requesting'
  | 'collecting'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'timed_out'
export type RouteStageResult = 'pending' | 'running' | 'success' | 'failed' | 'warning'
export type RouteAssertionKind = 'status_code' | 'contains_text' | 'forbidden_text'

export interface RouteHeader {
  name: string
  value: string
}

export interface RouteAssertionsInput {
  status_code: number
  contains_text: string
  forbidden_text: string
}

export interface RouteTestRequest {
  scheme: RouteScheme
  host: string
  port: number
  sni: string
  method: RouteMethod
  uri: string
  query: string
  headers: RouteHeader[]
  body: string
  timeout_ms: number
  assertions: RouteAssertionsInput
  confirmation: string
}

export interface RouteSource {
  path: string
  start_line: number
  start_column: number
  end_line: number
  end_column: number
}

export interface RouteListener {
  address: string
  port: number
  ssl: boolean
  default_server: boolean
  derived: boolean
  supported: boolean
}

export interface RouteServerCandidate {
  route_id: string
  source: RouteSource
  listeners: RouteListener[]
  server_names: string[]
  disposition: RouteCandidateDisposition
  reason: RouteCandidateReason
}

export interface RouteLocationCandidate {
  route_id: string
  parent_route_id: string
  source: RouteSource
  matcher_type: RouteMatcherType
  matcher: string
  depth: number
  disposition: RouteCandidateDisposition
  reason: RouteCandidateReason
}

export interface RouteAnalysis {
  complete: boolean
  normalized_uri: string
  predicted_tls_server_route_id?: string
  predicted_server_route_id?: string
  predicted_location_route_id?: string
  runtime_redirect_possible: boolean
  servers: RouteServerCandidate[]
  locations: RouteLocationCandidate[]
}

export interface RouteSafeRequest {
  scheme: RouteScheme
  host: string
  port: number
  sni: string
  method: RouteMethod
  uri: string
  query: string
  headers: RouteHeader[]
  sensitive_header_names: string[]
  body_bytes: number
  body_digest: string
  timeout_ms: number
  assertions: RouteAssertionsInput
  side_effecting: boolean
  replayable: boolean
}

export interface RouteAssertionResult {
  kind: RouteAssertionKind
  passed: boolean
  complete: boolean
}

export interface RouteAssertionOutcome {
  passed: boolean
  complete: boolean
  results: RouteAssertionResult[]
}

export interface RouteDefinition {
  route_id: string
  node_id: string
  parent_route_id: string
  kind: 'server' | 'location'
  matcher_type: RouteMatcherType
  matcher: string
  source: RouteSource
}

export interface RouteHTTPResponse {
  status_code: number
  headers: RouteHeader[]
  body_snippet: string
  body_bytes: number
  body_digest: string
  body_truncated: boolean
  snippet_omitted: boolean
  duration_ms: number
  assertions: RouteAssertionOutcome
}

export interface RouteRuntimeEvidence {
  server_route_id: string
  route_id: string
  final_uri: string
  upstream: string
  upstream_status: string
  status_code: number
  request_time_ms: number
}

export interface RouteCleanupEvidence {
  master_reaped: boolean
  port_closed: boolean
  stage_removed: boolean
}

export interface RouteAgentDiagnostic {
  code: string
  path: string
  line: number
  summary: string
}

export interface RouteAgentResult {
  candidate_digest: string
  routes: RouteDefinition[]
  response: RouteHTTPResponse
  evidence: RouteRuntimeEvidence
  cleanup: RouteCleanupEvidence
  diagnostics: RouteAgentDiagnostic[]
}

export interface RouteTerminalResult {
  agent_result: RouteAgentResult
}

export interface RouteRunStage {
  sequence: number
  stage: RouteRunStageName
  result: RouteStageResult
  code?: string
  details: Readonly<Record<string, unknown>>
  occurred_at: string
}

export interface RouteTestRun {
  id: string
  workspace_id: string
  workspace_revision: number
  workspace_etag: string
  production_digest: string
  draft_digest: string
  candidate_digest?: string
  state: RouteRunState
  stage: RouteRunStageName
  safe_request: RouteSafeRequest
  static_analysis: RouteAnalysis
  terminal_result?: RouteTerminalResult
  replayable: boolean
  side_effecting: boolean
  body_bytes: number
  body_digest: string
  sensitive_header_names: string[]
  last_error_code?: string
  cancel_requested_at?: string
  created_at: string
  updated_at: string
  started_at?: string
  finished_at?: string
  stages: RouteRunStage[]
}

export interface RouteHistoryPage {
  runs: RouteTestRun[]
  next_cursor?: string
}

export interface RouteHistoryQuery {
  workspace_id?: string
  state?: RouteRunState
  cursor?: string
  limit?: number
}

class RouteMalformedResponse extends Error {
  readonly kind = 'malformed_response'
  readonly status: number

  constructor(status: number) {
    super('API response was malformed')
    this.name = 'APIRequestError'
    this.status = status
  }
}

const candidateReasons: readonly RouteCandidateReason[] = [
  'listener_mismatch',
  'listener_unsupported',
  'listener_default',
  'server_name_exact',
  'server_name_leading_wildcard',
  'server_name_trailing_wildcard',
  'server_name_regex',
  'server_name_lower_priority',
  'server_name_indeterminate',
  'location_exact',
  'location_longest_prefix',
  'location_prefix_priority',
  'location_regex',
  'location_shorter_prefix',
  'location_prefix_no_match',
  'location_regex_no_match',
  'location_earlier_regex_selected',
  'location_named_not_initial',
  'location_parent_matched',
  'location_parent_not_selected',
  'location_regex_indeterminate',
  'location_uri_normalization_indeterminate',
]
const matcherTypes: readonly RouteMatcherType[] = [
  'unknown',
  'exact',
  'prefix',
  'prefix_priority',
  'regex',
  'regex_insensitive',
  'named',
]
const runStates: readonly RouteRunState[] = [
  'queued',
  'running',
  'succeeded',
  'failed',
  'cancelled',
  'timed_out',
]
const runStages: readonly RouteRunStageName[] = [
  'queued',
  'preparing',
  'validating',
  'starting',
  'requesting',
  'collecting',
  'completed',
  'failed',
  'cancelled',
  'timed_out',
]

export function parseRouteAnalysis(value: unknown, status: number): RouteAnalysis {
  if (
    !hasExactKeys(
      value,
      ['complete', 'normalized_uri', 'runtime_redirect_possible', 'servers', 'locations'],
      [
        'predicted_tls_server_route_id',
        'predicted_server_route_id',
        'predicted_location_route_id',
      ],
    ) ||
    typeof value.complete !== 'boolean' ||
    !isBoundedString(value.normalized_uri, 1, 8192) ||
    !value.normalized_uri.startsWith('/') ||
    typeof value.runtime_redirect_possible !== 'boolean' ||
    !isOptionalRouteID(value.predicted_tls_server_route_id, 'srv') ||
    !isOptionalRouteID(value.predicted_server_route_id, 'srv') ||
    !isOptionalRouteID(value.predicted_location_route_id, 'loc') ||
    !isBoundedArray(value.servers, 1000) ||
    !isBoundedArray(value.locations, 5000)
  ) {
    throw malformed(status)
  }
  return {
    complete: value.complete,
    normalized_uri: value.normalized_uri,
    ...(value.predicted_tls_server_route_id === undefined
      ? {}
      : { predicted_tls_server_route_id: value.predicted_tls_server_route_id }),
    ...(value.predicted_server_route_id === undefined
      ? {}
      : { predicted_server_route_id: value.predicted_server_route_id }),
    ...(value.predicted_location_route_id === undefined
      ? {}
      : { predicted_location_route_id: value.predicted_location_route_id }),
    runtime_redirect_possible: value.runtime_redirect_possible,
    servers: value.servers.map((candidate) => parseServerCandidate(candidate, status)),
    locations: value.locations.map((candidate) => parseLocationCandidate(candidate, status)),
  }
}

export function parseRouteRun(value: unknown, status: number): RouteTestRun {
  if (
    !hasExactKeys(
      value,
      [
        'id',
        'workspace_id',
        'workspace_revision',
        'workspace_etag',
        'production_digest',
        'draft_digest',
        'state',
        'stage',
        'safe_request',
        'static_analysis',
        'replayable',
        'side_effecting',
        'body_bytes',
        'body_digest',
        'sensitive_header_names',
        'created_at',
        'updated_at',
        'stages',
      ],
      [
        'candidate_digest',
        'terminal_result',
        'last_error_code',
        'cancel_requested_at',
        'started_at',
        'finished_at',
      ],
    ) ||
    !isOpaqueID(value.id) ||
    !isOpaqueID(value.workspace_id) ||
    !isInteger(value.workspace_revision, 1) ||
    !isDraftETag(value.workspace_etag) ||
    !isDigest(value.production_digest) ||
    !isDigest(value.draft_digest) ||
    (value.candidate_digest !== undefined && !isDigest(value.candidate_digest)) ||
    !isOneOf(value.state, runStates) ||
    !isOneOf(value.stage, runStages) ||
    typeof value.replayable !== 'boolean' ||
    typeof value.side_effecting !== 'boolean' ||
    !isInteger(value.body_bytes, 0, 65_536) ||
    !isDigest(value.body_digest) ||
    !isHeaderNameArray(value.sensitive_header_names, 32) ||
    !isOptionalCode(value.last_error_code) ||
    !isTimestamp(value.created_at) ||
    !isTimestamp(value.updated_at) ||
    !isOptionalTimestamp(value.cancel_requested_at) ||
    !isOptionalTimestamp(value.started_at) ||
    !isOptionalTimestamp(value.finished_at) ||
    !isBoundedArray(value.stages, 512)
  ) {
    throw malformed(status)
  }
  const safeRequest = parseSafeRequest(value.safe_request, status)
  if (
    safeRequest.replayable !== value.replayable ||
    safeRequest.side_effecting !== value.side_effecting ||
    safeRequest.body_bytes !== value.body_bytes ||
    safeRequest.body_digest !== value.body_digest ||
    !sameStrings(safeRequest.sensitive_header_names, value.sensitive_header_names)
  ) {
    throw malformed(status)
  }
  const stages = value.stages.map((stage) => parseRunStage(stage, status))
  if (!strictlyIncreasing(stages.map(({ sequence }) => sequence))) {
    throw malformed(status)
  }
  return {
    id: value.id,
    workspace_id: value.workspace_id,
    workspace_revision: value.workspace_revision,
    workspace_etag: value.workspace_etag,
    production_digest: value.production_digest,
    draft_digest: value.draft_digest,
    ...(value.candidate_digest === undefined ? {} : { candidate_digest: value.candidate_digest }),
    state: value.state,
    stage: value.stage,
    safe_request: safeRequest,
    static_analysis: parseRouteAnalysis(value.static_analysis, status),
    ...(value.terminal_result === undefined
      ? {}
      : { terminal_result: parseTerminalResult(value.terminal_result, status) }),
    replayable: value.replayable,
    side_effecting: value.side_effecting,
    body_bytes: value.body_bytes,
    body_digest: value.body_digest,
    sensitive_header_names: [...value.sensitive_header_names],
    ...(value.last_error_code === undefined ? {} : { last_error_code: value.last_error_code }),
    ...(value.cancel_requested_at === undefined
      ? {}
      : { cancel_requested_at: value.cancel_requested_at }),
    created_at: value.created_at,
    updated_at: value.updated_at,
    ...(value.started_at === undefined ? {} : { started_at: value.started_at }),
    ...(value.finished_at === undefined ? {} : { finished_at: value.finished_at }),
    stages,
  }
}

export function parseRouteHistoryPage(value: unknown, status: number): RouteHistoryPage {
  if (
    !hasExactKeys(value, ['runs'], ['next_cursor']) ||
    !isBoundedArray(value.runs, 100) ||
    (value.next_cursor !== undefined &&
      (!isBoundedString(value.next_cursor, 1, 512) || !/^[A-Za-z0-9_-]+$/.test(value.next_cursor)))
  ) {
    throw malformed(status)
  }
  return {
    runs: value.runs.map((run) => parseRouteRun(run, status)),
    ...(value.next_cursor === undefined ? {} : { next_cursor: value.next_cursor }),
  }
}

export function replayRouteRequest(run: RouteTestRun): RouteTestRequest {
  return {
    scheme: run.safe_request.scheme,
    host: run.safe_request.host,
    port: run.safe_request.port,
    sni: run.safe_request.sni,
    method: run.safe_request.method,
    uri: run.safe_request.uri,
    query: run.safe_request.query,
    headers: run.safe_request.headers.map((header) => ({ ...header })),
    body: '',
    timeout_ms: run.safe_request.timeout_ms,
    assertions: { ...run.safe_request.assertions },
    confirmation: '',
  }
}

export function isTerminalRouteRun(run: RouteTestRun | null): boolean {
  return run !== null && ['succeeded', 'failed', 'cancelled', 'timed_out'].includes(run.state)
}

function parseServerCandidate(value: unknown, status: number): RouteServerCandidate {
  if (
    !hasExactKeys(value, [
      'route_id',
      'source',
      'listeners',
      'server_names',
      'disposition',
      'reason',
    ]) ||
    !isRouteID(value.route_id, 'srv') ||
    !isBoundedArray(value.listeners, 64, 1) ||
    !isStringArray(value.server_names, 1024, 1024, 1) ||
    !isOneOf(value.disposition, ['selected', 'matched', 'excluded', 'indeterminate']) ||
    !isOneOf(value.reason, candidateReasons)
  ) {
    throw malformed(status)
  }
  return {
    route_id: value.route_id,
    source: parseSource(value.source, status),
    listeners: value.listeners.map((listener) => parseListener(listener, status)),
    server_names: [...value.server_names],
    disposition: value.disposition,
    reason: value.reason,
  }
}

function parseLocationCandidate(value: unknown, status: number): RouteLocationCandidate {
  if (
    !hasExactKeys(value, [
      'route_id',
      'parent_route_id',
      'source',
      'matcher_type',
      'matcher',
      'depth',
      'disposition',
      'reason',
    ]) ||
    !isRouteID(value.route_id, 'loc') ||
    !isRouteID(value.parent_route_id, 'srv') ||
    !isOneOf(value.matcher_type, matcherTypes) ||
    !isBoundedString(value.matcher, 0, 4096) ||
    !isInteger(value.depth, 0, 128) ||
    !isOneOf(value.disposition, ['selected', 'matched', 'excluded', 'indeterminate']) ||
    !isOneOf(value.reason, candidateReasons)
  ) {
    throw malformed(status)
  }
  return {
    route_id: value.route_id,
    parent_route_id: value.parent_route_id,
    source: parseSource(value.source, status),
    matcher_type: value.matcher_type,
    matcher: value.matcher,
    depth: value.depth,
    disposition: value.disposition,
    reason: value.reason,
  }
}

function parseListener(value: unknown, status: number): RouteListener {
  if (
    !hasExactKeys(value, ['address', 'port', 'ssl', 'default_server', 'derived', 'supported']) ||
    !isBoundedString(value.address, 0, 1024) ||
    !isInteger(value.port, 1, 65_535) ||
    typeof value.ssl !== 'boolean' ||
    typeof value.default_server !== 'boolean' ||
    typeof value.derived !== 'boolean' ||
    typeof value.supported !== 'boolean'
  ) {
    throw malformed(status)
  }
  return {
    address: value.address,
    port: value.port,
    ssl: value.ssl,
    default_server: value.default_server,
    derived: value.derived,
    supported: value.supported,
  }
}

function parseSource(value: unknown, status: number): RouteSource {
  if (
    !hasExactKeys(value, ['path', 'start_line', 'start_column', 'end_line', 'end_column']) ||
    typeof value.path !== 'string' ||
    !isSafeRelativePath(value.path) ||
    !isInteger(value.start_line, 1) ||
    !isInteger(value.start_column, 1) ||
    !isInteger(value.end_line, 1) ||
    !isInteger(value.end_column, 1) ||
    value.end_line < value.start_line ||
    (value.end_line === value.start_line && value.end_column < value.start_column)
  ) {
    throw malformed(status)
  }
  return {
    path: value.path,
    start_line: value.start_line,
    start_column: value.start_column,
    end_line: value.end_line,
    end_column: value.end_column,
  }
}

function parseSafeRequest(value: unknown, status: number): RouteSafeRequest {
  if (
    !hasExactKeys(value, [
      'scheme',
      'host',
      'port',
      'sni',
      'method',
      'uri',
      'query',
      'headers',
      'sensitive_header_names',
      'body_bytes',
      'body_digest',
      'timeout_ms',
      'assertions',
      'side_effecting',
      'replayable',
    ]) ||
    !isOneOf(value.scheme, ['http', 'https']) ||
    !isBoundedString(value.host, 1, 255) ||
    !isInteger(value.port, 1, 65_535) ||
    !isBoundedString(value.sni, 0, 253) ||
    !isOneOf(value.method, ['GET', 'HEAD', 'OPTIONS', 'POST', 'PUT', 'PATCH', 'DELETE']) ||
    !isBoundedString(value.uri, 1, 8192) ||
    !value.uri.startsWith('/') ||
    !isBoundedString(value.query, 0, 8192) ||
    !isBoundedArray(value.headers, 32) ||
    !isHeaderNameArray(value.sensitive_header_names, 32) ||
    !isInteger(value.body_bytes, 0, 65_536) ||
    !isDigest(value.body_digest) ||
    !isInteger(value.timeout_ms, 100, 30_000) ||
    typeof value.side_effecting !== 'boolean' ||
    typeof value.replayable !== 'boolean'
  ) {
    throw malformed(status)
  }
  const headers = value.headers.map((header) => parseHeader(header, status, 16_384))
  const sensitive = new Set(value.sensitive_header_names.map((name) => name.toLowerCase()))
  if (headers.some(({ name }) => sensitive.has(name.toLowerCase()))) {
    throw malformed(status)
  }
  return {
    scheme: value.scheme,
    host: value.host,
    port: value.port,
    sni: value.sni,
    method: value.method,
    uri: value.uri,
    query: value.query,
    headers,
    sensitive_header_names: [...value.sensitive_header_names],
    body_bytes: value.body_bytes,
    body_digest: value.body_digest,
    timeout_ms: value.timeout_ms,
    assertions: parseAssertionsInput(value.assertions, status),
    side_effecting: value.side_effecting,
    replayable: value.replayable,
  }
}

function parseAssertionsInput(value: unknown, status: number): RouteAssertionsInput {
  if (
    !hasExactKeys(value, ['status_code', 'contains_text', 'forbidden_text']) ||
    !isInteger(value.status_code, 0, 599) ||
    (value.status_code > 0 && value.status_code < 100) ||
    !isBoundedString(value.contains_text, 0, 1024) ||
    !isBoundedString(value.forbidden_text, 0, 1024)
  ) {
    throw malformed(status)
  }
  return {
    status_code: value.status_code,
    contains_text: value.contains_text,
    forbidden_text: value.forbidden_text,
  }
}

function parseTerminalResult(value: unknown, status: number): RouteTerminalResult {
  if (!hasExactKeys(value, ['agent_result'])) {
    throw malformed(status)
  }
  return { agent_result: parseAgentResult(value.agent_result, status) }
}

function parseAgentResult(value: unknown, status: number): RouteAgentResult {
  if (
    !hasExactKeys(value, [
      'candidate_digest',
      'routes',
      'response',
      'evidence',
      'cleanup',
      'diagnostics',
    ]) ||
    !isDigest(value.candidate_digest) ||
    !isBoundedArray(value.routes, 6000, 1) ||
    !isBoundedArray(value.diagnostics, 64)
  ) {
    throw malformed(status)
  }
  return {
    candidate_digest: value.candidate_digest,
    routes: value.routes.map((route) => parseRouteDefinition(route, status)),
    response: parseHTTPResponse(value.response, status),
    evidence: parseRuntimeEvidence(value.evidence, status),
    cleanup: parseCleanupEvidence(value.cleanup, status),
    diagnostics: value.diagnostics.map((diagnostic) => parseAgentDiagnostic(diagnostic, status)),
  }
}

function parseRouteDefinition(value: unknown, status: number): RouteDefinition {
  if (
    !hasExactKeys(value, [
      'route_id',
      'node_id',
      'parent_route_id',
      'kind',
      'matcher_type',
      'matcher',
      'source',
    ]) ||
    !isOneOf(value.kind, ['server', 'location']) ||
    !isRouteID(value.route_id, value.kind === 'server' ? 'srv' : 'loc') ||
    !isOpaqueID(value.node_id) ||
    (value.parent_route_id !== '' && !isRouteID(value.parent_route_id)) ||
    !isOneOf(value.matcher_type, matcherTypes) ||
    !isBoundedString(value.matcher, 0, 4096)
  ) {
    throw malformed(status)
  }
  return {
    route_id: value.route_id,
    node_id: value.node_id,
    parent_route_id: value.parent_route_id,
    kind: value.kind,
    matcher_type: value.matcher_type,
    matcher: value.matcher,
    source: parseSource(value.source, status),
  }
}

function parseHTTPResponse(value: unknown, status: number): RouteHTTPResponse {
  if (
    !hasExactKeys(value, [
      'status_code',
      'headers',
      'body_snippet',
      'body_bytes',
      'body_digest',
      'body_truncated',
      'snippet_omitted',
      'duration_ms',
      'assertions',
    ]) ||
    !isInteger(value.status_code, 100, 599) ||
    !isBoundedArray(value.headers, 64) ||
    !isBoundedString(value.body_snippet, 0, 16_384) ||
    !isInteger(value.body_bytes, 0, 65_537) ||
    !isDigest(value.body_digest) ||
    typeof value.body_truncated !== 'boolean' ||
    typeof value.snippet_omitted !== 'boolean' ||
    !isInteger(value.duration_ms, 0, 30_000)
  ) {
    throw malformed(status)
  }
  const headers = value.headers.map((header) => parseHeader(header, status, 16_384))
  if (headers.some(({ name }) => isSensitiveResponseHeader(name))) {
    throw malformed(status)
  }
  return {
    status_code: value.status_code,
    headers,
    body_snippet: value.body_snippet,
    body_bytes: value.body_bytes,
    body_digest: value.body_digest,
    body_truncated: value.body_truncated,
    snippet_omitted: value.snippet_omitted,
    duration_ms: value.duration_ms,
    assertions: parseAssertionOutcome(value.assertions, status),
  }
}

function parseAssertionOutcome(value: unknown, status: number): RouteAssertionOutcome {
  if (
    !hasExactKeys(value, ['passed', 'complete', 'results']) ||
    typeof value.passed !== 'boolean' ||
    typeof value.complete !== 'boolean' ||
    !isBoundedArray(value.results, 3)
  ) {
    throw malformed(status)
  }
  return {
    passed: value.passed,
    complete: value.complete,
    results: value.results.map((result) => parseAssertionResult(result, status)),
  }
}

function parseAssertionResult(value: unknown, status: number): RouteAssertionResult {
  if (
    !hasExactKeys(value, ['kind', 'passed', 'complete']) ||
    !isOneOf(value.kind, ['status_code', 'contains_text', 'forbidden_text']) ||
    typeof value.passed !== 'boolean' ||
    typeof value.complete !== 'boolean'
  ) {
    throw malformed(status)
  }
  return { kind: value.kind, passed: value.passed, complete: value.complete }
}

function parseRuntimeEvidence(value: unknown, status: number): RouteRuntimeEvidence {
  if (
    !hasExactKeys(value, [
      'server_route_id',
      'route_id',
      'final_uri',
      'upstream',
      'upstream_status',
      'status_code',
      'request_time_ms',
    ]) ||
    !isRouteID(value.server_route_id, 'srv') ||
    !isRouteID(value.route_id) ||
    !isBoundedString(value.final_uri, 1, 8192) ||
    !value.final_uri.startsWith('/') ||
    !isBoundedString(value.upstream, 0, 1024) ||
    !isBoundedString(value.upstream_status, 0, 1024) ||
    !isInteger(value.status_code, 100, 599) ||
    !isInteger(value.request_time_ms, 0, 30_000)
  ) {
    throw malformed(status)
  }
  return {
    server_route_id: value.server_route_id,
    route_id: value.route_id,
    final_uri: value.final_uri,
    upstream: value.upstream,
    upstream_status: value.upstream_status,
    status_code: value.status_code,
    request_time_ms: value.request_time_ms,
  }
}

function parseCleanupEvidence(value: unknown, status: number): RouteCleanupEvidence {
  if (
    !hasExactKeys(value, ['master_reaped', 'port_closed', 'stage_removed']) ||
    typeof value.master_reaped !== 'boolean' ||
    typeof value.port_closed !== 'boolean' ||
    typeof value.stage_removed !== 'boolean'
  ) {
    throw malformed(status)
  }
  return {
    master_reaped: value.master_reaped,
    port_closed: value.port_closed,
    stage_removed: value.stage_removed,
  }
}

function parseAgentDiagnostic(value: unknown, status: number): RouteAgentDiagnostic {
  if (
    !hasExactKeys(value, ['code', 'path', 'line', 'summary']) ||
    !isCode(value.code) ||
    (value.path !== '' && (typeof value.path !== 'string' || !isSafeRelativePath(value.path))) ||
    !isInteger(value.line, 0) ||
    !isBoundedString(value.summary, 0, 2048)
  ) {
    throw malformed(status)
  }
  return { code: value.code, path: value.path, line: value.line, summary: value.summary }
}

function parseRunStage(value: unknown, status: number): RouteRunStage {
  if (
    !hasExactKeys(value, ['sequence', 'stage', 'result', 'details', 'occurred_at'], ['code']) ||
    !isInteger(value.sequence, 1, 512) ||
    !isOneOf(value.stage, runStages) ||
    !isOneOf(value.result, ['pending', 'running', 'success', 'failed', 'warning']) ||
    !isOptionalCode(value.code) ||
    !isSafeDetails(value.details) ||
    !isTimestamp(value.occurred_at)
  ) {
    throw malformed(status)
  }
  return {
    sequence: value.sequence,
    stage: value.stage,
    result: value.result,
    ...(value.code === undefined ? {} : { code: value.code }),
    details: { ...value.details },
    occurred_at: value.occurred_at,
  }
}

function parseHeader(value: unknown, status: number, maximumBytes: number): RouteHeader {
  if (
    !hasExactKeys(value, ['name', 'value']) ||
    !isHeaderName(value.name) ||
    !isBoundedString(value.value, 0, maximumBytes) ||
    /[\r\n\0]/.test(value.value)
  ) {
    throw malformed(status)
  }
  return { name: value.name, value: value.value }
}

function malformed(status: number): RouteMalformedResponse {
  return new RouteMalformedResponse(status)
}

function hasExactKeys<const Required extends string, const Optional extends string>(
  value: unknown,
  required: readonly Required[],
  optional: readonly Optional[] = [],
): value is Record<Required, unknown> & Partial<Record<Optional, unknown>> {
  if (!isRecord(value)) return false
  const allowed = new Set<string>([...required, ...optional])
  return (
    required.every((key) => Object.hasOwn(value, key)) &&
    Object.keys(value).every((key) => allowed.has(key))
  )
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isBoundedArray(value: unknown, maximum: number, minimum = 0): value is unknown[] {
  return Array.isArray(value) && value.length >= minimum && value.length <= maximum
}

function isBoundedString(value: unknown, minimum: number, maximum: number): value is string {
  if (typeof value !== 'string') return false
  const length = Array.from(value).length
  return length >= minimum && length <= maximum
}

function isStringArray(
  value: unknown,
  maximum: number,
  itemMaximum: number,
  minimum = 0,
): value is string[] {
  return (
    isBoundedArray(value, maximum, minimum) &&
    value.every((item) => isBoundedString(item, 0, itemMaximum))
  )
}

function isInteger(value: unknown, minimum: number, maximum = Number.MAX_SAFE_INTEGER): value is number {
  return Number.isSafeInteger(value) && (value as number) >= minimum && (value as number) <= maximum
}

function isOneOf<const Value extends string>(value: unknown, allowed: readonly Value[]): value is Value {
  return typeof value === 'string' && allowed.some((candidate) => candidate === value)
}

function isOpaqueID(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{32}$/.test(value)
}

function isDigest(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{64}$/.test(value)
}

function isDraftETag(value: unknown): value is string {
  return typeof value === 'string' && /^"draft-v1:[0-9a-f]{64}"$/.test(value)
}

function isRouteID(value: unknown, kind?: 'srv' | 'loc'): value is string {
  if (typeof value !== 'string') return false
  const pattern = kind === undefined ? /^(?:srv|loc)_[0-9a-f]{32}$/ : new RegExp(`^${kind}_[0-9a-f]{32}$`)
  return pattern.test(value)
}

function isOptionalRouteID(value: unknown, kind: 'srv' | 'loc'): value is string | undefined {
  return value === undefined || isRouteID(value, kind)
}

function isCode(value: unknown): value is string {
  return typeof value === 'string' && /^[a-z0-9_]{1,64}$/.test(value)
}

function isOptionalCode(value: unknown): value is string | undefined {
  return value === undefined || isCode(value)
}

function isTimestamp(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value) &&
    !Number.isNaN(Date.parse(value))
  )
}

function isOptionalTimestamp(value: unknown): value is string | undefined {
  return value === undefined || isTimestamp(value)
}

function isHeaderName(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 256 && /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(value)
}

function isHeaderNameArray(value: unknown, maximum: number): value is string[] {
  return (
    isBoundedArray(value, maximum) &&
    value.every((name) => isHeaderName(name)) &&
    new Set(value.map((name) => name.toLowerCase())).size === value.length
  )
}

function isSensitiveResponseHeader(name: string): boolean {
  const lower = name.toLowerCase()
  return lower === 'authorization' || lower === 'proxy-authorization' || lower === 'set-cookie' || lower === 'cookie'
}

function sameStrings(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index])
}

function strictlyIncreasing(values: readonly number[]): boolean {
  return values.every((value, index) => index === 0 || value > values[index - 1]!)
}

function isSafeDetails(value: unknown): value is Record<string, unknown> {
  if (!isRecord(value)) return false
  try {
    return JSON.stringify(value).length <= 16_384 && safeJSONValue(value, 0)
  } catch {
    return false
  }
}

function safeJSONValue(value: unknown, depth: number): boolean {
  if (depth > 8) return false
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return true
  if (typeof value === 'number') return Number.isFinite(value)
  if (Array.isArray(value)) return value.length <= 256 && value.every((item) => safeJSONValue(item, depth + 1))
  if (!isRecord(value) || Object.keys(value).length > 128) return false
  return Object.entries(value).every(
    ([key, item]) => /^[a-z0-9_]{1,64}$/.test(key) && safeJSONValue(item, depth + 1),
  )
}

function isSafeRelativePath(value: string): boolean {
  const parts = value.split('/')
  const encoder = new TextEncoder()
  return (
    value !== '' &&
    !value.startsWith('/') &&
    !value.includes('\\') &&
    !value.includes('\0') &&
    encoder.encode(value).length <= 1024 &&
    parts.length <= 64 &&
    parts.every(
      (part) =>
        part !== '' &&
        part !== '.' &&
        part !== '..' &&
        encoder.encode(part).length <= 255,
    )
  )
}

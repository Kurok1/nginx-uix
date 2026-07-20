/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import type { WorkspaceDetail } from './types'

const structuredNodeLimit = 10_000
const structuredDiagnosticLimit = 20_000
const structuredArgumentLimit = 1_024

export type StructuredMatcherType =
  | 'unknown'
  | 'exact'
  | 'prefix'
  | 'prefix_priority'
  | 'regex'
  | 'regex_insensitive'
  | 'named'
export type EditableMatcherType = Exclude<StructuredMatcherType, 'unknown'>
export type StructuredReferenceState =
  | 'resolved'
  | 'dangling'
  | 'external'
  | 'dynamic'
  | 'ambiguous'
  | 'unknown'
export type StructuredDiagnosticSeverity = 'blocking' | 'warning'
export type StructuredOperationKind =
  | 'upstream.create'
  | 'upstream.rename'
  | 'upstream.delete'
  | 'upstream_server.create'
  | 'upstream_server.update'
  | 'upstream_server.delete'
  | 'location.create'
  | 'location.update'
  | 'location.delete'
export type StructuredProxyMode = 'preserve' | 'set' | 'remove'

export interface StructuredSource {
  path: string
  start_line: number
  start_column: number
  end_line: number
  end_column: number
}

export interface StructuredProjectDiagnostic {
  code: string
  path: string
  line: number
  column: number
  related_path?: string
}

export interface StructuredHTTPBlock {
  id: string
  source: StructuredSource
  editable: boolean
  read_only_reason?: string
  instances: number
}

export interface PreservedSyntax {
  name: string
  editable: false
}

export interface StructuredEndpoint {
  address: string
  port: number | null
  unix: boolean
}

export interface StructuredUpstreamServer {
  id: string
  source: StructuredSource
  endpoint: StructuredEndpoint
  weight: number | null
  backup: boolean
  down: boolean
  max_fails: number | null
  fail_timeout: string | null
  preserved_parameters: PreservedSyntax[]
  editable: boolean
  read_only_reason?: string
}

export interface StructuredReference {
  id: string
  source: StructuredSource
  state: StructuredReferenceState
  scheme?: 'http' | 'https'
  host?: string
  port: number | null
  uri?: string
  upstream_id?: string
  upstream_name?: string
}

export interface StructuredUpstream {
  id: string
  name: string
  source: StructuredSource
  servers: StructuredUpstreamServer[]
  preserved_directives: PreservedSyntax[]
  references: StructuredReference[]
  editable: boolean
  read_only_reason?: string
  instances: number
}

export interface StructuredLocation {
  id: string
  type: StructuredMatcherType
  matcher: string
  source: StructuredSource
  children: StructuredLocation[]
  proxy_passes: StructuredReference[]
  unknown_directive_count: number
  editable: boolean
  read_only_reason?: string
  proxy_pass_editable: boolean
  proxy_pass_read_only_reason?: string
  instances: number
}

export interface StructuredHTTPServer {
  id: string
  source: StructuredSource
  listens: string[]
  server_names: string[]
  summary_truncated: boolean
  locations: StructuredLocation[]
  editable: boolean
  read_only_reason?: string
  instances: number
}

export interface StructuredDiagnostic {
  domain: 'upstream' | 'location'
  code: string
  severity: StructuredDiagnosticSeverity
  source: StructuredSource
  related_id?: string
  parent_id?: string
}

export interface StructuredConfig {
  workspace_id: string
  draft_etag: string
  complete: boolean
  project_diagnostics: StructuredProjectDiagnostic[]
  http_blocks: StructuredHTTPBlock[]
  upstreams: StructuredUpstream[]
  proxy_pass_references: StructuredReference[]
  servers: StructuredHTTPServer[]
  diagnostics: StructuredDiagnostic[]
}

export interface StructuredUpstreamServerInput {
  address: string
  port: number | null
  unix: boolean
  weight: number | null
  backup: boolean
  down: boolean
  max_fails: number | null
  fail_timeout: string | null
}

export interface StructuredProxyPassInput {
  upstream_id: string
  scheme: 'http' | 'https'
  port: number | null
  uri: string
}

export type StructuredOperation =
  | {
      kind: 'upstream.create'
      input: {
        http_block_id: string
        name: string
        servers: StructuredUpstreamServerInput[]
      }
    }
  | {
      kind: 'upstream.rename'
      input: { upstream_id: string; new_name: string }
    }
  | {
      kind: 'upstream.delete'
      input: { upstream_id: string; confirm_name: string }
    }
  | {
      kind: 'upstream_server.create'
      input: { upstream_id: string; server: StructuredUpstreamServerInput }
    }
  | {
      kind: 'upstream_server.update'
      input: {
        upstream_id: string
        server_id: string
        server: StructuredUpstreamServerInput
      }
    }
  | {
      kind: 'upstream_server.delete'
      input: { upstream_id: string; server_id: string }
    }
  | {
      kind: 'location.create'
      input: {
        parent_id: string
        type: EditableMatcherType
        matcher: string
        proxy_pass: StructuredProxyPassInput | null
      }
    }
  | {
      kind: 'location.update'
      input: {
        location_id: string
        type: EditableMatcherType
        matcher: string
        proxy_mode: StructuredProxyMode
        proxy_pass: StructuredProxyPassInput | null
      }
    }
  | {
      kind: 'location.delete'
      input: { location_id: string; confirm_matcher: string }
    }

export interface StructuredChangedFile {
  path: string
  before_digest: string
  after_digest: string
  added_lines: number
  removed_lines: number
  patch: string
}

export interface StructuredChangePreview {
  preview_id: string
  workspace_id: string
  draft_etag: string
  operation_kind: StructuredOperationKind
  target_id: string
  changed_files: StructuredChangedFile[]
  complete: boolean
}

export interface StructuredChangeResult {
  workspace: WorkspaceDetail
  draft_etag: string
  changed_paths: string[]
}

class StructuredMalformedResponse extends Error {
  readonly kind = 'malformed_response'
  readonly status: number

  constructor(status: number) {
    super('API response was malformed')
    this.name = 'APIRequestError'
    this.status = status
  }
}

export function parseStructuredConfig(value: unknown, status: number): StructuredConfig {
  if (
    !hasExactKeys(value, [
      'workspace_id',
      'draft_etag',
      'complete',
      'project_diagnostics',
      'http_blocks',
      'upstreams',
      'proxy_pass_references',
      'servers',
      'diagnostics',
    ]) ||
    !isOpaqueID(value.workspace_id) ||
    !isDraftETag(value.draft_etag) ||
    typeof value.complete !== 'boolean' ||
    !isBoundedArray(value.project_diagnostics, structuredDiagnosticLimit) ||
    !isBoundedArray(value.http_blocks, structuredNodeLimit) ||
    !isBoundedArray(value.upstreams, structuredNodeLimit) ||
    !isBoundedArray(value.proxy_pass_references, structuredNodeLimit) ||
    !isBoundedArray(value.servers, structuredNodeLimit) ||
    !isBoundedArray(value.diagnostics, structuredDiagnosticLimit)
  ) {
    throw malformed(status)
  }
  const budget = { locations: 0 }
  return {
    workspace_id: value.workspace_id,
    draft_etag: value.draft_etag,
    complete: value.complete,
    project_diagnostics: value.project_diagnostics.map((item) =>
      parseProjectDiagnostic(item, status),
    ),
    http_blocks: value.http_blocks.map((item) => parseHTTPBlock(item, status)),
    upstreams: value.upstreams.map((item) => parseUpstream(item, status)),
    proxy_pass_references: value.proxy_pass_references.map((item) =>
      parseReference(item, status),
    ),
    servers: value.servers.map((item) => parseHTTPServer(item, status, budget)),
    diagnostics: value.diagnostics.map((item) => parseDiagnostic(item, status)),
  }
}

export function parseStructuredChangePreview(
  value: unknown,
  status: number,
): StructuredChangePreview {
  if (
    !hasExactKeys(value, [
      'preview_id',
      'workspace_id',
      'draft_etag',
      'operation_kind',
      'target_id',
      'changed_files',
      'complete',
    ]) ||
    !isDigest(value.preview_id) ||
    !isOpaqueID(value.workspace_id) ||
    !isDraftETag(value.draft_etag) ||
    !isOperationKind(value.operation_kind) ||
    !isOpaqueID(value.target_id) ||
    !isBoundedArray(value.changed_files, 4096, 1) ||
    typeof value.complete !== 'boolean'
  ) {
    throw malformed(status)
  }
  const changedFiles = value.changed_files.map((item) => parseChangedFile(item, status))
  const patchBytes = changedFiles.reduce(
    (total, file) => total + new TextEncoder().encode(file.patch).length,
    0,
  )
  if (
    (value.complete && changedFiles.some((file) => file.patch === '')) ||
    (!value.complete && changedFiles.some((file) => file.patch !== '')) ||
    patchBytes > 4_194_304
  ) {
    throw malformed(status)
  }
  return {
    preview_id: value.preview_id,
    workspace_id: value.workspace_id,
    draft_etag: value.draft_etag,
    operation_kind: value.operation_kind,
    target_id: value.target_id,
    changed_files: changedFiles,
    complete: value.complete,
  }
}

export function parseStructuredChangeResult(
  value: unknown,
  status: number,
  parseWorkspace: (candidate: unknown, responseStatus: number) => WorkspaceDetail,
): StructuredChangeResult {
  if (
    !hasExactKeys(value, ['workspace', 'draft_etag', 'changed_paths']) ||
    !isDraftETag(value.draft_etag) ||
    !isRelativePathArray(value.changed_paths, 4096, 1)
  ) {
    throw malformed(status)
  }
  const workspace = parseWorkspace(value.workspace, status)
  if (workspace.draft_etag !== value.draft_etag) {
    throw malformed(status)
  }
  return {
    workspace,
    draft_etag: value.draft_etag,
    changed_paths: [...value.changed_paths],
  }
}

function parseProjectDiagnostic(
  value: unknown,
  status: number,
): StructuredProjectDiagnostic {
  if (
    !hasExactKeys(value, ['code', 'path', 'line', 'column'], ['related_path']) ||
    !isCode(value.code) ||
    typeof value.path !== 'string' ||
    !isSafeRelativePath(value.path) ||
    !isInteger(value.line, 1) ||
    !isInteger(value.column, 1) ||
    (value.related_path !== undefined &&
      (typeof value.related_path !== 'string' || !isSafeRelativePath(value.related_path)))
  ) {
    throw malformed(status)
  }
  return {
    code: value.code,
    path: value.path,
    line: value.line,
    column: value.column,
    ...(value.related_path === undefined ? {} : { related_path: value.related_path }),
  }
}

function parseHTTPBlock(value: unknown, status: number): StructuredHTTPBlock {
  if (
    !hasExactKeys(value, ['id', 'source', 'editable', 'instances'], ['read_only_reason']) ||
    !isOpaqueID(value.id) ||
    typeof value.editable !== 'boolean' ||
    !isInteger(value.instances, 1, 16_384) ||
    !isReadOnlyReason(value.read_only_reason)
  ) {
    throw malformed(status)
  }
  return {
    id: value.id,
    source: parseSource(value.source, status),
    editable: value.editable,
    ...(value.read_only_reason === undefined ? {} : { read_only_reason: value.read_only_reason }),
    instances: value.instances,
  }
}

function parseUpstream(value: unknown, status: number): StructuredUpstream {
  if (
    !hasExactKeys(
      value,
      ['id', 'name', 'source', 'servers', 'preserved_directives', 'references', 'editable', 'instances'],
      ['read_only_reason'],
    ) ||
    !isOpaqueID(value.id) ||
    !isBoundedString(value.name, 1, 256) ||
    !isBoundedArray(value.servers, structuredNodeLimit) ||
    !isBoundedArray(value.preserved_directives, structuredNodeLimit) ||
    !isBoundedArray(value.references, structuredNodeLimit) ||
    typeof value.editable !== 'boolean' ||
    !isInteger(value.instances, 1, 16_384) ||
    !isReadOnlyReason(value.read_only_reason)
  ) {
    throw malformed(status)
  }
  return {
    id: value.id,
    name: value.name,
    source: parseSource(value.source, status),
    servers: value.servers.map((item) => parseUpstreamServer(item, status)),
    preserved_directives: value.preserved_directives.map((item) =>
      parsePreserved(item, status),
    ),
    references: value.references.map((item) => parseReference(item, status)),
    editable: value.editable,
    ...(value.read_only_reason === undefined ? {} : { read_only_reason: value.read_only_reason }),
    instances: value.instances,
  }
}

function parseUpstreamServer(
  value: unknown,
  status: number,
): StructuredUpstreamServer {
  if (
    !hasExactKeys(
      value,
      [
        'id',
        'source',
        'endpoint',
        'weight',
        'backup',
        'down',
        'max_fails',
        'fail_timeout',
        'preserved_parameters',
        'editable',
      ],
      ['read_only_reason'],
    ) ||
    !isOpaqueID(value.id) ||
    !isNullableInteger(value.weight, 1) ||
    typeof value.backup !== 'boolean' ||
    typeof value.down !== 'boolean' ||
    !isNullableInteger(value.max_fails, 0) ||
    !isNullableBoundedString(value.fail_timeout, 64) ||
    !isBoundedArray(value.preserved_parameters, structuredArgumentLimit) ||
    typeof value.editable !== 'boolean' ||
    !isReadOnlyReason(value.read_only_reason)
  ) {
    throw malformed(status)
  }
  return {
    id: value.id,
    source: parseSource(value.source, status),
    endpoint: parseEndpoint(value.endpoint, status),
    weight: value.weight,
    backup: value.backup,
    down: value.down,
    max_fails: value.max_fails,
    fail_timeout: value.fail_timeout,
    preserved_parameters: value.preserved_parameters.map((item) =>
      parsePreserved(item, status),
    ),
    editable: value.editable,
    ...(value.read_only_reason === undefined ? {} : { read_only_reason: value.read_only_reason }),
  }
}

function parseEndpoint(value: unknown, status: number): StructuredEndpoint {
  if (
    !hasExactKeys(value, ['address', 'port', 'unix']) ||
    !isBoundedString(value.address, 1, 1024) ||
    !isNullableInteger(value.port, 1, 65_535) ||
    typeof value.unix !== 'boolean'
  ) {
    throw malformed(status)
  }
  return { address: value.address, port: value.port, unix: value.unix }
}

function parsePreserved(value: unknown, status: number): PreservedSyntax {
  if (
    !hasExactKeys(value, ['name', 'editable']) ||
    !isBoundedString(value.name, 1, 256) ||
    value.editable !== false
  ) {
    throw malformed(status)
  }
  return { name: value.name, editable: false }
}

function parseReference(value: unknown, status: number): StructuredReference {
  if (
    !hasExactKeys(
      value,
      ['id', 'source', 'state', 'port'],
      ['scheme', 'host', 'uri', 'upstream_id', 'upstream_name'],
    ) ||
    !isOpaqueID(value.id) ||
    !isOneOf(value.state, ['resolved', 'dangling', 'external', 'dynamic', 'ambiguous', 'unknown']) ||
    (value.scheme !== undefined && !isOneOf(value.scheme, ['http', 'https'])) ||
    (value.host !== undefined && !isBoundedString(value.host, 1, 1024)) ||
    !isNullableInteger(value.port, 1, 65_535) ||
    (value.uri !== undefined && !isBoundedString(value.uri, 0, 4096)) ||
    (value.upstream_id !== undefined && !isOpaqueID(value.upstream_id)) ||
    (value.upstream_name !== undefined && !isBoundedString(value.upstream_name, 1, 256))
  ) {
    throw malformed(status)
  }
  return {
    id: value.id,
    source: parseSource(value.source, status),
    state: value.state,
    ...(value.scheme === undefined ? {} : { scheme: value.scheme }),
    ...(value.host === undefined ? {} : { host: value.host }),
    port: value.port,
    ...(value.uri === undefined ? {} : { uri: value.uri }),
    ...(value.upstream_id === undefined ? {} : { upstream_id: value.upstream_id }),
    ...(value.upstream_name === undefined ? {} : { upstream_name: value.upstream_name }),
  }
}

function parseHTTPServer(
  value: unknown,
  status: number,
  budget: { locations: number },
): StructuredHTTPServer {
  if (
    !hasExactKeys(
      value,
      ['id', 'source', 'listens', 'server_names', 'summary_truncated', 'locations', 'editable', 'instances'],
      ['read_only_reason'],
    ) ||
    !isOpaqueID(value.id) ||
    !isStringArray(value.listens, 64, 1024) ||
    !isStringArray(value.server_names, 256, 1024) ||
    typeof value.summary_truncated !== 'boolean' ||
    !isBoundedArray(value.locations, structuredNodeLimit) ||
    typeof value.editable !== 'boolean' ||
    !isReadOnlyReason(value.read_only_reason) ||
    !isInteger(value.instances, 1, 16_384)
  ) {
    throw malformed(status)
  }
  return {
    id: value.id,
    source: parseSource(value.source, status),
    listens: [...value.listens],
    server_names: [...value.server_names],
    summary_truncated: value.summary_truncated,
    locations: value.locations.map((item) => parseLocation(item, status, budget, 0)),
    editable: value.editable,
    ...(value.read_only_reason === undefined ? {} : { read_only_reason: value.read_only_reason }),
    instances: value.instances,
  }
}

function parseLocation(
  value: unknown,
  status: number,
  budget: { locations: number },
  depth: number,
): StructuredLocation {
  budget.locations++
  if (
    depth > 128 ||
    budget.locations > 10_000 ||
    !hasExactKeys(
      value,
      [
        'id',
        'type',
        'matcher',
        'source',
        'children',
        'proxy_passes',
        'unknown_directive_count',
        'editable',
        'proxy_pass_editable',
        'instances',
      ],
      ['read_only_reason', 'proxy_pass_read_only_reason'],
    ) ||
    !isOpaqueID(value.id) ||
    !isOneOf(value.type, [
      'unknown',
      'exact',
      'prefix',
      'prefix_priority',
      'regex',
      'regex_insensitive',
      'named',
    ]) ||
    !isBoundedString(value.matcher, 0, 4096) ||
    !isBoundedArray(value.children, structuredNodeLimit) ||
    !isBoundedArray(value.proxy_passes, structuredNodeLimit) ||
    !isInteger(value.unknown_directive_count, 0, structuredNodeLimit) ||
    typeof value.editable !== 'boolean' ||
    !isReadOnlyReason(value.read_only_reason) ||
    typeof value.proxy_pass_editable !== 'boolean' ||
    !isReadOnlyReason(value.proxy_pass_read_only_reason) ||
    !isInteger(value.instances, 1, 16_384)
  ) {
    throw malformed(status)
  }
  return {
    id: value.id,
    type: value.type,
    matcher: value.matcher,
    source: parseSource(value.source, status),
    children: value.children.map((item) => parseLocation(item, status, budget, depth + 1)),
    proxy_passes: value.proxy_passes.map((item) => parseReference(item, status)),
    unknown_directive_count: value.unknown_directive_count,
    editable: value.editable,
    ...(value.read_only_reason === undefined ? {} : { read_only_reason: value.read_only_reason }),
    proxy_pass_editable: value.proxy_pass_editable,
    ...(value.proxy_pass_read_only_reason === undefined
      ? {}
      : { proxy_pass_read_only_reason: value.proxy_pass_read_only_reason }),
    instances: value.instances,
  }
}

function parseDiagnostic(value: unknown, status: number): StructuredDiagnostic {
  if (
    !hasExactKeys(value, ['domain', 'code', 'severity', 'source'], ['related_id', 'parent_id']) ||
    !isOneOf(value.domain, ['upstream', 'location']) ||
    !isCode(value.code) ||
    !isOneOf(value.severity, ['blocking', 'warning']) ||
    (value.related_id !== undefined && !isOpaqueID(value.related_id)) ||
    (value.parent_id !== undefined && !isOpaqueID(value.parent_id))
  ) {
    throw malformed(status)
  }
  return {
    domain: value.domain,
    code: value.code,
    severity: value.severity,
    source: parseSource(value.source, status),
    ...(value.related_id === undefined ? {} : { related_id: value.related_id }),
    ...(value.parent_id === undefined ? {} : { parent_id: value.parent_id }),
  }
}

function parseSource(value: unknown, status: number): StructuredSource {
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

function parseChangedFile(value: unknown, status: number): StructuredChangedFile {
  if (
    !hasExactKeys(value, [
      'path',
      'before_digest',
      'after_digest',
      'added_lines',
      'removed_lines',
      'patch',
    ]) ||
    typeof value.path !== 'string' ||
    !isSafeRelativePath(value.path) ||
    !isDigest(value.before_digest) ||
    !isDigest(value.after_digest) ||
    value.before_digest === value.after_digest ||
    !isInteger(value.added_lines, 0) ||
    !isInteger(value.removed_lines, 0) ||
    typeof value.patch !== 'string'
  ) {
    throw malformed(status)
  }
  return {
    path: value.path,
    before_digest: value.before_digest,
    after_digest: value.after_digest,
    added_lines: value.added_lines,
    removed_lines: value.removed_lines,
    patch: value.patch,
  }
}

function malformed(status: number): StructuredMalformedResponse {
  return new StructuredMalformedResponse(status)
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

function isBoundedArray(
  value: unknown,
  maximum: number,
  minimum = 0,
): value is unknown[] {
  return Array.isArray(value) && value.length >= minimum && value.length <= maximum
}

function isStringArray(value: unknown, maximum: number, itemMaximum: number): value is string[] {
  return (
    isBoundedArray(value, maximum) &&
    value.every((item) => isBoundedString(item, 0, itemMaximum))
  )
}

function isRelativePathArray(
  value: unknown,
  maximum: number,
  minimum = 0,
): value is string[] {
  return (
    isBoundedArray(value, maximum, minimum) &&
    value.every((item) => typeof item === 'string' && isSafeRelativePath(item))
  )
}

function isBoundedString(
  value: unknown,
  minimum: number,
  maximum: number,
): value is string {
  return (
    typeof value === 'string' &&
    Array.from(value).length >= minimum &&
    Array.from(value).length <= maximum
  )
}

function isNullableBoundedString(value: unknown, maximum: number): value is string | null {
  return value === null || isBoundedString(value, 0, maximum)
}

function isInteger(value: unknown, minimum: number, maximum = Number.MAX_SAFE_INTEGER): value is number {
  return (
    Number.isSafeInteger(value) &&
    (value as number) >= minimum &&
    (value as number) <= maximum
  )
}

function isNullableInteger(
  value: unknown,
  minimum: number,
  maximum = Number.MAX_SAFE_INTEGER,
): value is number | null {
  return value === null || isInteger(value, minimum, maximum)
}

function isOneOf<const Value extends string>(
  value: unknown,
  allowed: readonly Value[],
): value is Value {
  return typeof value === 'string' && allowed.some((candidate) => candidate === value)
}

function isOperationKind(value: unknown): value is StructuredOperationKind {
  return isOneOf(value, [
    'upstream.create',
    'upstream.rename',
    'upstream.delete',
    'upstream_server.create',
    'upstream_server.update',
    'upstream_server.delete',
    'location.create',
    'location.update',
    'location.delete',
  ])
}

function isReadOnlyReason(value: unknown): value is string | undefined {
  return value === undefined || (typeof value === 'string' && /^[a-z0-9_]{1,64}$/.test(value))
}

function isCode(value: unknown): value is string {
  return typeof value === 'string' && /^[a-z0-9_]{1,64}$/.test(value)
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

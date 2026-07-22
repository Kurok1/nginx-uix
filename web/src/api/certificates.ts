/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

export type CertificateEnvironment = 'staging' | 'production'
export type CertificateChallenge = 'http_01' | 'cloudflare_dns_01'
export type ACMEAccountStatus = 'valid' | 'deactivating' | 'deactivated'
export type DNSCredentialStatus = 'valid' | 'needs_attention' | 'deleted'
export type CertificatePlanState = 'planned' | 'executed' | 'expired'
export type CertificateState =
  | 'pending'
  | 'active'
  | 'expiring'
  | 'expired'
  | 'unbound'
  | 'needs_attention'
  | 'deleted'
export type CertificateVersionState = 'ready' | 'active' | 'superseded' | 'needs_attention'
export type CertificateTaskKind = 'issue' | 'renew' | 'bind' | 'unbind'
export type CertificateTaskState =
  | 'queued'
  | 'running'
  | 'cancelling'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'needs_attention'
export type CertificateTaskStageName =
  | 'queued'
  | 'preparing'
  | 'ordering'
  | 'provisioning'
  | 'propagating'
  | 'authorizing'
  | 'finalizing'
  | 'validating'
  | 'deploying'
  | 'cleaning'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'needs_attention'
export type CertificateStageResult = 'pending' | 'running' | 'success' | 'failed' | 'warning'

export interface ACMEDirectory {
  environment: CertificateEnvironment
  directory_url: string
  terms_url: string
  website?: string
  external_account_required: boolean
}

export interface ACMEAccount {
  id: string
  environment: CertificateEnvironment
  directory_url: string
  account_uri: string
  email: string
  status: ACMEAccountStatus
  terms_url: string
  terms_agreed_at: string
  terms_agreed_by: number
  created_at: string
  updated_at: string
}

export interface DNSCredential {
  id: string
  name: string
  provider: 'cloudflare'
  fingerprint: string
  status: DNSCredentialStatus
  verified_at: string
  last_used_at?: string
  created_at: string
  updated_at: string
}

export interface CertificateServerRef {
  path: string
  start_offset: number
  server_names: string[]
  listeners: string[]
  fingerprint: string
}

export interface CertificateServerCandidate {
  ref: CertificateServerRef
  start_line: number
  start_column: number
  tls_enabled: boolean
  editable: boolean
  read_only_reason?: string
}

export interface CertificateBindingDiff {
  path: string
  patch: string
  added_lines: number
  removed_lines: number
}

export interface CertificateOrderPlan {
  id: string
  state: CertificatePlanState
  environment: CertificateEnvironment
  challenge: CertificateChallenge
  account_id: string
  staging_account_id?: string
  dns_credential_id?: string
  certificate_id: string
  primary_identifier: string
  identifiers: string[]
  server_refs: CertificateServerRef[]
  binding_diff: CertificateBindingDiff[]
  production_digest: string
  staging_evidence: boolean
  requires_risk_confirmation: boolean
  risk_confirmation_phrase?: string
  expires_at: string
  created_at: string
}

export interface CertificateBindingPlan {
  id: string
  state: CertificatePlanState
  certificate_id: string
  version_id: string
  server_refs: CertificateServerRef[]
  binding_diff: CertificateBindingDiff[]
  production_digest: string
  expires_at: string
  created_at: string
}

export interface CertificateTaskStage {
  sequence: number
  stage: CertificateTaskStageName
  result: CertificateStageResult
  code?: string
  details: Readonly<Record<string, unknown>>
  occurred_at: string
}

export interface CertificateTask {
  id: string
  kind: CertificateTaskKind
  state: CertificateTaskState
  stage: CertificateTaskStageName
  plan_id?: string
  certificate_id?: string
  version_id?: string
  account_id?: string
  dns_credential_id?: string
  challenge: CertificateChallenge
  release_id?: string
  last_error_code?: string
  cancel_requested_at?: string
  created_at: string
  updated_at: string
  started_at?: string
  finished_at?: string
  stages: CertificateTaskStage[]
}

export interface CertificateVersion {
  id: string
  state: CertificateVersionState
  leaf_fingerprint: string
  serial_number: string
  issuer: string
  not_before: string
  not_after: string
  created_at: string
}

export interface CertificateBinding {
  id: string
  version_id: string
  config_path: string
  server_start_offset: number
  server_names: string[]
  listeners: string[]
  server_fingerprint: string
  created_at: string
  updated_at: string
}

export interface CertificateRecord {
  id: string
  primary_identifier: string
  identifiers: string[]
  challenge: CertificateChallenge
  account_id: string
  dns_credential_id?: string
  state: CertificateState
  active_version_id: string
  auto_renew: boolean
  renew_before_seconds: number
  next_renewal_at?: string
  retry_count: number
  retry_at?: string
  not_before: string
  not_after: string
  last_error_code?: string
  created_at: string
  updated_at: string
  versions?: CertificateVersion[]
  bindings?: CertificateBinding[]
}

export interface CreateACMEAccountInput {
  environment: CertificateEnvironment
  email: string
  terms_of_service_agreed: boolean
}

export interface ImportACMEAccountInput extends CreateACMEAccountInput {
  account_uri: string
  private_key_pem: string
}

export interface CreateDNSCredentialInput {
  name: string
  api_token: string
}

export interface CreateCertificateOrderPlanInput {
  identifiers: string[]
  challenge: CertificateChallenge
  account_id: string
  staging_account_id?: string
  dns_credential_id?: string
  server_refs: CertificateServerRef[]
}

export interface ExecuteCertificateOrderPlanInput {
  confirmation: string
  production_risk_confirmation: string
}

export interface CertificateRenewalPolicyInput {
  confirmation: string
  auto_renew: boolean
  renew_before_seconds: number
}

export interface CertificateExportInput {
  confirmation: string
  include_private_key: boolean
  private_key_confirmation: string
}

export interface CertificateExport {
  blob: Blob
  filename: string
}

class CertificateMalformedResponse extends Error {
  readonly kind = 'malformed_response'
  readonly status: number

  constructor(status: number) {
    super('API response was malformed')
    this.name = 'APIRequestError'
    this.status = status
  }
}

const environments: readonly CertificateEnvironment[] = ['staging', 'production']
const challenges: readonly CertificateChallenge[] = ['http_01', 'cloudflare_dns_01']
const accountStatuses: readonly ACMEAccountStatus[] = ['valid', 'deactivating', 'deactivated']
const credentialStatuses: readonly DNSCredentialStatus[] = ['valid', 'needs_attention', 'deleted']
const planStates: readonly CertificatePlanState[] = ['planned', 'executed', 'expired']
const certificateStates: readonly CertificateState[] = [
  'pending',
  'active',
  'expiring',
  'expired',
  'unbound',
  'needs_attention',
  'deleted',
]
const versionStates: readonly CertificateVersionState[] = [
  'ready',
  'active',
  'superseded',
  'needs_attention',
]
const taskKinds: readonly CertificateTaskKind[] = ['issue', 'renew', 'bind', 'unbind']
const taskStates: readonly CertificateTaskState[] = [
  'queued',
  'running',
  'cancelling',
  'succeeded',
  'failed',
  'cancelled',
  'needs_attention',
]
const taskStages: readonly CertificateTaskStageName[] = [
  'queued',
  'preparing',
  'ordering',
  'provisioning',
  'propagating',
  'authorizing',
  'finalizing',
  'validating',
  'deploying',
  'cleaning',
  'completed',
  'failed',
  'cancelled',
  'needs_attention',
]
const stageResults: readonly CertificateStageResult[] = [
  'pending',
  'running',
  'success',
  'failed',
  'warning',
]

export function parseACMEDirectories(value: unknown, status: number): ACMEDirectory[] {
  if (!hasExactKeys(value, ['directories']) || !isBoundedArray(value.directories, 2)) {
    throw malformed(status)
  }
  return value.directories.map((item) => parseACMEDirectory(item, status))
}

export function parseACMEAccounts(value: unknown, status: number): ACMEAccount[] {
  if (!hasExactKeys(value, ['accounts']) || !isBoundedArray(value.accounts, 100)) {
    throw malformed(status)
  }
  return value.accounts.map((item) => parseACMEAccount(item, status))
}

export function parseACMEAccount(value: unknown, status: number): ACMEAccount {
  if (
    !hasExactKeys(value, [
      'id',
      'environment',
      'directory_url',
      'account_uri',
      'email',
      'status',
      'terms_url',
      'terms_agreed_at',
      'terms_agreed_by',
      'created_at',
      'updated_at',
    ]) ||
    !isOpaqueID(value.id) ||
    !isOneOf(value.environment, environments) ||
    !isHTTPSURL(value.directory_url) ||
    !isHTTPSURL(value.account_uri) ||
    !isBoundedString(value.email, 1, 254) ||
    !isOneOf(value.status, accountStatuses) ||
    !isHTTPSURL(value.terms_url) ||
    !isTimestamp(value.terms_agreed_at) ||
    !isPositiveInteger(value.terms_agreed_by) ||
    !isTimestamp(value.created_at) ||
    !isTimestamp(value.updated_at)
  ) {
    throw malformed(status)
  }
  return value as unknown as ACMEAccount
}

export function parseDNSCredentials(value: unknown, status: number): DNSCredential[] {
  if (!hasExactKeys(value, ['credentials']) || !isBoundedArray(value.credentials, 100)) {
    throw malformed(status)
  }
  return value.credentials.map((item) => parseDNSCredential(item, status))
}

export function parseDNSCredential(value: unknown, status: number): DNSCredential {
  if (
    !hasExactKeys(
      value,
      ['id', 'name', 'provider', 'fingerprint', 'status', 'verified_at', 'created_at', 'updated_at'],
      ['last_used_at'],
    ) ||
    !isOpaqueID(value.id) ||
    !isBoundedString(value.name, 1, 128) ||
    value.provider !== 'cloudflare' ||
    !isShortFingerprint(value.fingerprint) ||
    !isOneOf(value.status, credentialStatuses) ||
    !isTimestamp(value.verified_at) ||
    !isOptionalTimestamp(value.last_used_at) ||
    !isTimestamp(value.created_at) ||
    !isTimestamp(value.updated_at)
  ) {
    throw malformed(status)
  }
  return value as unknown as DNSCredential
}

export function parseCertificateServerCandidates(
  value: unknown,
  status: number,
): CertificateServerCandidate[] {
  if (!hasExactKeys(value, ['candidates']) || !isBoundedArray(value.candidates, 100)) {
    throw malformed(status)
  }
  return value.candidates.map((item) => parseServerCandidate(item, status))
}

export function parseCertificateOrderPlan(value: unknown, status: number): CertificateOrderPlan {
  if (
    !hasExactKeys(
      value,
      [
        'id',
        'state',
        'environment',
        'challenge',
        'account_id',
        'certificate_id',
        'primary_identifier',
        'identifiers',
        'server_refs',
        'binding_diff',
        'production_digest',
        'staging_evidence',
        'requires_risk_confirmation',
        'expires_at',
        'created_at',
      ],
      ['staging_account_id', 'dns_credential_id', 'risk_confirmation_phrase'],
    ) ||
    !isOpaqueID(value.id) ||
    !isOneOf(value.state, planStates) ||
    !isOneOf(value.environment, environments) ||
    !isOneOf(value.challenge, challenges) ||
    !isOpaqueID(value.account_id) ||
    !isOptionalOpaqueID(value.staging_account_id) ||
    !isOptionalOpaqueID(value.dns_credential_id) ||
    !isOpaqueID(value.certificate_id) ||
    !isDNSIdentifier(value.primary_identifier) ||
    !isIdentifierArray(value.identifiers) ||
    !isBoundedArray(value.server_refs, 100) ||
    !value.server_refs.every((item) => isServerRef(item)) ||
    !isBoundedArray(value.binding_diff, 200) ||
    !value.binding_diff.every((item) => isBindingDiff(item)) ||
    !isDigest(value.production_digest) ||
    typeof value.staging_evidence !== 'boolean' ||
    typeof value.requires_risk_confirmation !== 'boolean' ||
    !isOptionalBoundedString(value.risk_confirmation_phrase, 1, 128) ||
    !isTimestamp(value.expires_at) ||
    !isTimestamp(value.created_at)
  ) {
    throw malformed(status)
  }
  return value as unknown as CertificateOrderPlan
}

export function parseCertificateBindingPlan(value: unknown, status: number): CertificateBindingPlan {
  if (
    !hasExactKeys(value, [
      'id',
      'state',
      'certificate_id',
      'version_id',
      'server_refs',
      'binding_diff',
      'production_digest',
      'expires_at',
      'created_at',
    ]) ||
    !isOpaqueID(value.id) ||
    !isOneOf(value.state, planStates) ||
    !isOpaqueID(value.certificate_id) ||
    !isOpaqueID(value.version_id) ||
    !isBoundedArray(value.server_refs, 100) ||
    !value.server_refs.every((item) => isServerRef(item)) ||
    !isBoundedArray(value.binding_diff, 200) ||
    !value.binding_diff.every((item) => isBindingDiff(item)) ||
    !isDigest(value.production_digest) ||
    !isTimestamp(value.expires_at) ||
    !isTimestamp(value.created_at)
  ) {
    throw malformed(status)
  }
  return value as unknown as CertificateBindingPlan
}

export function parseCertificateTasks(value: unknown, status: number): CertificateTask[] {
  if (!hasExactKeys(value, ['tasks']) || !isBoundedArray(value.tasks, 100)) {
    throw malformed(status)
  }
  return value.tasks.map((item) => parseCertificateTask(item, status))
}

export function parseCertificateTask(value: unknown, status: number): CertificateTask {
  if (
    !hasExactKeys(
      value,
      ['id', 'kind', 'state', 'stage', 'challenge', 'created_at', 'updated_at', 'stages'],
      [
        'plan_id',
        'certificate_id',
        'version_id',
        'account_id',
        'dns_credential_id',
        'release_id',
        'last_error_code',
        'cancel_requested_at',
        'started_at',
        'finished_at',
      ],
    ) ||
    !isOpaqueID(value.id) ||
    !isOneOf(value.kind, taskKinds) ||
    !isOneOf(value.state, taskStates) ||
    !isOneOf(value.stage, taskStages) ||
    !isOneOf(value.challenge, challenges) ||
    !isOptionalOpaqueID(value.plan_id) ||
    !isOptionalOpaqueID(value.certificate_id) ||
    !isOptionalOpaqueID(value.version_id) ||
    !isOptionalOpaqueID(value.account_id) ||
    !isOptionalOpaqueID(value.dns_credential_id) ||
    !isOptionalOpaqueIDOrNotRequired(value.release_id) ||
    !isOptionalCode(value.last_error_code) ||
    !isOptionalTimestamp(value.cancel_requested_at) ||
    !isTimestamp(value.created_at) ||
    !isTimestamp(value.updated_at) ||
    !isOptionalTimestamp(value.started_at) ||
    !isOptionalTimestamp(value.finished_at) ||
    !isBoundedArray(value.stages, 512)
  ) {
    throw malformed(status)
  }
  const stages = value.stages.map((item) => parseCertificateTaskStage(item, status))
  if (!strictlyIncreasing(stages.map((stage) => stage.sequence))) {
    throw malformed(status)
  }
  return { ...(value as unknown as Omit<CertificateTask, 'stages'>), stages }
}

export function parseCertificateTaskStageEvent(payload: string, status: number): CertificateTaskStage {
  if (payload.length === 0 || payload.length > 16_384) {
    throw malformed(status)
  }
  let value: unknown
  try {
    value = JSON.parse(payload) as unknown
  } catch {
    throw malformed(status)
  }
  return parseCertificateTaskStage(value, status)
}

export function parseCertificates(value: unknown, status: number): CertificateRecord[] {
  if (!hasExactKeys(value, ['certificates']) || !isBoundedArray(value.certificates, 100)) {
    throw malformed(status)
  }
  return value.certificates.map((item) => parseCertificate(item, status))
}

export function parseCertificate(value: unknown, status: number): CertificateRecord {
  if (
    !hasExactKeys(
      value,
      [
        'id',
        'primary_identifier',
        'identifiers',
        'challenge',
        'account_id',
        'state',
        'active_version_id',
        'auto_renew',
        'renew_before_seconds',
        'retry_count',
        'not_before',
        'not_after',
        'created_at',
        'updated_at',
      ],
      [
        'dns_credential_id',
        'next_renewal_at',
        'retry_at',
        'last_error_code',
        'versions',
        'bindings',
      ],
    ) ||
    !isOpaqueID(value.id) ||
    !isDNSIdentifier(value.primary_identifier) ||
    !isIdentifierArray(value.identifiers) ||
    !isOneOf(value.challenge, challenges) ||
    !isOpaqueID(value.account_id) ||
    !isOptionalOpaqueID(value.dns_credential_id) ||
    !isOneOf(value.state, certificateStates) ||
    !(value.active_version_id === '' || isOpaqueID(value.active_version_id)) ||
    typeof value.auto_renew !== 'boolean' ||
    !isPositiveInteger(value.renew_before_seconds) ||
    !isNonNegativeInteger(value.retry_count) ||
    !isOptionalTimestamp(value.next_renewal_at) ||
    !isOptionalTimestamp(value.retry_at) ||
    !isTimestamp(value.not_before) ||
    !isTimestamp(value.not_after) ||
    !isOptionalCode(value.last_error_code) ||
    !isTimestamp(value.created_at) ||
    !isTimestamp(value.updated_at) ||
    (value.versions !== undefined && !isBoundedArray(value.versions, 100)) ||
    (value.bindings !== undefined && !isBoundedArray(value.bindings, 100))
  ) {
    throw malformed(status)
  }
  const versions = value.versions?.map((item) => parseCertificateVersion(item, status))
  const bindings = value.bindings?.map((item) => parseCertificateBinding(item, status))
  return {
    ...(value as unknown as Omit<CertificateRecord, 'versions' | 'bindings'>),
    ...(versions === undefined ? {} : { versions }),
    ...(bindings === undefined ? {} : { bindings }),
  }
}

export function certificateTaskEventsPath(taskID: string): string {
  if (!isOpaqueID(taskID)) {
    throw new TypeError('invalid certificate task id')
  }
  return `/api/v1/certificate-tasks/${taskID}/events`
}

export function isTerminalCertificateTask(state: CertificateTaskState): boolean {
  return state === 'succeeded' || state === 'failed' || state === 'cancelled' || state === 'needs_attention'
}

function parseACMEDirectory(value: unknown, status: number): ACMEDirectory {
  if (
    !hasExactKeys(
      value,
      ['environment', 'directory_url', 'terms_url', 'external_account_required'],
      ['website'],
    ) ||
    !isOneOf(value.environment, environments) ||
    !isHTTPSURL(value.directory_url) ||
    !isHTTPSURL(value.terms_url) ||
    !isOptionalHTTPSURL(value.website) ||
    typeof value.external_account_required !== 'boolean'
  ) {
    throw malformed(status)
  }
  return value as unknown as ACMEDirectory
}

function parseServerCandidate(value: unknown, status: number): CertificateServerCandidate {
  if (
    !hasExactKeys(value, ['ref', 'start_line', 'start_column', 'tls_enabled', 'editable'], ['read_only_reason']) ||
    !isServerRef(value.ref) ||
    !isPositiveInteger(value.start_line) ||
    !isPositiveInteger(value.start_column) ||
    typeof value.tls_enabled !== 'boolean' ||
    typeof value.editable !== 'boolean' ||
    !isOptionalBoundedString(value.read_only_reason, 1, 256)
  ) {
    throw malformed(status)
  }
  return value as unknown as CertificateServerCandidate
}

function parseCertificateTaskStage(value: unknown, status: number): CertificateTaskStage {
  if (
    !hasExactKeys(value, ['sequence', 'stage', 'result', 'details', 'occurred_at'], ['code']) ||
    !isPositiveInteger(value.sequence) ||
    !isOneOf(value.stage, taskStages) ||
    !isOneOf(value.result, stageResults) ||
    !isOptionalCode(value.code) ||
    !isSafeDetails(value.details) ||
    !isTimestamp(value.occurred_at)
  ) {
    throw malformed(status)
  }
  return value as unknown as CertificateTaskStage
}

function parseCertificateVersion(value: unknown, status: number): CertificateVersion {
  if (
    !hasExactKeys(value, [
      'id',
      'state',
      'leaf_fingerprint',
      'serial_number',
      'issuer',
      'not_before',
      'not_after',
      'created_at',
    ]) ||
    !isOpaqueID(value.id) ||
    !isOneOf(value.state, versionStates) ||
    !isDigest(value.leaf_fingerprint) ||
    !isBoundedString(value.serial_number, 1, 256) ||
    !isBoundedString(value.issuer, 1, 512) ||
    !isTimestamp(value.not_before) ||
    !isTimestamp(value.not_after) ||
    !isTimestamp(value.created_at)
  ) {
    throw malformed(status)
  }
  return value as unknown as CertificateVersion
}

function parseCertificateBinding(value: unknown, status: number): CertificateBinding {
  if (
    !hasExactKeys(value, [
      'id',
      'version_id',
      'config_path',
      'server_start_offset',
      'server_names',
      'listeners',
      'server_fingerprint',
      'created_at',
      'updated_at',
    ]) ||
    !isOpaqueID(value.id) ||
    !isOpaqueID(value.version_id) ||
    !isRelativePath(value.config_path) ||
    !isNonNegativeInteger(value.server_start_offset) ||
    !isStringArray(value.server_names, 128, 255) ||
    !isStringArray(value.listeners, 128, 512) ||
    !isDigest(value.server_fingerprint) ||
    !isTimestamp(value.created_at) ||
    !isTimestamp(value.updated_at)
  ) {
    throw malformed(status)
  }
  return value as unknown as CertificateBinding
}

function isServerRef(value: unknown): value is CertificateServerRef {
  return (
    hasExactKeys(value, ['path', 'start_offset', 'server_names', 'listeners', 'fingerprint']) &&
    isRelativePath(value.path) &&
    isNonNegativeInteger(value.start_offset) &&
    isStringArray(value.server_names, 128, 255) &&
    isStringArray(value.listeners, 128, 512) &&
    isDigest(value.fingerprint)
  )
}

function isBindingDiff(value: unknown): value is CertificateBindingDiff {
  return (
    hasExactKeys(value, ['path', 'patch', 'added_lines', 'removed_lines']) &&
    isRelativePath(value.path) &&
    isBoundedString(value.patch, 0, 2_097_152) &&
    isNonNegativeInteger(value.added_lines) &&
    isNonNegativeInteger(value.removed_lines)
  )
}

function hasExactKeys<const Required extends string, const Optional extends string>(
  value: unknown,
  required: readonly Required[],
  optional: readonly Optional[] = [],
): value is Record<Required, unknown> & Partial<Record<Optional, unknown>> {
  if (!isRecord(value)) return false
  const allowed = new Set<string>([...required, ...optional])
  return required.every((key) => Object.hasOwn(value, key)) && Object.keys(value).every((key) => allowed.has(key))
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isBoundedArray(value: unknown, maximum: number): value is unknown[] {
  return Array.isArray(value) && value.length <= maximum
}

function isOneOf<const Value extends string>(value: unknown, values: readonly Value[]): value is Value {
  return typeof value === 'string' && values.includes(value as Value)
}

function isOpaqueID(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{32}$/.test(value)
}

function isOptionalOpaqueID(value: unknown): value is string | undefined {
  return value === undefined || isOpaqueID(value)
}

function isOptionalOpaqueIDOrNotRequired(value: unknown): value is string | undefined {
  return value === undefined || value === 'not_required' || isOpaqueID(value)
}

function isDigest(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{64}$/.test(value)
}

function isShortFingerprint(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{16}$/.test(value)
}

function isBoundedString(value: unknown, minimum: number, maximum: number): value is string {
  return typeof value === 'string' && value.length >= minimum && value.length <= maximum && !value.includes('\0')
}

function isOptionalBoundedString(
  value: unknown,
  minimum: number,
  maximum: number,
): value is string | undefined {
  return value === undefined || isBoundedString(value, minimum, maximum)
}

function isStringArray(value: unknown, maximum: number, stringMaximum: number): value is string[] {
  return isBoundedArray(value, maximum) && value.every((item) => isBoundedString(item, 0, stringMaximum))
}

function isIdentifierArray(value: unknown): value is string[] {
  return (
    isBoundedArray(value, 100) &&
    value.length > 0 &&
    value.every((item) => isDNSIdentifier(item)) &&
    new Set(value).size === value.length
  )
}

function isDNSIdentifier(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.length >= 1 &&
    value.length <= 255 &&
    /^(?:\*\.)?(?=.{1,253}\.?$)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$/.test(value)
  )
}

function isRelativePath(value: unknown): value is string {
  if (typeof value !== 'string' || value === '' || value.startsWith('/') || value.includes('\\') || value.includes('\0')) {
    return false
  }
  const parts = value.split('/')
  return value.length <= 4096 && parts.every((part) => part !== '' && part !== '.' && part !== '..')
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

function isHTTPSURL(value: unknown): value is string {
  if (!isBoundedString(value, 1, 2048)) return false
  try {
    const parsed = new URL(value)
    return parsed.protocol === 'https:' && parsed.username === '' && parsed.password === ''
  } catch {
    return false
  }
}

function isOptionalHTTPSURL(value: unknown): value is string | undefined {
  return value === undefined || isHTTPSURL(value)
}

function isPositiveInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) > 0
}

function isNonNegativeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0
}

function isOptionalCode(value: unknown): value is string | undefined {
  return value === undefined || (typeof value === 'string' && /^[a-z0-9_]{1,128}$/.test(value))
}

function isSafeDetails(value: unknown): value is Record<string, unknown> {
  return isRecord(value) && safeJSONValue(value, 0)
}

function safeJSONValue(value: unknown, depth: number): boolean {
  if (depth > 8) return false
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return true
  if (typeof value === 'number') return Number.isFinite(value)
  if (Array.isArray(value)) return value.length <= 128 && value.every((item) => safeJSONValue(item, depth + 1))
  if (!isRecord(value) || Object.keys(value).length > 64) return false
  return Object.entries(value).every(
    ([key, item]) => /^[a-z0-9_]{1,64}$/.test(key) && safeJSONValue(item, depth + 1),
  )
}

function strictlyIncreasing(values: readonly number[]): boolean {
  return values.every((value, index) => index === 0 || value > values[index - 1]!)
}

function malformed(status: number): CertificateMalformedResponse {
  return new CertificateMalformedResponse(status)
}

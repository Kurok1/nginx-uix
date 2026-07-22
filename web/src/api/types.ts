/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
export interface LoginRequest {
  username: string
  password: string
}

export interface SessionResponse {
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

export type APIErrorCode =
  | 'invalid_request'
  | 'invalid_credentials'
  | 'unauthenticated'
  | 'origin_rejected'
  | 'csrf_rejected'
  | 'rate_limited'
  | 'unsupported_media_type'
  | 'service_unavailable'
  | 'internal_error'
  | 'AUTH_SESSION_EXPIRED'
  | 'NGINX_CONFIG_INVALID'
  | 'NGINX_COMMAND_TIMEOUT'
  | 'NGINX_OUTPUT_TOO_LARGE'
  | 'CONFIG_PATH_INVALID'
  | 'CONFIG_ENTRY_NOT_MANAGED'
  | 'CONFIG_LIMIT_EXCEEDED'
  | 'CONFIG_WORKSPACE_NOT_FOUND'
  | 'CONFIG_PUBLISH_CHECK_NOT_FOUND'
  | 'CONFIG_RELEASE_NOT_FOUND'
  | 'CONFIG_WORKSPACE_CONFLICT'
  | 'CONFIG_WORKSPACE_STALE'
  | 'CONFIG_WORKSPACE_NEEDS_ATTENTION'
  | 'CONFIG_SNAPSHOT_CHANGED'
  | 'CONFIG_PRODUCTION_CHANGED'
  | 'CONFIG_BACKUP_INVALID'
  | 'NGINX_HEALTH_UNAVAILABLE'
  | 'CONFIG_RELEASE_NEEDS_ATTENTION'
  | 'AGENT_UNAVAILABLE'
  | 'CONFIG_OPERATION_TIMEOUT'
	| 'CONFIG_CANDIDATE_INVALID'
	| 'CONFIG_NO_CHANGES'
  | 'CONFIG_PUBLISH_CHECK_EXPIRED'
	| 'CONFIG_PUBLISH_IN_PROGRESS'
	| 'CONFIG_OPERATION_IN_PROGRESS'
	| 'CONFIG_BACKUP_PROTECTED'
	| 'CONFIG_RETENTION_PLAN_EXPIRED'
	| 'CONFIG_ATTENTION_UNRESOLVED'
	| 'CONFIG_BACKUP_TARGET_INVALID'
	| 'CONFIG_RESTORE_NEEDS_ATTENTION'
	| 'NGINX_RESTART_CONFIG_INVALID'
	| 'NGINX_RESTART_FAILED'
	| 'NGINX_RESTART_NEEDS_ATTENTION'
	| 'CONFIG_BACKUP_NOT_FOUND'
	| 'CONFIG_RETENTION_RUN_NOT_FOUND'
	| 'CONFIG_RESTORE_NOT_FOUND'
	| 'NGINX_RESTART_NOT_FOUND'
	| 'CONFIG_ATTENTION_CASE_NOT_FOUND'
	| 'STRUCTURED_PARSE_FAILED'
	| 'STRUCTURED_LIMIT_EXCEEDED'
	| 'STRUCTURED_PREVIEW_STALE'
	| 'STRUCTURED_CONTEXT_AMBIGUOUS'
	| 'STRUCTURED_EDIT_CONFLICT'
	| 'UPSTREAM_INVALID'
	| 'UPSTREAM_DUPLICATE'
	| 'UPSTREAM_REFERENCED'
	| 'UPSTREAM_REFERENCE_INCOMPLETE'
	| 'LOCATION_INVALID'
	| 'LOCATION_DUPLICATE'
	| 'PROXY_PASS_INVALID'
	| 'ROUTE_REQUEST_TOO_LARGE'
	| 'ROUTE_REQUEST_INVALID'
	| 'ROUTE_LAB_UNAVAILABLE'
	| 'ROUTE_TEST_NOT_FOUND'
	| 'ROUTE_WORKSPACE_CONFLICT'
	| 'ROUTE_CONFIRMATION_REQUIRED'
	| 'ROUTE_PROJECT_INCOMPLETE'
	| 'ROUTE_LISTENER_AMBIGUOUS'
	| 'ROUTE_LAB_BUSY'
	| 'ROUTE_CANDIDATE_INVALID'
	| 'ROUTE_SANDBOX_START_FAILED'
	| 'ROUTE_CLEANUP_FAILED'
	| 'ROUTE_REQUEST_TIMEOUT'
	| 'ROUTE_EVIDENCE_INCOMPLETE'
	| 'ROUTE_ALREADY_TERMINAL'
	| 'ROUTE_LIMIT_EXCEEDED'
  | 'ACME_ACCOUNT_INVALID'
  | 'ACME_ACCOUNT_DEACTIVATED'
  | 'ACME_ORDER_FAILED'
  | 'ACME_RATE_LIMITED'
  | 'ACME_STAGING_PREFLIGHT_REQUIRED'
  | 'ACME_TERMS_REQUIRED'
  | 'CERTIFICATE_BINDING_CONFLICT'
  | 'CERTIFICATE_FILE_INVALID'
  | 'CERTIFICATE_IDENTIFIER_INVALID'
  | 'CERTIFICATE_KEY_MISMATCH'
  | 'CERTIFICATE_LIMIT_EXCEEDED'
  | 'CERTIFICATE_NEEDS_ATTENTION'
  | 'CERTIFICATE_OPERATION_TIMEOUT'
  | 'CERTIFICATE_PLAN_EXPIRED'
  | 'CERTIFICATE_PRIVATE_KEY_CONFIRMATION_REQUIRED'
  | 'CERTIFICATE_REFERENCED'
  | 'CERTIFICATE_RENEWAL_POLICY_INVALID'
  | 'CERTIFICATE_REQUEST_INVALID'
  | 'CERTIFICATE_RESOURCE_NOT_FOUND'
  | 'CERTIFICATE_SAN_MISMATCH'
  | 'CERTIFICATE_SERVER_AMBIGUOUS'
  | 'CERTIFICATE_SERVER_NOT_FOUND'
  | 'CERTIFICATE_SERVICE_UNAVAILABLE'
  | 'CERTIFICATE_TASK_ACTIVE'
  | 'CERTIFICATE_WILDCARD_REQUIRES_DNS'
  | 'CHALLENGE_CLEANUP_FAILED'
  | 'CLOUDFLARE_PERMISSION_DENIED'
  | 'CLOUDFLARE_TOKEN_INVALID'
  | 'CLOUDFLARE_UNAVAILABLE'
  | 'CLOUDFLARE_ZONE_NOT_FOUND'
  | 'DNS_PROPAGATION_TIMEOUT'

export interface APIError {
  code: APIErrorCode
  message: string
  request_id: string
  details?: Readonly<Record<string, string | number>>
}

export interface APIErrorEnvelope {
  error: APIError
}

export type NginxRuntimeState = 'running' | 'degraded' | 'stopped' | 'unknown'
export type AgentHealthState = 'healthy' | 'unavailable'
export type ProcessRole = 'master' | 'worker'
export type RecoveryResult = 'restarting' | 'invalid_config' | 'permanent_failure'

export interface SystemComponents {
  ui: 'healthy'
  agent: AgentHealthState
  nginx: NginxRuntimeState
}

export interface NginxProcess {
  pid: number
  role: ProcessRole
  started_at: string
}

export interface NginxBuild {
  version: string
  configure_arguments: string[]
}

export interface StartupValidation {
  valid: boolean
  checked_at: string
  exit_code: number | null
  diagnostic: string
}

export interface RecoveryStatus {
  count: number
  last_result: RecoveryResult
  permanent: boolean
}

export interface SystemStatusResponse {
  sampled_at: string
  components: SystemComponents
  master: NginxProcess | null
  workers: NginxProcess[]
  build: NginxBuild | null
  startup_validation: StartupValidation | null
  recovery: RecoveryStatus | null
  issues: string[]
}

export interface EffectiveConfigOccurrence {
  id: string
  load_order: number
  path: string
  content: string
}

export type EffectiveConfigWarning =
  | 'NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS'
  | 'NGINX_CONFIG_STRUCTURE_UNVERIFIED'

interface EffectiveConfigResponseBase {
  generated_at: string
  nginx_version: string
  entry_config_path: string
}

export interface StructuredEffectiveConfigResponse extends EffectiveConfigResponseBase {
  display_mode: 'structured'
  occurrence_count: number
  occurrences: EffectiveConfigOccurrence[]
  raw_content: null
  warnings: EffectiveConfigWarning[]
}

export interface RawEffectiveConfigResponse extends EffectiveConfigResponseBase {
  display_mode: 'raw'
  occurrence_count: 0
  occurrences: []
  raw_content: string
  warnings: EffectiveConfigWarning[]
}

export type EffectiveConfigResponse =
  | StructuredEffectiveConfigResponse
  | RawEffectiveConfigResponse

export type WorkspaceState = 'preparing' | 'ready' | 'stale' | 'published' | 'needs_attention'
export type EntryType = 'regular' | 'directory' | 'symlink' | 'special'
export type DiffStatus = 'unchanged' | 'created' | 'modified' | 'deleted'
export type ConfigStatusReason =
  | 'managed_text'
  | 'sensitive_material'
  | 'not_candidate'
  | 'invalid_text'
  | 'file_limit'
  | 'directory'
  | 'symlink_internal'
  | 'symlink_external'
  | 'symlink_unavailable'
  | 'special'
export type DependencyStatus =
  | 'resolved'
  | 'missing'
  | 'external'
  | 'unresolved'
  | 'symlink'
  | 'special'
  | 'cycle'

export interface WorkspaceSummary {
  id: string
  name: string
  state: WorkspaceState
  state_reason_code?: string
	last_release_id?: string
  production_digest: string
  base_digest: string
  draft_etag: string
  entry_count: number
  managed_bytes: number
  workspace_bytes: number
  created_by: number
  created_at: string
  updated_at: string
}

export type WorkspaceDetail = WorkspaceSummary

export interface ConfigTreeNode {
  path: string
  name: string
  entry_type: EntryType
  managed: boolean
  read_only: boolean
  status_reason_code: ConfigStatusReason
  size_bytes?: number
  content_digest?: string
  diff_status?: DiffStatus
  dependency_status?: DependencyStatus
  dependency_target_count?: number
  dependency_cycle?: boolean
}

export interface ConfigDependency {
  source: string
  line: number
  column: number
  display_value: string
  target?: string
  status: DependencyStatus
  cycle: boolean
}

export interface ConfigTree {
  entries: ConfigTreeNode[]
  dependencies: ConfigDependency[]
  draft_etag: string
}

export interface ConfigFile {
  path: string
  content: string
  size_bytes: number
  content_digest: string
  line_ending: 'none' | 'lf' | 'crlf' | 'mixed'
  draft_etag: string
}

export interface FileMutationResponse {
  workspace: WorkspaceDetail
  entry?: ConfigTreeNode
  draft_etag: string
}

export interface FileDiffSummary {
  path: string
  status: DiffStatus
  added_lines: number
  removed_lines: number
}

export interface DiffResponse {
  files: FileDiffSummary[]
  complete: boolean
  reason: '' | 'response_limit'
  patch: string
}

export interface SearchMatch {
  path: string
  line: number
  column: number
  snippet: string
}

export interface SearchResponse {
  matches: SearchMatch[]
  complete: boolean
}

export interface ConfigGroup {
  id: string
  name: string
  sort_order: number
  members: string[]
  missing: string[]
  created_by: number
  created_at: string
  updated_at: string
}

export interface GroupCollection {
  groups: ConfigGroup[]
  groups_etag: string
}

export interface GroupMutationRequest {
  name: string
  sort_order: number
  members: string[]
}

export type PublishCheckState = 'running' | 'valid' | 'invalid' | 'failed'

export interface CandidateDiagnostic {
	code: string
	path: string
	line: number
	summary: string
}

export interface PublishCheck {
	id: string
	workspace_id: string
	workspace_revision: number
	production_digest: string
	base_digest: string
	draft_digest: string
	candidate_digest: string
	manifest_version: number
	policy_version: number
	validator_version: number
	validator_build_id: string
	state: PublishCheckState
	diagnostic_count: number
	details: { diagnostics: CandidateDiagnostic[] }
	started_at: string
	finished_at: string
	expires_at: string
}

export type ReleaseState =
	| 'queued'
	| 'running'
	| 'rolling_back'
	| 'succeeded'
	| 'failed'
	| 'rolled_back'
	| 'needs_attention'
	| 'cancelled'

export type ReleaseStageName =
	| 'queued'
	| 'rechecking'
	| 'backup_creating'
	| 'backup_verified'
	| 'candidate_validated'
	| 'files_applying'
	| 'files_applied'
	| 'production_validated'
	| 'reload_requested'
	| 'runtime_confirmed'
	| 'committed'
	| 'rollback_applying'
	| 'rollback_files_restored'
	| 'rollback_validated'
	| 'rollback_reload_requested'
	| 'rolled_back'
	| 'failed'
	| 'needs_attention'

export type ReleaseStageResult = 'pending' | 'running' | 'success' | 'failed' | 'warning'

export interface ReleaseStage {
	sequence: number
	stage: ReleaseStageName
	result: ReleaseStageResult
	code?: string
	details: Readonly<Record<string, unknown>>
	occurred_at: string
}

export interface Release {
	id: string
	workspace_id: string
	check_id: string
	backup_id?: string
	state: ReleaseState
	stage: ReleaseStageName
	production_digest: string
	draft_digest: string
	candidate_digest: string
	last_error_code?: string
	created_at: string
	updated_at: string
	finished_at?: string
	stages: ReleaseStage[]
}

export interface CursorPage<T> {
  items: T[]
  next_cursor?: string
}

export type BackupOriginType = 'release' | 'restore'
export type BackupState = 'creating' | 'complete' | 'invalid' | 'deleting' | 'deleted'

export interface BackupProtectionReason {
  kind: string
  code: string
}

export interface ConfigBackup {
  id: string
  origin_type: BackupOriginType
  origin_id: string
  release_id?: string
  production_digest: string
  state: BackupState
  entry_count: number
  total_bytes: number
  body_present: boolean
  protected: boolean
  manually_protected: boolean
  protection_reason?: string
  protections: BackupProtectionReason[]
  created_at: string
  verified_at?: string
  deleted_at?: string
}

export type RestoreState =
  | 'queued'
  | 'running'
  | 'rolling_back'
  | 'succeeded'
  | 'failed'
  | 'rolled_back'
  | 'needs_attention'
  | 'cancelled'

export type RestoreStageName =
  | 'queued'
  | 'target_verifying'
  | 'target_validated'
  | 'safety_backup_creating'
  | 'safety_backup_verified'
  | 'files_restoring'
  | 'files_restored'
  | 'production_validated'
  | 'reload_requested'
  | 'runtime_confirmed'
  | 'succeeded'
  | 'rollback_applying'
  | 'rollback_files_restored'
  | 'rollback_validated'
  | 'rollback_reload_requested'
  | 'rolled_back'
  | 'failed'
  | 'needs_attention'

export interface RestoreStage {
  sequence: number
  stage: RestoreStageName
  result: ReleaseStageResult
  code?: string
  details: Readonly<Record<string, unknown>>
  occurred_at: string
}

export interface ConfigRestore {
  id: string
  target_backup_id: string
  safety_backup_id: string
  attention_case_id?: string
  state: RestoreState
  stage: RestoreStageName
  source_digest: string
  target_digest: string
  last_error_code?: string
  reason: string
  request_id: string
  created_at: string
  updated_at: string
  finished_at?: string
  stages: RestoreStage[]
}

export type RestartState =
  | 'queued'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'needs_attention'
  | 'cancelled'

export type RestartStageName =
  | 'queued'
  | 'production_validating'
  | 'runtime_sampling'
  | 'restart_requested'
  | 'runtime_confirming'
  | 'succeeded'
  | 'failed'
  | 'needs_attention'

export interface RestartStage {
  sequence: number
  stage: RestartStageName
  result: ReleaseStageResult
  code?: string
  details: Readonly<Record<string, unknown>>
  occurred_at: string
}

export interface NginxRestart {
  id: string
  attention_case_id?: string
  state: RestartState
  stage: RestartStageName
  production_digest: string
  before_master_pid?: number
  after_master_pid?: number
  worker_count: number
  http_status?: number
  last_error_code?: string
  reason: string
  request_id: string
  created_at: string
  updated_at: string
  finished_at?: string
  stages: RestartStage[]
}

export interface RetentionPolicy {
  minimum_complete: number
  maximum_complete: number
  maximum_total_bytes: number
  minimum_age_seconds: number
}

export type RetentionRunState =
  | 'planned'
  | 'executing'
  | 'succeeded'
  | 'failed'
  | 'needs_attention'
  | 'expired'

export type RetentionItemState =
  | 'planned'
  | 'kept'
  | 'deleting'
  | 'deleted'
  | 'skipped_protected'
  | 'failed'
  | 'needs_attention'

export interface RetentionItem {
  ordinal: number
  backup_id: string
  decision: 'keep' | 'delete'
  reason_code: string
  state: RetentionItemState
  snapshot_created_at: string
  snapshot_total_bytes: number
}

export interface RetentionRun {
  id: string
  state: RetentionRunState
  policy: RetentionPolicy
  backup_count: number
  total_bytes: number
  protected_count: number
  delete_count: number
  delete_bytes: number
  deleted_count: number
  deleted_bytes: number
  last_error_code?: string
  created_at: string
  expires_at: string
  started_at?: string
  finished_at?: string
  items: RetentionItem[]
}

export type AttentionCaseState = 'open' | 'resolved'
export type AttentionSubjectType = 'workspace' | 'release' | 'restore' | 'restart'

export interface AttentionCase {
  id: string
  subject_type: AttentionSubjectType
  subject_id: string
  workspace_id?: string
  backup_id?: string
  state: AttentionCaseState
  reason_code: string
  opened_at: string
  resolved_at?: string
  resolution_type?: 'restore' | 'restart' | 'verification'
  resolution_id?: string
}

export interface RuntimeVerification {
  id: string
  attention_case_id: string
  state: 'succeeded' | 'failed'
  production_digest: string
  master_pid?: number
  worker_count: number
  http_status?: number
  last_error_code?: string
  request_id: string
  created_at: string
  finished_at: string
}

export interface AuditEvent {
  id: number
  occurred_at: string
  actor_name: string
  action: string
  object_type: string
  object_id: string
  result: string
  request_id: string
  details: Readonly<Record<string, string | number | boolean>>
}

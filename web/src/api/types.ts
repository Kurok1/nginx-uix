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
  | 'CONFIG_WORKSPACE_CONFLICT'
  | 'CONFIG_WORKSPACE_STALE'
  | 'CONFIG_WORKSPACE_NEEDS_ATTENTION'
  | 'CONFIG_SNAPSHOT_CHANGED'
  | 'AGENT_UNAVAILABLE'
  | 'CONFIG_OPERATION_TIMEOUT'

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

export interface EffectiveConfigResponse {
  generated_at: string
  nginx_version: string
  entry_config_path: string
  occurrence_count: number
  occurrences: EffectiveConfigOccurrence[]
}

export type WorkspaceState = 'preparing' | 'ready' | 'stale' | 'needs_attention'
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

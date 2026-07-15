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
  }
  csrf_token: string
  idle_expires_at: string
  absolute_expires_at: string
}

export interface APIError {
  code: string
  message: string
  request_id: string
  details?: Readonly<Record<string, unknown>>
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

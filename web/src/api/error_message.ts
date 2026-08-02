/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 1.1.0
 */
import { appI18n, type AppI18n } from '../i18n'
import { APIRequestError } from './client'
import type { APIErrorCode } from './types'
import type { MessageSchema } from '../locales/en-US'

type APIErrorMessageKey = keyof MessageSchema['errors']['api']

const apiErrorMessageKeys = {
  invalid_request: 'invalidRequest',
  invalid_credentials: 'invalidCredentials',
  unauthenticated: 'authenticationRequired',
  origin_rejected: 'requestRejected',
  csrf_rejected: 'requestRejected',
  rate_limited: 'rateLimited',
  unsupported_media_type: 'unsupportedMedia',
  service_unavailable: 'serviceUnavailable',
  internal_error: 'internal',
  AUTH_SESSION_EXPIRED: 'sessionExpired',
  NGINX_CONFIG_INVALID: 'nginxInvalid',
  NGINX_COMMAND_TIMEOUT: 'timeout',
  NGINX_OUTPUT_TOO_LARGE: 'outputTooLarge',
  CONFIG_PATH_INVALID: 'invalidPath',
  CONFIG_ENTRY_NOT_MANAGED: 'notManaged',
  CONFIG_LIMIT_EXCEEDED: 'limitExceeded',
  CONFIG_WORKSPACE_NOT_FOUND: 'notFound',
  CONFIG_PUBLISH_CHECK_NOT_FOUND: 'notFound',
  CONFIG_RELEASE_NOT_FOUND: 'notFound',
  CONFIG_WORKSPACE_CONFLICT: 'conflict',
  CONFIG_WORKSPACE_STALE: 'stale',
  CONFIG_WORKSPACE_NEEDS_ATTENTION: 'needsAttention',
  CONFIG_SNAPSHOT_CHANGED: 'conflict',
  CONFIG_PRODUCTION_CHANGED: 'productionChanged',
  CONFIG_BACKUP_INVALID: 'invalidBackup',
  NGINX_HEALTH_UNAVAILABLE: 'serviceUnavailable',
  CONFIG_RELEASE_NEEDS_ATTENTION: 'needsAttention',
  AGENT_UNAVAILABLE: 'agentUnavailable',
  CONFIG_OPERATION_TIMEOUT: 'timeout',
  CONFIG_CANDIDATE_INVALID: 'nginxInvalid',
  CONFIG_NO_CHANGES: 'noChanges',
  CONFIG_PUBLISH_CHECK_EXPIRED: 'expired',
  CONFIG_PUBLISH_IN_PROGRESS: 'inProgress',
  CONFIG_OPERATION_IN_PROGRESS: 'inProgress',
  CONFIG_BACKUP_PROTECTED: 'protected',
  CONFIG_RETENTION_PLAN_EXPIRED: 'expired',
  CONFIG_ATTENTION_UNRESOLVED: 'unresolved',
  CONFIG_BACKUP_TARGET_INVALID: 'invalidBackup',
  CONFIG_RESTORE_NEEDS_ATTENTION: 'needsAttention',
  NGINX_RESTART_CONFIG_INVALID: 'nginxInvalid',
  NGINX_RESTART_FAILED: 'internal',
  NGINX_RESTART_NEEDS_ATTENTION: 'needsAttention',
  CONFIG_BACKUP_NOT_FOUND: 'notFound',
  CONFIG_RETENTION_RUN_NOT_FOUND: 'notFound',
  CONFIG_RESTORE_NOT_FOUND: 'notFound',
  NGINX_RESTART_NOT_FOUND: 'notFound',
  CONFIG_ATTENTION_CASE_NOT_FOUND: 'notFound',
  STRUCTURED_PARSE_FAILED: 'parseFailed',
  STRUCTURED_LIMIT_EXCEEDED: 'limitExceeded',
  STRUCTURED_PREVIEW_STALE: 'stale',
  STRUCTURED_CONTEXT_AMBIGUOUS: 'ambiguous',
  STRUCTURED_EDIT_CONFLICT: 'conflict',
  UPSTREAM_INVALID: 'invalidResource',
  UPSTREAM_DUPLICATE: 'duplicate',
  UPSTREAM_REFERENCED: 'referenced',
  UPSTREAM_REFERENCE_INCOMPLETE: 'incomplete',
  LOCATION_INVALID: 'invalidResource',
  LOCATION_DUPLICATE: 'duplicate',
  PROXY_PASS_INVALID: 'invalidResource',
  ROUTE_REQUEST_TOO_LARGE: 'limitExceeded',
  ROUTE_REQUEST_INVALID: 'invalidRequest',
  ROUTE_LAB_UNAVAILABLE: 'routeUnavailable',
  ROUTE_TEST_NOT_FOUND: 'notFound',
  ROUTE_WORKSPACE_CONFLICT: 'conflict',
  ROUTE_CONFIRMATION_REQUIRED: 'confirmationRequired',
  ROUTE_PROJECT_INCOMPLETE: 'incomplete',
  ROUTE_LISTENER_AMBIGUOUS: 'ambiguous',
  ROUTE_LAB_BUSY: 'busy',
  ROUTE_CANDIDATE_INVALID: 'nginxInvalid',
  ROUTE_SANDBOX_START_FAILED: 'routeUnavailable',
  ROUTE_CLEANUP_FAILED: 'cleanupFailed',
  ROUTE_REQUEST_TIMEOUT: 'timeout',
  ROUTE_EVIDENCE_INCOMPLETE: 'incomplete',
  ROUTE_ALREADY_TERMINAL: 'conflict',
  ROUTE_LIMIT_EXCEEDED: 'limitExceeded',
  ACME_ACCOUNT_INVALID: 'accountInvalid',
  ACME_ACCOUNT_DEACTIVATED: 'accountDeactivated',
  ACME_ORDER_FAILED: 'orderFailed',
  ACME_RATE_LIMITED: 'rateLimited',
  ACME_STAGING_PREFLIGHT_REQUIRED: 'confirmationRequired',
  ACME_TERMS_REQUIRED: 'termsRequired',
  CERTIFICATE_BINDING_CONFLICT: 'bindingConflict',
  CERTIFICATE_FILE_INVALID: 'fileInvalid',
  CERTIFICATE_IDENTIFIER_INVALID: 'identifierInvalid',
  CERTIFICATE_KEY_MISMATCH: 'keyMismatch',
  CERTIFICATE_LIMIT_EXCEEDED: 'limitExceeded',
  CERTIFICATE_NEEDS_ATTENTION: 'needsAttention',
  CERTIFICATE_OPERATION_TIMEOUT: 'timeout',
  CERTIFICATE_PLAN_EXPIRED: 'expired',
  CERTIFICATE_PRIVATE_KEY_CONFIRMATION_REQUIRED: 'privateKeyConfirmation',
  CERTIFICATE_REFERENCED: 'referenced',
  CERTIFICATE_RENEWAL_POLICY_INVALID: 'invalidRequest',
  CERTIFICATE_REQUEST_INVALID: 'invalidRequest',
  CERTIFICATE_RESOURCE_NOT_FOUND: 'notFound',
  CERTIFICATE_SAN_MISMATCH: 'sanMismatch',
  CERTIFICATE_SERVER_AMBIGUOUS: 'ambiguous',
  CERTIFICATE_SERVER_NOT_FOUND: 'notFound',
  CERTIFICATE_SERVICE_UNAVAILABLE: 'serviceUnavailable',
  CERTIFICATE_TASK_ACTIVE: 'inProgress',
  CERTIFICATE_WILDCARD_REQUIRES_DNS: 'wildcardRequiresDNS',
  CHALLENGE_CLEANUP_FAILED: 'cleanupFailed',
  CLOUDFLARE_PERMISSION_DENIED: 'cloudflarePermission',
  CLOUDFLARE_TOKEN_INVALID: 'cloudflareToken',
  CLOUDFLARE_UNAVAILABLE: 'cloudflareUnavailable',
  CLOUDFLARE_ZONE_NOT_FOUND: 'cloudflareZone',
  DNS_PROPAGATION_TIMEOUT: 'dnsTimeout',
} as const satisfies Record<APIErrorCode, APIErrorMessageKey>

export function apiRequestID(error: unknown): string {
  return error instanceof APIRequestError ? error.requestID ?? '' : ''
}

export function withAPIRequestID(
  message: string,
  error: unknown,
  i18n: AppI18n = appI18n,
): string {
  const requestId = apiRequestID(error)
  return requestId === ''
    ? message
    : i18n.global.t('errors.withRequestId', { message, requestId })
}

export function formatAPIRequestError(
  error: unknown,
  i18n: AppI18n = appI18n,
): string {
  if (!(error instanceof APIRequestError)) {
    return i18n.global.t('errors.unexpected')
  }

  let message: string
  if (error.apiError !== undefined) {
    const messageKey = apiErrorMessageKeys[error.apiError.code]
    message = i18n.global.t(`errors.api.${messageKey}`)
  } else if (error.kind === 'network') {
    message = i18n.global.t('errors.network')
  } else if (error.kind === 'malformed_response') {
    message = i18n.global.t('errors.malformedResponse')
  } else {
    message = i18n.global.t('errors.unexpected')
  }

  return withAPIRequestID(message, error, i18n)
}

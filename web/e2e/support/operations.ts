/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

import { expect, type Page, type Request, type Route } from '@playwright/test'

import type {
  AttentionCase,
  AuditEvent,
  ConfigBackup,
  ConfigRestore,
  NginxRestart,
  Release,
  RetentionRun,
} from '../../src/api/types'
import {
  appOrigin,
  authenticatedSession,
  csrfToken,
  healthyStatus,
  sessionCookieName,
  sessionCookieValue,
} from './api'

export const operationsBackupID = '11111111111111111111111111111111'
export const operationsReleaseID = '22222222222222222222222222222222'
export const operationsAttentionID = '33333333333333333333333333333333'
export const operationsRestartID = '44444444444444444444444444444444'
export const operationsRestoreID = '55555555555555555555555555555555'
export const operationsSafetyBackupID = '66666666666666666666666666666666'
export const operationsRetentionID = '77777777777777777777777777777777'

const historicRestartID = '88888888888888888888888888888888'
const sourceDigest = 'a'.repeat(64)
const targetDigest = 'b'.repeat(64)
const createdAt = '2026-07-19T08:00:00Z'
const finishedAt = '2026-07-19T08:00:05Z'

export interface OperationsAPIRequest {
  method: string
  path: string
  query: string
  body: string | null
  headers: Readonly<Record<string, string>>
}

export interface OperationsAPIHarness {
  assertContract(): void
  callsFor(method: string, path: string): OperationsAPIRequest[]
  requests(): ReadonlyArray<OperationsAPIRequest>
}

const backup = {
  id: operationsBackupID,
  origin_type: 'release',
  origin_id: operationsReleaseID,
  release_id: operationsReleaseID,
  production_digest: targetDigest,
  state: 'complete',
  entry_count: 4,
  total_bytes: 8192,
  body_present: true,
  protected: false,
  manually_protected: false,
  protections: [],
  created_at: createdAt,
  verified_at: '2026-07-19T08:00:01Z',
} satisfies ConfigBackup

const attentionCase = {
  id: operationsAttentionID,
  subject_type: 'restore',
  subject_id: '99999999999999999999999999999999',
  backup_id: operationsBackupID,
  state: 'open',
  reason_code: 'runtime_unknown',
  opened_at: '2026-07-19T07:59:00Z',
} satisfies AttentionCase

const release = {
  id: operationsReleaseID,
  workspace_id: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  check_id: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
  backup_id: operationsBackupID,
  state: 'succeeded',
  stage: 'committed',
  production_digest: sourceDigest,
  draft_digest: targetDigest,
  candidate_digest: targetDigest,
  created_at: '2026-07-19T07:55:00Z',
  updated_at: createdAt,
  finished_at: createdAt,
  stages: [{
    sequence: 1,
    stage: 'committed',
    result: 'success',
    details: {},
    occurred_at: createdAt,
  }],
} satisfies Release

const historicRestart = {
  id: historicRestartID,
  state: 'succeeded',
  stage: 'succeeded',
  production_digest: targetDigest,
  before_master_pid: 90,
  after_master_pid: 101,
  worker_count: 2,
  http_status: 200,
  reason: 'historic fixed restart',
  request_id: 'request-restart-history',
  created_at: '2026-07-19T07:50:00Z',
  updated_at: '2026-07-19T07:50:05Z',
  finished_at: '2026-07-19T07:50:05Z',
  stages: [{
    sequence: 1,
    stage: 'succeeded',
    result: 'success',
    details: {},
    occurred_at: '2026-07-19T07:50:05Z',
  }],
} satisfies NginxRestart

const auditEvent = {
  id: 1,
  occurred_at: createdAt,
  actor_name: 'admin',
  action: 'config.backup.created',
  object_type: 'backup',
  object_id: operationsBackupID,
  result: 'succeeded',
  request_id: 'request-backup-created',
  details: {
    entry_count: 4,
    body_present: true,
    reason_code: 'release_backup',
  },
} satisfies AuditEvent

export async function installOperationsAPIFixture(page: Page): Promise<OperationsAPIHarness> {
  const captured: OperationsAPIRequest[] = []
  const violations: string[] = []
  let attentionResolved = false
  let restartQueued = false
  let restartTerminal = false
  let restoreQueued = false
  let restoreTerminal = false
  let retentionState: RetentionRun['state'] | null = null

  await page.route(`${appOrigin}/api/v1/**`, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const headers = request.headers()
    captured.push({
      method: request.method(),
      path: url.pathname,
      query: url.search,
      body: request.postData(),
      headers,
    })

    if (url.origin !== appOrigin || url.hash !== '') {
      return rejectUnexpected(route, violations, `unexpected API URL: ${request.url()}`)
    }
    if (!hasSessionCookie(headers.cookie)) {
      return rejectUnexpected(
        route,
        violations,
        `missing HttpOnly session cookie: ${request.method()} ${url.pathname}`,
      )
    }

    if (request.method() === 'GET' && url.pathname === '/api/v1/auth/session') {
      requireQuery(url, {}, violations)
      return fulfillJSON(route, 200, authenticatedSession)
    }
    if (request.method() === 'GET' && url.pathname === '/api/v1/system/status') {
      requireQuery(url, {}, violations)
      return fulfillJSON(route, 200, healthyStatus)
    }
    if (request.method() === 'GET' && url.pathname === '/api/v1/config/attention-cases') {
      requireQuery(url, { state: 'open' }, violations)
      return fulfillJSON(route, 200, { items: attentionResolved ? [] : [attentionCase] })
    }
    if (request.method() === 'GET' && url.pathname === '/api/v1/config/backups') {
      requireQuery(url, { include_deleted: 'true' }, violations)
      return fulfillJSON(route, 200, { items: [backup] })
    }
    if (request.method() === 'GET' && url.pathname === '/api/v1/config/history/releases') {
      requireQuery(url, {}, violations)
      return fulfillJSON(route, 200, { items: [release] })
    }
    if (request.method() === 'GET' && url.pathname === '/api/v1/config/history/restores') {
      requireQuery(url, {}, violations)
      return fulfillJSON(route, 200, {
        items: restoreTerminal ? [terminalRestore()] : [],
      })
    }
    if (request.method() === 'GET' && url.pathname === '/api/v1/config/history/restarts') {
      requireQuery(url, {}, violations)
      return fulfillJSON(route, 200, {
        items: restartTerminal ? [terminalRestart(), historicRestart] : [historicRestart],
      })
    }
    if (request.method() === 'GET' && url.pathname === '/api/v1/config/audit-events') {
      requireQuery(url, {}, violations)
      return fulfillJSON(route, 200, { items: [auditEvent] })
    }

    if (request.method() === 'POST' && url.pathname === '/api/v1/nginx/restarts') {
      requireQuery(url, {}, violations)
      requireMutation(request, headers, ['attention_case_id', 'reason', 'confirmation'], violations)
      restartQueued = true
      return fulfillJSON(route, 202, queuedRestart(), {
        Location: `/api/v1/nginx/restarts/${operationsRestartID}`,
      })
    }
    if (request.method() === 'GET' && url.pathname === `/api/v1/nginx/restarts/${operationsRestartID}`) {
      requireQuery(url, {}, violations)
      return fulfillJSON(route, 200, restartTerminal ? terminalRestart() : queuedRestart())
    }
    if (request.method() === 'GET' && url.pathname === `/api/v1/nginx/restarts/${operationsRestartID}/events`) {
      requireQuery(url, {}, violations)
      if (!restartQueued) violations.push('restart progress stream opened before the restart was queued')
      restartTerminal = true
      return fulfillEventStream(route)
    }

    if (
      request.method() === 'POST' &&
      url.pathname === `/api/v1/config/backups/${operationsBackupID}/restores`
    ) {
      requireQuery(url, {}, violations)
      requireMutation(
        request,
        headers,
        ['attention_case_id', 'reason', 'confirm_backup_id'],
        violations,
      )
      restoreQueued = true
      return fulfillJSON(route, 202, queuedRestore(), {
        Location: `/api/v1/config/restores/${operationsRestoreID}`,
      })
    }
    if (request.method() === 'GET' && url.pathname === `/api/v1/config/restores/${operationsRestoreID}`) {
      requireQuery(url, {}, violations)
      return fulfillJSON(route, 200, restoreTerminal ? terminalRestore() : queuedRestore())
    }
    if (request.method() === 'GET' && url.pathname === `/api/v1/config/restores/${operationsRestoreID}/events`) {
      requireQuery(url, {}, violations)
      if (!restoreQueued) violations.push('restore progress stream opened before the restore was queued')
      restoreTerminal = true
      return fulfillEventStream(route)
    }

    if (request.method() === 'POST' && url.pathname === '/api/v1/config/backup-retention-runs') {
      requireQuery(url, {}, violations)
      requireMutation(request, headers, [], violations)
      retentionState = 'planned'
      return fulfillJSON(route, 201, retentionRun('planned'), {
        Location: `/api/v1/config/backup-retention-runs/${operationsRetentionID}`,
      })
    }
    if (
      request.method() === 'POST' &&
      url.pathname === `/api/v1/config/backup-retention-runs/${operationsRetentionID}/executions`
    ) {
      requireQuery(url, {}, violations)
      requireMutation(request, headers, ['confirmation'], violations)
      retentionState = 'executing'
      return fulfillJSON(route, 202, retentionRun('executing'), {
        Location: `/api/v1/config/backup-retention-runs/${operationsRetentionID}`,
      })
    }
    if (
      request.method() === 'GET' &&
      url.pathname === `/api/v1/config/backup-retention-runs/${operationsRetentionID}`
    ) {
      requireQuery(url, {}, violations)
      retentionState = retentionState === 'executing' ? 'succeeded' : retentionState
      return fulfillJSON(route, 200, retentionRun(retentionState ?? 'planned'))
    }

    if (
      request.method() === 'POST' &&
      url.pathname === `/api/v1/config/attention-cases/${operationsAttentionID}/verifications`
    ) {
      requireQuery(url, {}, violations)
      requireMutation(request, headers, [], violations)
      attentionResolved = true
      return fulfillJSON(route, 201, {
        id: 'cccccccccccccccccccccccccccccccc',
        attention_case_id: operationsAttentionID,
        state: 'succeeded',
        production_digest: targetDigest,
        master_pid: 101,
        worker_count: 2,
        http_status: 200,
        request_id: 'request-verification',
        created_at: createdAt,
        finished_at: finishedAt,
      })
    }

    return rejectUnexpected(
      route,
      violations,
      `unexpected API request: ${request.method()} ${url.pathname}${url.search}`,
    )
  })

  return {
    assertContract: () => expect(violations, 'Operations API fixture contract violations').toEqual([]),
    callsFor: (method, path) => captured.filter((call) =>
      call.method === method && call.path === path,
    ),
    requests: () => captured,
  }
}

function queuedRestart(): NginxRestart {
  return {
    id: operationsRestartID,
    state: 'queued',
    stage: 'queued',
    production_digest: targetDigest,
    worker_count: 0,
    reason: 'replace unhealthy master',
    request_id: 'request-restart',
    created_at: createdAt,
    updated_at: createdAt,
    stages: [{
      sequence: 1,
      stage: 'queued',
      result: 'running',
      details: {},
      occurred_at: createdAt,
    }],
  }
}

function terminalRestart(): NginxRestart {
  return {
    ...queuedRestart(),
    state: 'succeeded',
    stage: 'succeeded',
    before_master_pid: 101,
    after_master_pid: 201,
    worker_count: 2,
    http_status: 200,
    updated_at: finishedAt,
    finished_at: finishedAt,
    stages: [
      { sequence: 1, stage: 'queued', result: 'success', details: {}, occurred_at: createdAt },
      { sequence: 2, stage: 'succeeded', result: 'success', details: {}, occurred_at: finishedAt },
    ],
  }
}

function queuedRestore(): ConfigRestore {
  return {
    id: operationsRestoreID,
    target_backup_id: operationsBackupID,
    safety_backup_id: operationsSafetyBackupID,
    state: 'queued',
    stage: 'queued',
    source_digest: sourceDigest,
    target_digest: targetDigest,
    reason: 'restore verified recovery point',
    request_id: 'request-restore',
    created_at: createdAt,
    updated_at: createdAt,
    stages: [{
      sequence: 1,
      stage: 'queued',
      result: 'running',
      details: {},
      occurred_at: createdAt,
    }],
  }
}

function terminalRestore(): ConfigRestore {
  return {
    ...queuedRestore(),
    state: 'succeeded',
    stage: 'succeeded',
    updated_at: finishedAt,
    finished_at: finishedAt,
    stages: [
      { sequence: 1, stage: 'queued', result: 'success', details: {}, occurred_at: createdAt },
      { sequence: 2, stage: 'succeeded', result: 'success', details: {}, occurred_at: finishedAt },
    ],
  }
}

function retentionRun(state: RetentionRun['state']): RetentionRun {
  const terminal = state === 'succeeded'
  return {
    id: operationsRetentionID,
    state,
    policy: {
      minimum_complete: 1,
      maximum_complete: 10,
      maximum_total_bytes: 1048576,
      minimum_age_seconds: 86400,
    },
    backup_count: 2,
    total_bytes: 16384,
    protected_count: 1,
    delete_count: 1,
    delete_bytes: 8192,
    deleted_count: terminal ? 1 : 0,
    deleted_bytes: terminal ? 8192 : 0,
    created_at: createdAt,
    expires_at: '2026-07-19T08:05:00Z',
    ...(state === 'planned' ? {} : { started_at: '2026-07-19T08:00:02Z' }),
    ...(terminal ? { finished_at: finishedAt } : {}),
    items: [{
      ordinal: 1,
      backup_id: operationsBackupID,
      decision: 'delete',
      reason_code: 'maximum_count',
      state: terminal ? 'deleted' : state === 'executing' ? 'deleting' : 'planned',
      snapshot_created_at: createdAt,
      snapshot_total_bytes: 8192,
    }],
  }
}

function hasSessionCookie(cookie: string | undefined): boolean {
  return cookie?.split(';').some((part) =>
    part.trim() === `${sessionCookieName}=${sessionCookieValue}`,
  ) ?? false
}

function requireQuery(
  url: URL,
  expected: Readonly<Record<string, string>>,
  violations: string[],
): void {
  const actual = [...url.searchParams.entries()].sort(([left], [right]) => left.localeCompare(right))
  const wanted = Object.entries(expected).sort(([left], [right]) => left.localeCompare(right))
  if (JSON.stringify(actual) !== JSON.stringify(wanted)) {
    violations.push(`unexpected query for ${url.pathname}: ${url.search}`)
  }
}

function requireMutation(
  request: Request,
  headers: Readonly<Record<string, string>>,
  keys: readonly string[],
  violations: string[],
): void {
  if (headers['x-csrf-token'] !== csrfToken) {
    violations.push(`missing CSRF token: ${request.method()} ${new URL(request.url()).pathname}`)
  }
  if (headers['content-type'] !== 'application/json') {
    violations.push(`unexpected mutation content type: ${headers['content-type'] ?? 'missing'}`)
  }
  let body: unknown
  try {
    body = JSON.parse(request.postData() ?? '')
  } catch {
    violations.push(`mutation body is not JSON: ${request.method()} ${request.url()}`)
    return
  }
  if (typeof body !== 'object' || body === null || Array.isArray(body)) {
    violations.push(`mutation body is not an object: ${request.method()} ${request.url()}`)
    return
  }
  const actualKeys = Object.keys(body).sort()
  const expectedKeys = [...keys].sort()
  if (JSON.stringify(actualKeys) !== JSON.stringify(expectedKeys)) {
    violations.push(`unexpected mutation keys for ${request.url()}: ${actualKeys.join(',')}`)
  }
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
    headers: {
      'Cache-Control': 'no-store',
      'X-Request-ID': 'request-operations-e2e',
      ...headers,
    },
  })
}

async function fulfillEventStream(route: Route): Promise<void> {
  await route.fulfill({
    status: 200,
    contentType: 'text/event-stream',
    headers: { 'Cache-Control': 'no-store' },
    body: 'id: 1\nevent: terminal\ndata: {"state":"succeeded"}\n\n',
  })
}

async function rejectUnexpected(
  route: Route,
  violations: string[],
  message: string,
): Promise<void> {
  violations.push(message)
  await fulfillJSON(route, 500, {
    error: {
      code: 'internal_error',
      message: 'Unexpected fixture request',
      request_id: 'request-operations-e2e',
    },
  })
}

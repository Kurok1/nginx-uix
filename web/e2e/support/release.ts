/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
import { expect, type Page, type Request, type Route } from '@playwright/test'

import type { Release, ReleaseStage, ReleaseState, WorkspaceState } from '../../src/api/types'
import {
  appOrigin,
  csrfToken,
  sessionCookieName,
  sessionCookieValue,
  type WorkspaceAPIFixture,
  type WorkspaceAPIRequest,
} from './api'

export type TerminalReleaseOutcome = Extract<
  ReleaseState,
  'succeeded' | 'failed' | 'rolled_back' | 'needs_attention'
>

export interface ReleaseAPIFixture {
  checkId: string
  releaseId: string
  backupId: string
  requests: () => ReadonlyArray<WorkspaceAPIRequest>
  assertContract: () => void
}

interface ReleaseFixtureOptions {
  initialOutcome?: TerminalReleaseOutcome
  outcome?: TerminalReleaseOutcome
  terminalDelayMs?: number
}

const checkID = '11111111111111111111111111111111'
const releaseID = '22222222222222222222222222222222'
const backupID = '33333333333333333333333333333333'
const productionDigest = 'a'.repeat(64)
const baseDigest = 'b'.repeat(64)
const draftDigest = 'c'.repeat(64)
const candidateDigest = 'd'.repeat(64)

export async function installReleaseAPIFixture(
  page: Page,
  workspace: WorkspaceAPIFixture,
  options: ReleaseFixtureOptions = {},
): Promise<ReleaseAPIFixture> {
  const captured: WorkspaceAPIRequest[] = []
  const violations: string[] = []
  const startedAt = new Date(Date.now() - 2_000)
  const finishedAt = new Date(startedAt.getTime() + 1_000)
  const expiresAt = new Date(Date.now() + 10 * 60_000)
  let checkCreated = false
  let releaseCreated = options.initialOutcome !== undefined
  let outcome: TerminalReleaseOutcome | null = options.initialOutcome ?? null

  if (outcome !== null) applyWorkspaceOutcome(workspace, outcome)

  await page.route(`${appOrigin}/api/v1/config/**`, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (!isReleaseFixturePath(url.pathname, workspace.workspaceId)) {
      await route.fallback()
      return
    }

    const headers = request.headers()
    captured.push({
      method: request.method(),
      path: url.pathname,
      query: url.search,
      ifMatch: headers['if-match'] ?? null,
      csrf: headers['x-csrf-token'] ?? null,
      body: request.postData(),
      headers,
    })
    requireAuthenticatedRequest(request, url, violations)

    const publishCheckPath = `/api/v1/config/workspaces/${workspace.workspaceId}/publish-checks`
    const publishCheckReadPath = `/api/v1/config/publish-checks/${checkID}`
    const releasesPath = `/api/v1/config/workspaces/${workspace.workspaceId}/releases`
    const releasePath = `/api/v1/config/releases/${releaseID}`
    const eventsPath = `${releasePath}/events`

    if (url.pathname === publishCheckPath && request.method() === 'POST') {
      requireMutation(request, workspace.currentDraftETag(), violations)
      requireExactBody(request, [], violations)
      checkCreated = true
      await fulfillJSON(route, 201, publishCheck(startedAt, finishedAt, expiresAt))
      return
    }
    if (url.pathname === publishCheckReadPath && request.method() === 'GET' && checkCreated) {
      await fulfillJSON(route, 200, publishCheck(startedAt, finishedAt, expiresAt))
      return
    }
    if (url.pathname === releasesPath && request.method() === 'POST') {
      requireMutation(request, workspace.currentDraftETag(), violations)
      const body = requireExactBody(request, ['check_id', 'confirm_name'], violations)
      if (body?.check_id !== checkID || body.confirm_name !== 'E2E workspace') {
        violations.push('release confirmation body did not bind the checked workspace name')
      }
      if (!checkCreated) violations.push('release was queued without a publication check')
      releaseCreated = true
      outcome = null
      await fulfillJSON(route, 202, releaseProjection(null, startedAt), {
        Location: releasePath,
      })
      return
    }
    if (url.pathname === releasePath && request.method() === 'GET' && releaseCreated) {
      await fulfillJSON(route, 200, releaseProjection(outcome, startedAt))
      return
    }
    if (url.pathname === eventsPath && request.method() === 'GET' && releaseCreated) {
      const terminal = options.outcome ?? 'succeeded'
      if (outcome === null) {
        await waitForTerminal(options.terminalDelayMs ?? 350)
        outcome = terminal
        applyWorkspaceOutcome(workspace, terminal)
      }
      const projection = releaseProjection(outcome, startedAt)
      const sequence = projection.stages.at(-1)?.sequence ?? 1
      await route.fulfill({
        status: 200,
        headers: {
          'Cache-Control': 'no-store',
          'Content-Type': 'text/event-stream; charset=utf-8',
          'X-Accel-Buffering': 'no',
        },
        body: `id: ${sequence}\nevent: terminal\ndata: {"release_id":"${releaseID}"}\n\n`,
      })
      return
    }

    violations.push(`unexpected release API request: ${request.method()} ${url.pathname}`)
    await fulfillJSON(route, 404, {
      error: { code: 'CONFIG_RELEASE_NOT_FOUND', message: 'Release not found', request_id: 'e2e-release' },
    })
  })

  return {
    checkId: checkID,
    releaseId: releaseID,
    backupId: backupID,
    requests: () => captured,
    assertContract: () => expect(violations, 'release API contract violations').toEqual([]),
  }
}

function publishCheck(startedAt: Date, finishedAt: Date, expiresAt: Date) {
  return {
    id: checkID,
    workspace_id: '0123456789abcdef0123456789abcdef',
    workspace_revision: 1,
    production_digest: productionDigest,
    base_digest: baseDigest,
    draft_digest: draftDigest,
    candidate_digest: candidateDigest,
    manifest_version: 1,
    policy_version: 1,
    validator_version: 1,
    validator_build_id: 'nginx-1.30.3-e2e',
    state: 'valid',
    diagnostic_count: 0,
    details: { diagnostics: [] },
    started_at: startedAt.toISOString(),
    finished_at: finishedAt.toISOString(),
    expires_at: expiresAt.toISOString(),
  }
}

function releaseProjection(outcome: TerminalReleaseOutcome | null, startedAt: Date): Release {
  const stages = outcome === null ? [stage(1, 'queued', 'pending', startedAt)] : terminalStages(outcome, startedAt)
  const terminalStage = stages.at(-1)
  const updatedAt = terminalStage?.occurred_at ?? startedAt.toISOString()
  return {
    id: releaseID,
    workspace_id: '0123456789abcdef0123456789abcdef',
    check_id: checkID,
    ...(outcome === 'failed' || outcome === null ? {} : { backup_id: backupID }),
    state: outcome ?? 'queued',
    stage: terminalStage?.stage ?? 'queued',
    production_digest: productionDigest,
    draft_digest: draftDigest,
    candidate_digest: candidateDigest,
    ...(outcome === 'failed' ? { last_error_code: 'candidate_recheck_failed' } : {}),
    ...(outcome === 'rolled_back' ? { last_error_code: 'reload_failed' } : {}),
    ...(outcome === 'needs_attention' ? { last_error_code: 'runtime_state_uncertain' } : {}),
    created_at: startedAt.toISOString(),
    updated_at: updatedAt,
    ...(outcome === null ? {} : { finished_at: updatedAt }),
    stages,
  }
}

function terminalStages(outcome: TerminalReleaseOutcome, startedAt: Date): ReleaseStage[] {
  const successPath: Array<[ReleaseStage['stage'], ReleaseStage['result']]> = [
    ['queued', 'pending'],
    ['rechecking', 'success'],
    ['backup_creating', 'success'],
    ['backup_verified', 'success'],
    ['candidate_validated', 'success'],
    ['files_applying', 'success'],
    ['files_applied', 'success'],
    ['production_validated', 'success'],
    ['reload_requested', 'success'],
    ['runtime_confirmed', 'success'],
  ]
  if (outcome === 'succeeded') successPath.push(['committed', 'success'])
  if (outcome === 'failed') {
    return [
      stage(1, 'queued', 'pending', startedAt),
      stage(2, 'rechecking', 'failed', startedAt, 'candidate_recheck_failed'),
      stage(3, 'failed', 'failed', startedAt, 'candidate_recheck_failed'),
    ]
  }
  if (outcome === 'rolled_back') {
    successPath.push(
      ['rollback_applying', 'warning'],
      ['rollback_files_restored', 'success'],
      ['rollback_validated', 'success'],
      ['rollback_reload_requested', 'success'],
      ['rolled_back', 'warning'],
    )
  }
  if (outcome === 'needs_attention') successPath.push(['needs_attention', 'failed'])
  return successPath.map(([name, result], index) =>
    stage(
      index + 1,
      name,
      result,
      startedAt,
      name === 'needs_attention' ? 'runtime_state_uncertain' : undefined,
    ),
  )
}

function stage(
  sequence: number,
  name: ReleaseStage['stage'],
  result: ReleaseStage['result'],
  startedAt: Date,
  code?: string,
): ReleaseStage {
  return {
    sequence,
    stage: name,
    result,
    ...(code === undefined ? {} : { code }),
    details: {},
    occurred_at: new Date(startedAt.getTime() + sequence * 1_000).toISOString(),
  }
}

function applyWorkspaceOutcome(
  workspace: WorkspaceAPIFixture,
  outcome: TerminalReleaseOutcome,
): void {
  const states: Record<TerminalReleaseOutcome, WorkspaceState> = {
    succeeded: 'published',
    failed: 'ready',
    rolled_back: 'ready',
    needs_attention: 'needs_attention',
  }
  workspace.setWorkspaceState(states[outcome])
}

function isReleaseFixturePath(path: string, workspaceID: string): boolean {
  return (
    path === `/api/v1/config/workspaces/${workspaceID}/publish-checks` ||
    path === `/api/v1/config/publish-checks/${checkID}` ||
    path === `/api/v1/config/workspaces/${workspaceID}/releases` ||
    path === `/api/v1/config/releases/${releaseID}` ||
    path === `/api/v1/config/releases/${releaseID}/events`
  )
}

function requireAuthenticatedRequest(request: Request, url: URL, violations: string[]): void {
  if (url.search !== '' || url.hash !== '') violations.push(`release request contained query or fragment: ${url.href}`)
  const cookie = request.headers().cookie ?? ''
  if (!cookie.includes(`${sessionCookieName}=${sessionCookieValue}`)) {
    violations.push(`release request did not include the HttpOnly session cookie: ${request.method()} ${url.pathname}`)
  }
}

function requireMutation(request: Request, etag: string, violations: string[]): void {
  const headers = request.headers()
  if (headers.origin !== appOrigin) violations.push(`release mutation Origin was ${String(headers.origin)}`)
  if (headers['content-type'] !== 'application/json') {
    violations.push(`release mutation Content-Type was ${String(headers['content-type'])}`)
  }
  if (headers['x-csrf-token'] !== csrfToken) {
    violations.push(`release mutation CSRF token was ${String(headers['x-csrf-token'])}`)
  }
  if (headers['if-match'] !== etag) {
    violations.push(`release mutation If-Match was ${String(headers['if-match'])}, expected ${etag}`)
  }
}

function requireExactBody(
  request: Request,
  keys: readonly string[],
  violations: string[],
): Readonly<Record<string, unknown>> | null {
  let body: unknown
  try {
    body = request.postDataJSON()
  } catch {
    violations.push(`release mutation body was not JSON: ${String(request.postData())}`)
    return null
  }
  if (body === null || typeof body !== 'object' || Array.isArray(body)) {
    violations.push('release mutation body was not an object')
    return null
  }
  const record = body as Readonly<Record<string, unknown>>
  if (JSON.stringify(Object.keys(record).sort()) !== JSON.stringify([...keys].sort())) {
    violations.push(`release mutation keys were ${Object.keys(record).sort().join(',')}, expected ${[...keys].sort().join(',')}`)
  }
  return record
}

async function fulfillJSON(
  route: Route,
  status: number,
  body: unknown,
  headers: Readonly<Record<string, string>> = {},
): Promise<void> {
  await route.fulfill({
    status,
    headers: {
      'Cache-Control': 'no-store',
      'Content-Type': 'application/json; charset=utf-8',
      ...headers,
    },
    body: JSON.stringify(body),
  })
}

async function waitForTerminal(delayMs: number): Promise<void> {
  await new Promise<void>((resolve) => {
    setTimeout(resolve, delayMs)
  })
}

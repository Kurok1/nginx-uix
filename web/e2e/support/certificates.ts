/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */
import { expect, type Page, type Route } from '@playwright/test'

import type {
  ACMEAccount,
  ACMEDirectory,
  CertificateOrderPlan,
  CertificateRecord,
  CertificateServerCandidate,
  CertificateTask,
  DNSCredential,
} from '../../src/api/certificates'
import {
  appOrigin,
  authenticatedSession,
  csrfToken,
  sessionCookieName,
  sessionCookieValue,
} from './api'

export const stagingAccountID = '1'.repeat(32)
export const productionAccountID = '2'.repeat(32)
export const dnsCredentialID = '3'.repeat(32)
export const certificateID = '4'.repeat(32)
export const certificateVersionID = '5'.repeat(32)
export const certificateBindingID = '6'.repeat(32)
export const certificatePlanID = '7'.repeat(32)
export const certificateTaskID = '8'.repeat(32)
export const productionRiskPhrase = 'ISSUE PRODUCTION CERTIFICATE WITHOUT STAGING'
export const fixtureTime = '2026-07-22T08:00:00Z'

const directories: ACMEDirectory[] = [
  {
    environment: 'staging',
    directory_url: 'https://acme-staging-v02.api.letsencrypt.org/directory',
    terms_url: 'https://letsencrypt.org/repository/',
    external_account_required: false,
  },
  {
    environment: 'production',
    directory_url: 'https://acme-v02.api.letsencrypt.org/directory',
    terms_url: 'https://letsencrypt.org/repository/',
    external_account_required: false,
  },
]

const initialAccounts: ACMEAccount[] = [
  {
    id: stagingAccountID,
    environment: 'staging',
    directory_url: directories[0]!.directory_url,
    account_uri: 'https://acme-staging-v02.api.letsencrypt.org/acme/acct/10',
    email: 'staging-ops@example.test',
    status: 'valid',
    terms_url: directories[0]!.terms_url,
    terms_agreed_at: fixtureTime,
    terms_agreed_by: 1,
    created_at: fixtureTime,
    updated_at: fixtureTime,
  },
  {
    id: productionAccountID,
    environment: 'production',
    directory_url: directories[1]!.directory_url,
    account_uri: 'https://acme-v02.api.letsencrypt.org/acme/acct/20',
    email: 'production-ops@example.test',
    status: 'valid',
    terms_url: directories[1]!.terms_url,
    terms_agreed_at: fixtureTime,
    terms_agreed_by: 1,
    created_at: fixtureTime,
    updated_at: fixtureTime,
  },
]

const initialCredential: DNSCredential = {
  id: dnsCredentialID,
  name: 'Restricted example.test zone',
  provider: 'cloudflare',
  fingerprint: '0123456789abcdef',
  status: 'valid',
  verified_at: fixtureTime,
  created_at: fixtureTime,
  updated_at: fixtureTime,
}

export const certificateServerCandidate: CertificateServerCandidate = {
  ref: {
    path: 'conf.d/example.conf',
    start_offset: 48,
    server_names: ['example.test', '*.example.test'],
    listeners: ['443 ssl'],
    fingerprint: 'a'.repeat(64),
  },
  start_line: 3,
  start_column: 1,
  tls_enabled: true,
  editable: true,
}

const certificate: CertificateRecord = {
  id: certificateID,
  primary_identifier: 'example.test',
  identifiers: ['example.test', 'www.example.test'],
  challenge: 'cloudflare_dns_01',
  account_id: productionAccountID,
  dns_credential_id: dnsCredentialID,
  state: 'active',
  active_version_id: certificateVersionID,
  auto_renew: true,
  renew_before_seconds: 2_592_000,
  next_renewal_at: '2026-08-22T08:00:00Z',
  retry_count: 0,
  not_before: fixtureTime,
  not_after: '2026-10-20T08:00:00Z',
  created_at: fixtureTime,
  updated_at: fixtureTime,
  versions: [
    {
      id: certificateVersionID,
      state: 'active',
      leaf_fingerprint: 'b'.repeat(64),
      serial_number: '01AB',
      issuer: "Let's Encrypt E2E issuer",
      not_before: fixtureTime,
      not_after: '2026-10-20T08:00:00Z',
      created_at: fixtureTime,
    },
  ],
  bindings: [
    {
      id: certificateBindingID,
      version_id: certificateVersionID,
      config_path: certificateServerCandidate.ref.path,
      server_start_offset: certificateServerCandidate.ref.start_offset,
      server_names: certificateServerCandidate.ref.server_names,
      listeners: certificateServerCandidate.ref.listeners,
      server_fingerprint: certificateServerCandidate.ref.fingerprint,
      created_at: fixtureTime,
      updated_at: fixtureTime,
    },
  ],
}

export const certificateOrderPlan: CertificateOrderPlan = {
  id: certificatePlanID,
  state: 'planned',
  environment: 'production',
  challenge: 'cloudflare_dns_01',
  account_id: productionAccountID,
  staging_account_id: stagingAccountID,
  dns_credential_id: dnsCredentialID,
  certificate_id: '9'.repeat(32),
  primary_identifier: '*.example.test',
  identifiers: ['*.example.test'],
  server_refs: [certificateServerCandidate.ref],
  binding_diff: [
    {
      path: certificateServerCandidate.ref.path,
      patch: '@@ -3,0 +4,2 @@\n+ssl_certificate /var/lib/nginx-uix/certificates/public.pem;\n+ssl_certificate_key /var/lib/nginx-uix/certificates/private.pem;\n',
      added_lines: 2,
      removed_lines: 0,
    },
  ],
  production_digest: 'c'.repeat(64),
  staging_evidence: false,
  requires_risk_confirmation: true,
  risk_confirmation_phrase: productionRiskPhrase,
  expires_at: '2026-07-22T08:10:00Z',
  created_at: fixtureTime,
}

const completedTask: CertificateTask = {
  id: certificateTaskID,
  kind: 'issue',
  state: 'succeeded',
  stage: 'completed',
  plan_id: certificatePlanID,
  certificate_id: certificateOrderPlan.certificate_id,
  account_id: productionAccountID,
  dns_credential_id: dnsCredentialID,
  challenge: 'cloudflare_dns_01',
  release_id: 'not_required',
  created_at: fixtureTime,
  updated_at: '2026-07-22T08:02:00Z',
  started_at: fixtureTime,
  finished_at: '2026-07-22T08:02:00Z',
  stages: [
    {
      sequence: 1,
      stage: 'completed',
      result: 'success',
      details: { certificate_id: certificateOrderPlan.certificate_id },
      occurred_at: '2026-07-22T08:02:00Z',
    },
  ],
}

export interface CertificateAPIRequest {
  body: unknown
  headers: Readonly<Record<string, string>>
  method: string
  path: string
  query: string
}

export interface CertificateAPIFixture {
  assertContract: () => void
  callsFor: (method: string, path: string) => CertificateAPIRequest[]
  requests: () => readonly CertificateAPIRequest[]
}

export async function installCertificateAPIFixture(page: Page): Promise<CertificateAPIFixture> {
  let accounts = initialAccounts.map((account) => ({ ...account }))
  let credentials = [{ ...initialCredential }]
  let tasks: CertificateTask[] = []
  const requests: CertificateAPIRequest[] = []
  const violations: string[] = []

  await page.route(`${appOrigin}/api/v1/**`, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()
    const body = request.postData() === null ? null : parseRequestBody(request.postData(), violations)
    const captured = {
      body,
      headers: request.headers(),
      method,
      path: url.pathname,
      query: url.search,
    }
    requests.push(captured)
    validateRequest(captured, violations)

    if (method === 'GET' && url.pathname === '/api/v1/auth/session' && url.search === '') {
      await fulfillJSON(route, 200, authenticatedSession)
      return
    }
    if (method === 'GET' && url.pathname === '/api/v1/acme/directories' && url.search === '') {
      await fulfillJSON(route, 200, { directories })
      return
    }
    if (method === 'GET' && url.pathname === '/api/v1/acme/accounts' && url.search === '') {
      await fulfillJSON(route, 200, { accounts })
      return
    }
    if (method === 'GET' && url.pathname === '/api/v1/certificate-dns-credentials' && url.search === '') {
      await fulfillJSON(route, 200, { credentials })
      return
    }
    if (method === 'GET' && url.pathname === '/api/v1/certificate-server-candidates' && url.search === '') {
      await fulfillJSON(route, 200, { candidates: [certificateServerCandidate] })
      return
    }
    if (method === 'GET' && url.pathname === '/api/v1/certificates' && url.search === '?limit=100') {
      await fulfillJSON(route, 200, { certificates: [certificate] })
      return
    }
    if (method === 'GET' && url.pathname === `/api/v1/certificates/${certificateID}` && url.search === '') {
      await fulfillJSON(route, 200, certificate)
      return
    }
    if (method === 'GET' && url.pathname === '/api/v1/certificate-tasks' && url.search === '?limit=100') {
      await fulfillJSON(route, 200, { tasks })
      return
    }
    if (method === 'GET' && url.pathname === `/api/v1/certificate-tasks/${certificateTaskID}` && url.search === '') {
      await fulfillJSON(route, 200, completedTask)
      return
    }
    if (method === 'GET' && url.pathname === `/api/v1/certificate-tasks/${certificateTaskID}/events` && url.search === '') {
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        headers: { 'Cache-Control': 'no-store' },
        body: `data: ${JSON.stringify(completedTask.stages[0])}\n\n`,
      })
      return
    }
    if (method === 'POST' && url.pathname === '/api/v1/certificate-dns-credentials' && url.search === '') {
      const input = recordBody(body)
      const created: DNSCredential = {
        id: 'd'.repeat(32),
        name: typeof input?.name === 'string' ? input.name : 'Invalid fixture input',
        provider: 'cloudflare',
        fingerprint: 'fedcba9876543210',
        status: 'valid',
        verified_at: fixtureTime,
        created_at: fixtureTime,
        updated_at: fixtureTime,
      }
      credentials = [created, ...credentials]
      await fulfillJSON(route, 201, created, {
        Location: `/api/v1/certificate-dns-credentials/${created.id}`,
      })
      return
    }
    if (method === 'POST' && url.pathname === '/api/v1/certificate-order-plans' && url.search === '') {
      await fulfillJSON(route, 201, certificateOrderPlan, {
        Location: `/api/v1/certificate-order-plans/${certificatePlanID}`,
      })
      return
    }
    if (method === 'POST' && url.pathname === `/api/v1/certificate-order-plans/${certificatePlanID}/executions` && url.search === '') {
      tasks = [completedTask]
      await fulfillJSON(route, 202, completedTask, {
        Location: `/api/v1/certificate-tasks/${certificateTaskID}`,
      })
      return
    }
    const deactivation = url.pathname.match(/^\/api\/v1\/acme\/accounts\/([0-9a-f]{32})\/deactivations$/)
    if (method === 'POST' && deactivation !== null && url.search === '') {
      const id = deactivation[1]
      const current = accounts.find((account) => account.id === id)
      if (current !== undefined) {
        const updated: ACMEAccount = { ...current, status: 'deactivated', updated_at: fixtureTime }
        accounts = accounts.map((account) => account.id === id ? updated : account)
        await fulfillJSON(route, 200, updated)
        return
      }
    }

    violations.push(`unexpected request: ${method} ${url.pathname}${url.search}`)
    await fulfillJSON(route, 500, {
      error: {
        code: 'internal_error',
        message: 'Unexpected certificate fixture request',
        request_id: 'request-certificate-e2e',
      },
    })
  })

  return {
    assertContract: () => expect(violations, 'certificate API contract violations').toEqual([]),
    callsFor: (method, path) => requests.filter((request) => request.method === method && request.path === path),
    requests: () => requests,
  }
}

function parseRequestBody(value: string | null, violations: string[]): unknown {
  if (value === null || value === '') return null
  try {
    return JSON.parse(value) as unknown
  } catch {
    violations.push('request body was not valid JSON')
    return null
  }
}

function recordBody(value: unknown): Readonly<Record<string, unknown>> | null {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? value as Readonly<Record<string, unknown>>
    : null
}

function validateRequest(request: CertificateAPIRequest, violations: string[]): void {
  if (!request.headers.cookie?.includes(`${sessionCookieName}=${sessionCookieValue}`)) {
    violations.push(`${request.method} ${request.path} omitted the session cookie`)
  }
  if (request.method === 'GET') {
    if (request.headers['x-csrf-token'] !== undefined) {
      violations.push(`GET ${request.path} unexpectedly sent a CSRF token`)
    }
    return
  }
  if (request.headers.origin !== appOrigin) {
    violations.push(`${request.method} ${request.path} sent Origin ${String(request.headers.origin)}`)
  }
  if (request.headers['x-csrf-token'] !== csrfToken) {
    violations.push(`${request.method} ${request.path} sent CSRF ${String(request.headers['x-csrf-token'])}`)
  }
  if (request.headers['content-type'] !== 'application/json') {
    violations.push(`${request.method} ${request.path} sent Content-Type ${String(request.headers['content-type'])}`)
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
      'X-Request-ID': 'request-certificate-e2e',
      ...headers,
    },
  })
}

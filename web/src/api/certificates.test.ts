/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

import {
  certificateTaskEventsPath,
  parseCertificateOrderPlan,
  parseCertificateTaskStageEvent,
  parseDNSCredentials,
} from './certificates'
import { APIClient } from './client'

const credentialID = '1'.repeat(32)
const accountID = '2'.repeat(32)
const certificateID = '3'.repeat(32)
const planID = '4'.repeat(32)
const fingerprint = 'a'.repeat(64)

describe('certificate API contracts', () => {
  it('accepts only secret-free Cloudflare credential metadata', () => {
    const response = {
      credentials: [
        {
          id: credentialID,
          name: 'Production zones',
          provider: 'cloudflare',
          fingerprint: '0123456789abcdef',
          status: 'valid',
          verified_at: '2026-07-21T08:00:00Z',
          last_used_at: '2026-07-21T09:00:00Z',
          created_at: '2026-07-21T08:00:00Z',
          updated_at: '2026-07-21T09:00:00Z',
        },
      ],
    }

    expect(parseDNSCredentials(response, 200)).toEqual(response.credentials)
    expect(() =>
      parseDNSCredentials(
        {
          credentials: [{ ...response.credentials[0], api_token: 'cf-secret-token' }],
        },
        200,
      ),
    ).toThrow('API response was malformed')
  })

  it('submits a Cloudflare Token once through the authenticated mutation boundary', async () => {
    const response = {
      id: credentialID,
      name: 'Production zones',
      provider: 'cloudflare',
      fingerprint: '0123456789abcdef',
      status: 'valid',
      verified_at: '2026-07-21T08:00:00Z',
      created_at: '2026-07-21T08:00:00Z',
      updated_at: '2026-07-21T08:00:00Z',
    }
    const fetcher = vi.fn(async () =>
      new Response(JSON.stringify(response), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const client = new APIClient(fetcher)

    const credential = await client.createCertificateDNSCredential(
      { name: 'Production zones', api_token: 'cf-secret-token' },
      'csrf-token',
    )

    expect(credential).toEqual(response)
    expect(JSON.stringify(credential)).not.toContain('cf-secret-token')
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/certificate-dns-credentials',
      expect.objectContaining({
        method: 'POST',
        credentials: 'same-origin',
        cache: 'no-store',
        headers: expect.objectContaining({
          'Content-Type': 'application/json',
          'X-CSRF-Token': 'csrf-token',
        }),
        body: JSON.stringify({ name: 'Production zones', api_token: 'cf-secret-token' }),
      }),
    )
  })

  it('returns a short-lived certificate export without attempting JSON parsing', async () => {
    const pem = '-----BEGIN CERTIFICATE-----\npublic\n-----END CERTIFICATE-----\n'
    const fetcher = vi.fn(async () =>
      new Response(pem, {
        status: 200,
        headers: {
          'Content-Type': 'application/x-pem-file',
          'Content-Disposition': `attachment; filename="certificate-${certificateID}.pem"`,
          'Content-Length': String(pem.length),
          'Cache-Control': 'no-store',
        },
      }),
    )
    const client = new APIClient(fetcher)

    const exported = await client.exportCertificate(
      certificateID,
      {
        confirmation: certificateID,
        include_private_key: false,
        private_key_confirmation: '',
      },
      'csrf-token',
    )

    expect(exported.filename).toBe(`certificate-${certificateID}.pem`)
    expect(await exported.blob.text()).toBe(pem)
  })

  it('parses a complete digest-bound issuance review', () => {
    const plan = {
      id: planID,
      state: 'planned',
      environment: 'production',
      challenge: 'cloudflare_dns_01',
      account_id: accountID,
      staging_account_id: '5'.repeat(32),
      dns_credential_id: credentialID,
      certificate_id: certificateID,
      primary_identifier: '*.example.com',
      identifiers: ['*.example.com', 'example.com'],
      server_refs: [],
      binding_diff: [],
      production_digest: fingerprint,
      staging_evidence: false,
      requires_risk_confirmation: true,
      risk_confirmation_phrase: 'PRODUCTION WITHOUT STAGING',
      expires_at: '2026-07-21T08:10:00Z',
      created_at: '2026-07-21T08:00:00Z',
    }

    expect(parseCertificateOrderPlan(plan, 201)).toEqual(plan)
  })

  it('validates task event IDs and safe SSE stage payloads', () => {
    const taskID = '6'.repeat(32)
    const stage = {
      sequence: 7,
      stage: 'propagating',
      result: 'running',
      details: {},
      occurred_at: '2026-07-21T08:03:00Z',
    }

    expect(certificateTaskEventsPath(taskID)).toBe(`/api/v1/certificate-tasks/${taskID}/events`)
    expect(parseCertificateTaskStageEvent(JSON.stringify(stage), 200)).toEqual(stage)
    expect(() => certificateTaskEventsPath('../secret')).toThrow('invalid certificate task id')
    expect(() =>
      parseCertificateTaskStageEvent(JSON.stringify({ ...stage, token: 'secret' }), 200),
    ).toThrow('API response was malformed')
  })
})

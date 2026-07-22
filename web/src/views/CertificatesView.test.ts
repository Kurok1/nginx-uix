/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

import { flushPromises, mount } from '@vue/test-utils'

import type {
  ACMEAccount,
  ACMEDirectory,
  CertificateOrderPlan,
  CertificateRecord,
  CertificateServerCandidate,
  CertificateTask,
  DNSCredential,
} from '../api/certificates'
import { sessionStore } from '../session'
import CertificatesView from './CertificatesView.vue'

const accountStagingID = '1'.repeat(32)
const accountProductionID = '2'.repeat(32)
const credentialID = '3'.repeat(32)
const certificateID = '4'.repeat(32)
const versionID = '5'.repeat(32)
const bindingID = '6'.repeat(32)
const taskID = '7'.repeat(32)
const planID = '8'.repeat(32)
const now = '2026-07-21T08:00:00Z'

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

const accounts: ACMEAccount[] = [
  {
    id: accountStagingID,
    environment: 'staging',
    directory_url: directories[0]!.directory_url,
    account_uri: 'https://acme-staging-v02.api.letsencrypt.org/acme/acct/10',
    email: 'ops@example.com',
    status: 'valid',
    terms_url: directories[0]!.terms_url,
    terms_agreed_at: now,
    terms_agreed_by: 7,
    created_at: now,
    updated_at: now,
  },
  {
    id: accountProductionID,
    environment: 'production',
    directory_url: directories[1]!.directory_url,
    account_uri: 'https://acme-v02.api.letsencrypt.org/acme/acct/20',
    email: 'ops@example.com',
    status: 'valid',
    terms_url: directories[1]!.terms_url,
    terms_agreed_at: now,
    terms_agreed_by: 7,
    created_at: now,
    updated_at: now,
  },
]

const credential: DNSCredential = {
  id: credentialID,
  name: 'Production zones',
  provider: 'cloudflare',
  fingerprint: '0123456789abcdef',
  status: 'valid',
  verified_at: now,
  created_at: now,
  updated_at: now,
}

const server: CertificateServerCandidate = {
  ref: {
    path: 'conf.d/site.conf',
    start_offset: 42,
    server_names: ['example.com', '*.example.com'],
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
  primary_identifier: 'example.com',
  identifiers: ['example.com', 'www.example.com'],
  challenge: 'cloudflare_dns_01',
  account_id: accountProductionID,
  dns_credential_id: credentialID,
  state: 'active',
  active_version_id: versionID,
  auto_renew: true,
  renew_before_seconds: 2_592_000,
  next_renewal_at: '2026-08-21T08:00:00Z',
  retry_count: 0,
  not_before: now,
  not_after: '2026-10-19T08:00:00Z',
  created_at: now,
  updated_at: now,
  versions: [
    {
      id: versionID,
      state: 'active',
      leaf_fingerprint: 'b'.repeat(64),
      serial_number: '01AB',
      issuer: "Let's Encrypt test issuer",
      not_before: now,
      not_after: '2026-10-19T08:00:00Z',
      created_at: now,
    },
  ],
  bindings: [
    {
      id: bindingID,
      version_id: versionID,
      config_path: server.ref.path,
      server_start_offset: server.ref.start_offset,
      server_names: server.ref.server_names,
      listeners: server.ref.listeners,
      server_fingerprint: server.ref.fingerprint,
      created_at: now,
      updated_at: now,
    },
  ],
}

const activeTask: CertificateTask = {
  id: taskID,
  kind: 'issue',
  state: 'running',
  stage: 'propagating',
  plan_id: planID,
  certificate_id: certificateID,
  account_id: accountProductionID,
  dns_credential_id: credentialID,
  challenge: 'cloudflare_dns_01',
  created_at: now,
  updated_at: now,
  started_at: now,
  stages: [
    {
      sequence: 1,
      stage: 'propagating',
      result: 'running',
      details: {},
      occurred_at: now,
    },
  ],
}

const orderPlan: CertificateOrderPlan = {
  id: planID,
  state: 'planned',
  environment: 'production',
  challenge: 'cloudflare_dns_01',
  account_id: accountProductionID,
  staging_account_id: accountStagingID,
  dns_credential_id: credentialID,
  certificate_id: '9'.repeat(32),
  primary_identifier: '*.example.com',
  identifiers: ['*.example.com'],
  server_refs: [server.ref],
  binding_diff: [
    {
      path: server.ref.path,
      patch: '@@ -2,0 +3,2 @@\n+ssl_certificate /var/lib/nginx-uix/certs/public.pem;\n',
      added_lines: 2,
      removed_lines: 0,
    },
  ],
  production_digest: 'c'.repeat(64),
  staging_evidence: false,
  requires_risk_confirmation: true,
  risk_confirmation_phrase: 'ISSUE PRODUCTION CERTIFICATE WITHOUT STAGING',
  expires_at: '2026-07-21T08:10:00Z',
  created_at: now,
}

class FakeEventSource {
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  close = vi.fn()
}

function clientFixture() {
  return {
    listACMEDirectories: vi.fn(async () => directories),
    listACMEAccounts: vi.fn(async () => accounts),
    createACMEAccount: vi.fn(async (input: { environment: 'staging' | 'production'; email: string }) => ({
      ...accounts[1]!,
      id: 'b'.repeat(32),
      environment: input.environment,
      email: input.email,
    })),
    importACMEAccount: vi.fn(async (input: { environment: 'staging' | 'production'; email: string; account_uri: string }) => ({
      ...accounts[0]!,
      id: 'c'.repeat(32),
      environment: input.environment,
      email: input.email,
      account_uri: input.account_uri,
    })),
    deactivateACMEAccount: vi.fn(async (id: string) => ({
      ...accounts[0]!,
      id,
      status: 'deactivated' as const,
    })),
    listCertificateDNSCredentials: vi.fn(async () => [credential]),
    createCertificateDNSCredential: vi.fn(async (input: { name: string }) => ({
      ...credential,
      name: input.name,
    })),
    deleteCertificateDNSCredential: vi.fn(),
    listCertificateServerCandidates: vi.fn(async () => [server]),
    createCertificateOrderPlan: vi.fn(async () => orderPlan),
    executeCertificateOrderPlan: vi.fn(async () => ({ ...activeTask, id: 'a'.repeat(32) })),
    listCertificateTasks: vi.fn(async () => [activeTask]),
    getCertificateTask: vi.fn(async () => activeTask),
    cancelCertificateTask: vi.fn(async () => ({ ...activeTask, state: 'cancelling' as const })),
    listCertificates: vi.fn(async () => [certificate]),
    getCertificate: vi.fn(async () => certificate),
    renewCertificate: vi.fn(async () => activeTask),
    updateCertificateRenewalPolicy: vi.fn(async () => certificate),
    createCertificateBindingPlan: vi.fn(async () => ({
      id: 'd'.repeat(32),
      state: 'planned' as const,
      certificate_id: certificateID,
      version_id: versionID,
      server_refs: [server.ref],
      binding_diff: orderPlan.binding_diff,
      production_digest: 'c'.repeat(64),
      expires_at: '2026-07-21T08:10:00Z',
      created_at: now,
    })),
    executeCertificateBindingPlan: vi.fn(async () => ({ ...activeTask, id: 'e'.repeat(32), kind: 'bind' as const })),
    unbindCertificate: vi.fn(async () => ({
      ...certificate,
      state: 'unbound' as const,
      bindings: [],
    })),
    exportCertificate: vi.fn(async () => ({
      blob: new Blob(['public certificate']),
      filename: `certificate-${certificateID}.pem`,
    })),
    deleteCertificate: vi.fn(),
  }
}

async function mountView() {
  const client = clientFixture()
  const sources: FakeEventSource[] = []
  const eventSourceFactory = vi.fn(() => {
    const source = new FakeEventSource()
    sources.push(source)
    return source
  })
  const saveFile = vi.fn()
  const wrapper = mount(CertificatesView, {
    props: {
      client,
      csrfToken: 'csrf-token',
      certificateId: certificateID,
      eventSourceFactory,
      saveFile,
    },
    global: {
      stubs: {
        RouterLink: {
          props: ['to'],
          template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
        },
      },
    },
  })
  await flushPromises()
  return { client, eventSourceFactory, saveFile, sources, wrapper }
}

describe('CertificatesView', () => {
  it('uses the authenticated session CSRF token when the route does not pass one', async () => {
    const previousPhase = sessionStore.state.phase
    const previousSession = sessionStore.state.session
    sessionStore.state.phase = 'authenticated'
    sessionStore.state.session = {
      user: {
        id: 7,
        username: 'operator',
        created_at: now,
      },
      csrf_token: 'session-csrf-token',
      created_at: now,
      last_seen_at: now,
      idle_expires_at: '2026-07-21T08:30:00Z',
      absolute_expires_at: '2026-07-22T08:00:00Z',
    }
    const client = clientFixture()
    const wrapper = mount(CertificatesView, {
      props: {
        client,
        eventSourceFactory: () => new FakeEventSource(),
      },
      global: {
        stubs: {
          RouterLink: {
            props: ['to'],
            template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>',
          },
        },
      },
    })

    try {
      await flushPromises()
      await wrapper.get('[name="credential-name"]').setValue('Session credential')
      await wrapper.get('[name="cloudflare-token"]').setValue('one-time-token')
      await wrapper.get('[data-action="save-cloudflare-token"]').trigger('submit')
      await flushPromises()

      expect(client.createCertificateDNSCredential).toHaveBeenCalledWith(
        { name: 'Session credential', api_token: 'one-time-token' },
        'session-csrf-token',
      )
    } finally {
      wrapper.unmount()
      sessionStore.state.phase = previousPhase
      sessionStore.state.session = previousSession
    }
  })

  it('submits a Cloudflare API Token once, clears it, and renders only safe metadata', async () => {
    const { client, wrapper } = await mountView()
    const token = 'cloudflare-secret-token'

    expect(wrapper.text()).toContain('Zone Read')
    expect(wrapper.text()).toContain('DNS Write')
    expect(wrapper.text()).toContain('Global API Key is unsupported')
    expect(wrapper.get('[name="cloudflare-token"]').attributes('type')).toBe('password')

    await wrapper.get('[name="credential-name"]').setValue('Restricted example.com zone')
    await wrapper.get('[name="cloudflare-token"]').setValue(token)
    await wrapper.get('[data-action="save-cloudflare-token"]').trigger('submit')
    await flushPromises()

    expect(client.createCertificateDNSCredential).toHaveBeenCalledWith(
      { name: 'Restricted example.com zone', api_token: token },
      'csrf-token',
    )
    expect((wrapper.get('[name="cloudflare-token"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.html()).not.toContain(token)
    expect(wrapper.text()).toContain('0123456789abcdef')
  })

  it('blocks wildcard HTTP-01, then reviews and exactly confirms a Cloudflare DNS-01 plan', async () => {
    const { client, wrapper } = await mountView()

    await wrapper.get('[name="identifier-0"]').setValue('*.example.com')
    await wrapper.get('[data-action="review-certificate"]').trigger('click')

    expect(wrapper.text()).toContain('Wildcard certificates require Cloudflare DNS-01')
    expect(client.createCertificateOrderPlan).not.toHaveBeenCalled()

    await wrapper.get('[name="certificate-challenge"]').setValue('cloudflare_dns_01')
    await wrapper.get('[name="certificate-account"]').setValue(accountProductionID)
    await wrapper.get('[name="staging-account"]').setValue(accountStagingID)
    await wrapper.get('[name="dns-credential"]').setValue(credentialID)
    await wrapper.get(`[value="${server.ref.fingerprint}"]`).setValue(true)
    await wrapper.get('[data-action="review-certificate"]').trigger('click')
    await flushPromises()

    expect(client.createCertificateOrderPlan).toHaveBeenCalledWith(
      {
        identifiers: ['*.example.com'],
        challenge: 'cloudflare_dns_01',
        account_id: accountProductionID,
        staging_account_id: accountStagingID,
        dns_credential_id: credentialID,
        server_refs: [server.ref],
      },
      'csrf-token',
    )
    expect(wrapper.text()).toContain('No certificate or Nginx configuration has been changed.')
    expect(wrapper.text()).toContain('A matching staging preflight is required before production')
    expect(wrapper.text()).toContain(server.ref.path)
    expect(wrapper.html()).not.toContain('cloudflare-secret-token')

    const execute = wrapper.get('[data-action="execute-certificate-plan"]')
    expect(execute.attributes('disabled')).toBeDefined()
    await wrapper.get('[name="certificate-confirmation"]').setValue('*.example.com')
    await wrapper
      .get('[name="production-risk-confirmation"]')
      .setValue('ISSUE PRODUCTION CERTIFICATE WITHOUT STAGING')
    expect(execute.attributes('disabled')).toBeUndefined()
    await execute.trigger('click')
    await flushPromises()

    expect(client.executeCertificateOrderPlan).toHaveBeenCalledWith(
      planID,
      {
        confirmation: '*.example.com',
        production_risk_confirmation: 'ISSUE PRODUCTION CERTIFICATE WITHOUT STAGING',
      },
      'csrf-token',
    )
  })

  it('rebuilds task evidence after an SSE disconnect without treating it as cancellation', async () => {
    const { client, sources, wrapper } = await mountView()

    expect(sources).toHaveLength(1)
    expect(wrapper.text()).toContain('Leaving this page does not cancel the task.')
    sources[0]!.onerror?.(new Event('error'))
    await flushPromises()

    expect(client.getCertificateTask).toHaveBeenCalledWith(taskID)
    expect(client.cancelCertificateTask).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Reconnecting — the server task continues')

    sources[0]!.onopen?.(new Event('open'))
    await flushPromises()
    expect(client.getCertificateTask).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('Connected')
  })

  it('creates and imports ACME accounts while clearing imported private-key material', async () => {
    const { client, wrapper } = await mountView()

    await wrapper.get('[name="account-environment"]').setValue('production')
    await wrapper.get('[name="account-email"]').setValue('certs@example.com')
    await wrapper.get('[name="account-terms"]').setValue(true)
    await wrapper.get('[data-action="create-acme-account"]').trigger('submit')
    await flushPromises()

    expect(client.createACMEAccount).toHaveBeenCalledWith(
      {
        environment: 'production',
        email: 'certs@example.com',
        terms_of_service_agreed: true,
      },
      'csrf-token',
    )

    const privateKey = '-----BEGIN PRIVATE KEY-----\nsecret-account-key\n-----END PRIVATE KEY-----'
    await wrapper.get('[name="import-environment"]').setValue('staging')
    await wrapper.get('[name="import-email"]').setValue('import@example.com')
    await wrapper
      .get('[name="import-account-uri"]')
      .setValue('https://acme-staging-v02.api.letsencrypt.org/acme/acct/99')
    await wrapper.get('[name="import-private-key"]').setValue(privateKey)
    await wrapper.get('[name="import-terms"]').setValue(true)
    await wrapper.get('[data-action="import-acme-account"]').trigger('submit')
    await flushPromises()

    expect(client.importACMEAccount).toHaveBeenCalledWith(
      {
        environment: 'staging',
        email: 'import@example.com',
        account_uri: 'https://acme-staging-v02.api.letsencrypt.org/acme/acct/99',
        private_key_pem: privateKey,
        terms_of_service_agreed: true,
      },
      'csrf-token',
    )
    expect((wrapper.get('[name="import-private-key"]').element as HTMLTextAreaElement).value).toBe('')
    expect(wrapper.html()).not.toContain('secret-account-key')
  })

  it('requires exact lifecycle confirmations and passes exports directly to a save boundary', async () => {
    const { client, saveFile, wrapper } = await mountView()

    const renew = wrapper.get('[data-action="renew-certificate"] button[type="submit"]')
    expect(renew.attributes('disabled')).toBeDefined()
    await wrapper.get('[name="renew-confirmation"]').setValue(certificate.primary_identifier)
    await renew.trigger('submit')
    await flushPromises()
    expect(client.renewCertificate).toHaveBeenCalledWith(
      certificateID,
      certificate.primary_identifier,
      'csrf-token',
    )

    await wrapper.get('[data-action="open-certificate-export"]').trigger('click')
    expect((wrapper.get('[name="include-private-key"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.get('[name="export-confirmation"]').setValue(certificateID)
    await wrapper.get('[data-action="export-certificate"]').trigger('click')
    await flushPromises()

    expect(client.exportCertificate).toHaveBeenCalledWith(
      certificateID,
      {
        confirmation: certificateID,
        include_private_key: false,
        private_key_confirmation: '',
      },
      'csrf-token',
    )
    expect(saveFile).toHaveBeenCalledWith(expect.objectContaining({
      filename: `certificate-${certificateID}.pem`,
    }))
    expect(wrapper.text()).not.toContain('public certificate')
    expect(wrapper.get('[data-action="delete-certificate"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('Delete is blocked while 1 Nginx binding remains')
  })

  it('deactivates accounts only after the full account ID is confirmed', async () => {
    const { client, wrapper } = await mountView()

    await wrapper.get(`[data-action="deactivate-account"][data-id="${accountStagingID}"]`).trigger('click')
    const confirm = wrapper.get('[data-confirmation]')
    expect(wrapper.get('[role="dialog"]').text()).toContain('Existing certificates remain served, but renewals using this account stop')
    await confirm.setValue(accountStagingID)
    await wrapper.get('[role="dialog"] [data-action="confirm"]').trigger('submit')
    await flushPromises()

    expect(client.deactivateACMEAccount).toHaveBeenCalledWith(accountStagingID, 'csrf-token')
    expect(wrapper.text()).toContain('deactivated')
  })

  it('unbinds through exact confirmation, then reviews and executes a standalone binding plan', async () => {
    const { client, wrapper } = await mountView()

    await wrapper.get('[name="unbind-confirmation"]').setValue(certificate.primary_identifier)
    await wrapper.get('[data-action="unbind-certificate"]').trigger('submit')
    await flushPromises()
    expect(client.unbindCertificate).toHaveBeenCalledWith(
      certificateID,
      certificate.primary_identifier,
      'csrf-token',
    )
    expect(wrapper.text()).toContain('Unbound — no Nginx server currently references this certificate')

    await wrapper.get(`[value="bind:${server.ref.fingerprint}"]`).setValue(true)
    await wrapper.get('[data-action="review-certificate-binding"]').trigger('click')
    await flushPromises()
    expect(client.createCertificateBindingPlan).toHaveBeenCalledWith(
      certificateID,
      [server.ref],
      'csrf-token',
    )
    expect(wrapper.text()).toContain('No Nginx configuration has been changed by this binding review.')

    await wrapper.get('[name="binding-confirmation"]').setValue(certificate.primary_identifier)
    await wrapper.get('[data-action="execute-certificate-binding"]').trigger('click')
    await flushPromises()
    expect(client.executeCertificateBindingPlan).toHaveBeenCalledWith(
      'd'.repeat(32),
      certificate.primary_identifier,
      'csrf-token',
    )
  })
})

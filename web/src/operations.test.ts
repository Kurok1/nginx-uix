/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

import { describe, expect, it, vi } from 'vitest'

import type {
  AttentionCase,
  ConfigBackup,
  ConfigRestore,
  NginxRestart,
  SystemStatusResponse,
} from './api/types'
import {
  createOperationsStore,
  type OperationsClient,
  type OperationsEventStream,
} from './operations'
import type { SessionStore } from './session'

const backup: ConfigBackup = {
  id: '11111111111111111111111111111111',
  origin_type: 'release',
  origin_id: '22222222222222222222222222222222',
  release_id: '22222222222222222222222222222222',
  production_digest: 'a'.repeat(64),
  state: 'complete',
  entry_count: 2,
  total_bytes: 512,
  body_present: true,
  protected: false,
  manually_protected: false,
  protections: [],
  created_at: '2026-07-19T08:00:00Z',
  verified_at: '2026-07-19T08:00:01Z',
}

const queuedRestore: ConfigRestore = {
  id: '33333333333333333333333333333333',
  target_backup_id: backup.id,
  safety_backup_id: '44444444444444444444444444444444',
  state: 'queued',
  stage: 'queued',
  source_digest: 'b'.repeat(64),
  target_digest: backup.production_digest,
  reason: 'restore known configuration',
  request_id: 'restore-request',
  created_at: '2026-07-19T09:00:00Z',
  updated_at: '2026-07-19T09:00:00Z',
  stages: [],
}

const succeededRestore: ConfigRestore = {
  ...queuedRestore,
  state: 'succeeded',
  stage: 'succeeded',
  updated_at: '2026-07-19T09:00:05Z',
  finished_at: '2026-07-19T09:00:05Z',
  stages: [{
    sequence: 1,
    stage: 'succeeded',
    result: 'success',
    details: {},
    occurred_at: '2026-07-19T09:00:05Z',
  }],
}

const attention: AttentionCase = {
  id: '55555555555555555555555555555555',
  subject_type: 'restart',
  subject_id: '66666666666666666666666666666666',
  state: 'open',
  reason_code: 'runtime_unknown',
  opened_at: '2026-07-19T07:00:00Z',
}

const runtime: SystemStatusResponse = {
  sampled_at: '2026-07-19T08:00:00Z',
  components: { ui: 'healthy', agent: 'healthy', nginx: 'running' },
  master: { pid: 100, role: 'master', started_at: '2026-07-19T07:00:00Z' },
  workers: [{ pid: 101, role: 'worker', started_at: '2026-07-19T07:00:00Z' }],
  build: null,
  startup_validation: { valid: true, checked_at: '2026-07-19T08:00:00Z', exit_code: 0, diagnostic: '' },
  recovery: null,
  issues: [],
}

class FakeStream implements OperationsEventStream {
  readonly listeners = new Map<string, EventListener[]>()
  closed = false

  addEventListener(type: string, listener: EventListener): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  close(): void {
    this.closed = true
  }

  emit(type: string): void {
    for (const listener of this.listeners.get(type) ?? []) listener(new Event(type))
  }
}

function sessionFixture(): SessionStore {
  return {
    state: {
      phase: 'authenticated',
      session: {
        user: { id: 7, username: 'operator', created_at: '2026-07-19T06:00:00Z' },
        csrf_token: 'csrf-1',
        created_at: '2026-07-19T06:00:00Z',
        last_seen_at: '2026-07-19T08:00:00Z',
        idle_expires_at: '2026-07-19T16:00:00Z',
        absolute_expires_at: '2026-07-20T06:00:00Z',
      },
    },
    restore: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    handleAPIError: vi.fn(() => false),
    onExpired: vi.fn(() => () => undefined),
  }
}

function clientFixture(): OperationsClient & {
  restoreInput: Parameters<OperationsClient['createRestore']>[1] | null
} {
  let restoreReads = 0
  return {
    restoreInput: null,
    getSystemStatus: async () => runtime,
    listAttentionCases: async () => ({ items: [attention] }),
    listBackups: async () => ({ items: [backup] }),
    listReleaseHistory: async () => ({ items: [] }),
    listRestoreHistory: async () => ({ items: [] }),
    listRestartHistory: async () => ({ items: [] }),
    listAuditEvents: async () => ({ items: [] }),
    createRestore(inputBackupID, input) {
      if (inputBackupID !== backup.id) throw new Error('unexpected backup')
      this.restoreInput = input
      return Promise.resolve(queuedRestore)
    },
    async getRestore() {
      restoreReads++
      return restoreReads === 1 ? succeededRestore : succeededRestore
    },
    async createRestart(): Promise<NginxRestart> {
      throw new Error('not used')
    },
    async getRestart(): Promise<NginxRestart> {
      throw new Error('not used')
    },
    changeBackupProtection: async () => backup,
    planBackupRetention: async () => { throw new Error('not used') },
    executeBackupRetention: async () => { throw new Error('not used') },
    getBackupRetention: async () => { throw new Error('not used') },
    verifyAttentionCase: async () => { throw new Error('not used') },
  }
}

describe('operations store', () => {
  it('loads blocking evidence and follows a restore SSE task independently of the request', async () => {
    const client = clientFixture()
    const stream = new FakeStream()
    let streamURL = ''
    const store = createOperationsStore(client, sessionFixture(), (url) => {
      streamURL = url
      return stream
    })

    await store.loadOverview()
    expect(store.state.runtime).toEqual(runtime)
    expect(store.state.attention).toEqual([attention])

    await store.startRestore(backup, 'restore known configuration', backup.id, attention.id)
    expect(client.restoreInput).toEqual({
      attention_case_id: attention.id,
      reason: 'restore known configuration',
      confirm_backup_id: backup.id,
    })
    expect(store.state.activeRestore).toEqual(queuedRestore)
    expect(streamURL).toBe(`/api/v1/config/restores/${queuedRestore.id}/events`)

    stream.emit('terminal')
    await vi.waitFor(() => expect(store.state.activeRestore).toEqual(succeededRestore))
    expect(stream.closed).toBe(true)
    store.dispose()
  })
})

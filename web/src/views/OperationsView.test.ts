/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'

import type {
  AttentionCase,
  AuditEvent,
  ConfigBackup,
  ConfigRestore,
  NginxRestart,
  RetentionRun,
  SystemStatusResponse,
} from '../api/types'
import type { OperationsState, OperationsStore } from '../operations'
import { appI18n } from '../i18n'
import OperationsView from './OperationsView.vue'

const backup: ConfigBackup = {
  id: '11111111111111111111111111111111',
  origin_type: 'release',
  origin_id: '22222222222222222222222222222222',
  release_id: '22222222222222222222222222222222',
  production_digest: 'a'.repeat(64),
  state: 'complete',
  entry_count: 2,
  total_bytes: 1024,
  body_present: true,
  protected: false,
  manually_protected: false,
  protections: [],
  created_at: '2026-07-19T08:00:00Z',
  verified_at: '2026-07-19T08:00:01Z',
}

const attention: AttentionCase = {
  id: '33333333333333333333333333333333',
  subject_type: 'restore',
  subject_id: '44444444444444444444444444444444',
  backup_id: backup.id,
  state: 'open',
  reason_code: 'runtime_unknown',
  opened_at: '2026-07-19T07:00:00Z',
}

const audit: AuditEvent = {
  id: 1,
  occurred_at: '2026-07-19T08:05:00Z',
  actor_name: 'operator',
  action: 'config.backup.protect',
  object_type: 'config_backup',
  object_id: backup.id,
  result: 'succeeded',
  request_id: 'request-audit',
  details: { protected: true },
}

const runtime: SystemStatusResponse = {
  sampled_at: '2026-07-19T08:00:00Z',
  components: { ui: 'healthy', agent: 'healthy', nginx: 'running' },
  master: { pid: 100, role: 'master', started_at: '2026-07-19T07:00:00Z' },
  workers: [{ pid: 101, role: 'worker', started_at: '2026-07-19T07:00:00Z' }],
  build: null,
  startup_validation: {
    valid: true,
    checked_at: '2026-07-19T08:00:00Z',
    exit_code: 0,
    diagnostic: '',
  },
  recovery: null,
  issues: [],
}

const restore: ConfigRestore = {
  id: '55555555555555555555555555555555',
  target_backup_id: backup.id,
  safety_backup_id: '66666666666666666666666666666666',
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

const restart: NginxRestart = {
  id: '77777777777777777777777777777777',
  state: 'queued',
  stage: 'queued',
  production_digest: backup.production_digest,
  worker_count: 0,
  reason: 'replace unhealthy master',
  request_id: 'restart-request',
  created_at: '2026-07-19T09:00:00Z',
  updated_at: '2026-07-19T09:00:00Z',
  stages: [],
}

const retention: RetentionRun = {
  id: '88888888888888888888888888888888',
  state: 'planned',
  policy: {
    minimum_complete: 3,
    maximum_complete: 20,
    maximum_total_bytes: 1073741824,
    minimum_age_seconds: 86400,
  },
  backup_count: 1,
  total_bytes: backup.total_bytes,
  protected_count: 0,
  delete_count: 1,
  delete_bytes: backup.total_bytes,
  deleted_count: 0,
  deleted_bytes: 0,
  created_at: '2026-07-19T10:00:00Z',
  expires_at: '2026-07-19T10:05:00Z',
  items: [{
    ordinal: 1,
    backup_id: backup.id,
    decision: 'delete',
    reason_code: 'maximum_count',
    state: 'planned',
    snapshot_created_at: backup.created_at,
    snapshot_total_bytes: backup.total_bytes,
  }],
}

function storeFixture(): OperationsStore {
  const state = reactive<OperationsState>({
    phase: 'ready',
    stream: 'closed',
    runtime,
    attention: [attention],
    attentionCursor: '',
    backups: [backup],
    backupsCursor: '',
    releases: [],
    releaseCursor: '',
    restores: [],
    restoreCursor: '',
    restarts: [],
    restartCursor: '',
    audit: [audit],
    auditCursor: '',
    retention: null,
    activeRestore: null,
    activeRestart: null,
    verification: null,
    pending: '',
    error: '',
    errorRequestID: '',
  })
  return {
    state,
    loadOverview: vi.fn().mockResolvedValue(undefined),
    loadBackups: vi.fn().mockResolvedValue(undefined),
    loadHistory: vi.fn().mockResolvedValue(undefined),
    loadAudit: vi.fn().mockResolvedValue(undefined),
    startRestore: vi.fn(async () => {
      state.activeRestore = restore
      return restore
    }),
    startRestart: vi.fn(async () => {
      state.activeRestart = restart
      return restart
    }),
    changeProtection: vi.fn(async () => backup),
    planRetention: vi.fn(async () => {
      state.retention = retention
      return retention
    }),
    executeRetention: vi.fn(async () => retention),
    verifyAttention: vi.fn().mockResolvedValue({}),
    refreshActive: vi.fn().mockResolvedValue(undefined),
    dispose: vi.fn(),
  } as unknown as OperationsStore
}

async function mountView(store: OperationsStore, path = '/config/operations') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/config/operations', component: { template: '<div />' } }],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(OperationsView, {
    props: { store },
    global: { plugins: [router] },
    attachTo: document.body,
  })
  await flushPromises()
  return { router, wrapper }
}

describe('OperationsView', () => {
  it('localizes a scoped failure while preserving its request ID', async () => {
    appI18n.global.locale.value = 'en-US'
    const store = storeFixture()
    store.state.error = 'backups_failed'
    store.state.errorRequestID = 'request-operations-view'
    const { wrapper } = await mountView(store)

    expect(wrapper.text()).toContain(
      'Backup evidence could not be loaded. Request ID: request-operations-view.',
    )
    wrapper.unmount()
  })

  it('renders recovery controls in Simplified Chinese', async () => {
    appI18n.global.locale.value = 'zh-CN'
    const { wrapper } = await mountView(storeFixture())

    expect(wrapper.get('h1').text()).toBe('恢复与历史')
    expect(wrapper.text()).toContain('刷新证据')
    expect(wrapper.get('[role="tab"][aria-selected="true"]').text()).toBe('概览')
    expect(wrapper.get('[data-attention-case]').text()).toContain('需要处理')
    expect(wrapper.get('[data-runtime-control]').text()).toContain('Nginx 运行中')
    expect(wrapper.text()).toContain('不可变备份')
    wrapper.unmount()
  })

  it('keeps attention evidence first and queues only a named fixed restart', async () => {
    const store = storeFixture()
    const { wrapper } = await mountView(store)

    expect(wrapper.get('h1').text()).toBe('Recovery & History')
    expect(wrapper.get('[data-attention-case]').text()).toContain('Needs attention')
    expect(wrapper.get('[data-runtime-control]').text()).toContain('Nginx running')
    await wrapper.get('[data-action="verify-attention"]').trigger('click')
    expect(store.verifyAttention).toHaveBeenCalledWith(attention.id)

    await wrapper.get('[data-action="restart-nginx"]').trigger('click')
    const modal = wrapper.get('[role="dialog"]')
    await modal.get('textarea').setValue('replace unhealthy master')
    await modal.get('[data-confirmation]').setValue('RESTART NGINX')
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(store.startRestart).toHaveBeenCalledWith(
      'replace unhealthy master',
      'RESTART NGINX',
      undefined,
    )
    wrapper.unmount()
  })

  it('restores a verified backup and executes only the exact current retention plan', async () => {
    const store = storeFixture()
    const { router, wrapper } = await mountView(store, '/config/operations?tab=backups')

    expect(wrapper.get('[role="tab"][aria-selected="true"]').text()).toBe('Backups')
    await wrapper.get('[data-backup-table] [data-action="restore"]').trigger('click')
    let modal = wrapper.get('[role="dialog"]')
    await modal.get('textarea').setValue('restore known configuration')
    await modal.get('[data-confirmation]').setValue(backup.id)
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(store.startRestore).toHaveBeenCalledWith(
      backup,
      'restore known configuration',
      backup.id,
      undefined,
    )

    await wrapper.get('[data-action="plan-retention"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain(retention.id)
    await wrapper.get('[data-action="execute-retention"]').trigger('click')
    modal = wrapper.get('[role="dialog"]')
    await modal.get('[data-confirmation]').setValue(retention.id)
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(store.executeRetention).toHaveBeenCalledWith(retention.id)

    const auditTab = wrapper.findAll('[role="tab"]').find((tab) => tab.text() === 'Audit')
    await auditTab?.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('audit')
    expect(store.loadAudit).toHaveBeenCalled()
    expect(wrapper.get('[data-audit-action]').text()).toBe('config.backup.protect')
    wrapper.unmount()
  })
})

/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

import { reactive } from 'vue'

import { apiClient } from './api/client'
import type {
  AttentionCase,
  AuditEvent,
  ConfigBackup,
  ConfigRestore,
  CursorPage,
  NginxRestart,
  Release,
  RetentionRun,
  RuntimeVerification,
  SystemStatusResponse,
} from './api/types'
import { sessionStore, type SessionStore } from './session'

export interface OperationsClient {
  getSystemStatus(signal?: AbortSignal): Promise<SystemStatusResponse>
  listAttentionCases(options?: {
    state?: 'open' | 'resolved'
    cursor?: string
    signal?: AbortSignal
  }): Promise<CursorPage<AttentionCase>>
  listBackups(options?: {
    cursor?: string
    includeDeleted?: boolean
    signal?: AbortSignal
  }): Promise<CursorPage<ConfigBackup>>
  listReleaseHistory(cursor?: string, signal?: AbortSignal): Promise<CursorPage<Release>>
  listRestoreHistory(cursor?: string, signal?: AbortSignal): Promise<CursorPage<ConfigRestore>>
  listRestartHistory(cursor?: string, signal?: AbortSignal): Promise<CursorPage<NginxRestart>>
  listAuditEvents(cursor?: string, signal?: AbortSignal): Promise<CursorPage<AuditEvent>>
  createRestore(
    backupID: string,
    input: { attention_case_id: string; reason: string; confirm_backup_id: string },
    csrfToken: string,
  ): Promise<ConfigRestore>
  getRestore(id: string, signal?: AbortSignal): Promise<ConfigRestore>
  createRestart(
    input: { attention_case_id: string; reason: string; confirmation: string },
    csrfToken: string,
  ): Promise<NginxRestart>
  getRestart(id: string, signal?: AbortSignal): Promise<NginxRestart>
  changeBackupProtection(
    id: string,
    input: {
      expected_protected: boolean
      protected: boolean
      reason: string
      confirmation: string
    },
    csrfToken: string,
  ): Promise<ConfigBackup>
  planBackupRetention(csrfToken: string): Promise<RetentionRun>
  executeBackupRetention(id: string, confirmation: string, csrfToken: string): Promise<RetentionRun>
  getBackupRetention(id: string, signal?: AbortSignal): Promise<RetentionRun>
  verifyAttentionCase(id: string, csrfToken: string): Promise<RuntimeVerification>
}

export interface OperationsEventStream {
  addEventListener(type: string, listener: EventListener): void
  close(): void
}

export interface OperationsState {
  phase: 'idle' | 'loading' | 'ready'
  stream: 'closed' | 'connecting' | 'live' | 'reconnecting'
  runtime: SystemStatusResponse | null
  attention: AttentionCase[]
  attentionCursor: string
  backups: ConfigBackup[]
  backupsCursor: string
  releases: Release[]
  releaseCursor: string
  restores: ConfigRestore[]
  restoreCursor: string
  restarts: NginxRestart[]
  restartCursor: string
  audit: AuditEvent[]
  auditCursor: string
  retention: RetentionRun | null
  activeRestore: ConfigRestore | null
  activeRestart: NginxRestart | null
  verification: RuntimeVerification | null
  pending: string
  error: string
}

export interface OperationsStore {
  readonly state: OperationsState
  loadOverview(): Promise<void>
  loadBackups(append?: boolean): Promise<void>
  loadHistory(append?: boolean): Promise<void>
  loadAudit(append?: boolean): Promise<void>
  startRestore(
    backup: ConfigBackup,
    reason: string,
    confirmation: string,
    attentionCaseID?: string,
  ): Promise<ConfigRestore>
  startRestart(reason: string, confirmation: string, attentionCaseID?: string): Promise<NginxRestart>
  changeProtection(
    backup: ConfigBackup,
    protectedValue: boolean,
    reason: string,
    confirmation: string,
  ): Promise<ConfigBackup>
  planRetention(): Promise<RetentionRun>
  executeRetention(confirmation: string): Promise<RetentionRun>
  verifyAttention(id: string): Promise<RuntimeVerification>
  refreshActive(): Promise<void>
  dispose(): void
}

type EventStreamFactory = (url: string) => OperationsEventStream

export function createOperationsStore(
  client: OperationsClient,
  sessions: SessionStore,
  createStream: EventStreamFactory = (url) => new EventSource(url, { withCredentials: true }),
): OperationsStore {
  const state = reactive<OperationsState>({
    phase: 'idle', stream: 'closed', runtime: null, attention: [], attentionCursor: '',
    backups: [], backupsCursor: '', releases: [], releaseCursor: '', restores: [],
    restoreCursor: '', restarts: [], restartCursor: '', audit: [], auditCursor: '',
    retention: null, activeRestore: null, activeRestart: null, verification: null,
    pending: '', error: '',
  })
  let stream: OperationsEventStream | null = null
  let activeController: AbortController | null = null
  let refreshPromise: Promise<void> | null = null
  let retentionTimer: ReturnType<typeof setTimeout> | undefined
  let disposed = false

  const removeExpiryListener = sessions.onExpired(() => {
    closeStream()
    stopRetentionPolling()
    state.error = 'Session expired while tracking the operation. Sign in to resume.'
  })

  function csrfToken(): string {
    const token = sessions.state.session?.csrf_token
    if (sessions.state.phase !== 'authenticated' || token === undefined) {
      throw new Error('an authenticated session is required')
    }
    return token
  }

  async function loadOverview(): Promise<void> {
    if (disposed || state.pending === 'overview') return
    state.phase = 'loading'
    state.pending = 'overview'
    state.error = ''
    const controller = replaceController()
    try {
      const [runtime, cases] = await Promise.all([
        client.getSystemStatus(controller.signal),
        client.listAttentionCases({ state: 'open', signal: controller.signal }),
      ])
      state.runtime = runtime
      state.attention = cases.items
      state.attentionCursor = cases.next_cursor ?? ''
      state.phase = 'ready'
    } catch (error: unknown) {
      handleError(error, 'Runtime and attention evidence could not be loaded.')
      state.phase = 'ready'
      throw error
    } finally {
      if (activeController === controller) activeController = null
      state.pending = ''
    }
  }

  async function loadBackups(append = false): Promise<void> {
    if (disposed || state.pending === 'backups') return
    state.pending = 'backups'
    state.error = ''
    try {
      const page = await client.listBackups({
        cursor: append && state.backupsCursor !== '' ? state.backupsCursor : undefined,
        includeDeleted: true,
      })
      state.backups = append ? [...state.backups, ...page.items] : page.items
      state.backupsCursor = page.next_cursor ?? ''
    } catch (error: unknown) {
      handleError(error, 'Backup evidence could not be loaded.')
      throw error
    } finally {
      state.pending = ''
    }
  }

  async function loadHistory(append = false): Promise<void> {
    if (disposed || state.pending === 'history') return
    state.pending = 'history'
    state.error = ''
    try {
      const [releases, restores, restarts] = await Promise.all([
        append && state.releaseCursor === ''
          ? Promise.resolve<CursorPage<Release>>({ items: [] })
          : client.listReleaseHistory(append ? state.releaseCursor : undefined),
        append && state.restoreCursor === ''
          ? Promise.resolve<CursorPage<ConfigRestore>>({ items: [] })
          : client.listRestoreHistory(append ? state.restoreCursor : undefined),
        append && state.restartCursor === ''
          ? Promise.resolve<CursorPage<NginxRestart>>({ items: [] })
          : client.listRestartHistory(append ? state.restartCursor : undefined),
      ])
      state.releases = append ? [...state.releases, ...releases.items] : releases.items
      state.releaseCursor = releases.next_cursor ?? ''
      state.restores = append ? [...state.restores, ...restores.items] : restores.items
      state.restoreCursor = restores.next_cursor ?? ''
      state.restarts = append ? [...state.restarts, ...restarts.items] : restarts.items
      state.restartCursor = restarts.next_cursor ?? ''
    } catch (error: unknown) {
      handleError(error, 'Operation history could not be loaded.')
      throw error
    } finally {
      state.pending = ''
    }
  }

  async function loadAudit(append = false): Promise<void> {
    if (disposed || state.pending === 'audit') return
    state.pending = 'audit'
    state.error = ''
    try {
      const page = await client.listAuditEvents(
        append && state.auditCursor !== '' ? state.auditCursor : undefined,
      )
      state.audit = append ? [...state.audit, ...page.items] : page.items
      state.auditCursor = page.next_cursor ?? ''
    } catch (error: unknown) {
      handleError(error, 'Audit evidence could not be loaded.')
      throw error
    } finally {
      state.pending = ''
    }
  }

  async function startRestore(
    backup: ConfigBackup,
    reason: string,
    confirmation: string,
    attentionCaseID = '',
  ): Promise<ConfigRestore> {
    if (state.pending !== '') throw new Error('another operation request is pending')
    if (backup.state !== 'complete' || !backup.body_present || confirmation !== backup.id ||
      reason.trim() !== reason || reason.length < 1 || reason.length > 256) {
      throw new Error('verified backup evidence and exact confirmation are required')
    }
    state.pending = 'restore'
    state.error = ''
    try {
      const restore = await client.createRestore(backup.id, {
        attention_case_id: attentionCaseID, reason, confirm_backup_id: confirmation,
      }, csrfToken())
      state.activeRestore = restore
      state.activeRestart = null
      connect('restore', restore.id)
      return restore
    } catch (error: unknown) {
      handleError(error, 'The restore task could not be queued.')
      throw error
    } finally {
      state.pending = ''
    }
  }

  async function startRestart(
    reason: string,
    confirmation: string,
    attentionCaseID = '',
  ): Promise<NginxRestart> {
    if (state.pending !== '') throw new Error('another operation request is pending')
    if (confirmation !== 'RESTART NGINX' || reason.trim() !== reason ||
      reason.length < 1 || reason.length > 256) {
      throw new Error('exact restart confirmation and a bounded reason are required')
    }
    state.pending = 'restart'
    state.error = ''
    try {
      const restart = await client.createRestart({
        attention_case_id: attentionCaseID, reason, confirmation,
      }, csrfToken())
      state.activeRestart = restart
      state.activeRestore = null
      connect('restart', restart.id)
      return restart
    } catch (error: unknown) {
      handleError(error, 'The fixed restart task could not be queued.')
      throw error
    } finally {
      state.pending = ''
    }
  }

  async function changeProtection(
    backup: ConfigBackup,
    protectedValue: boolean,
    reason: string,
    confirmation: string,
  ): Promise<ConfigBackup> {
    if (protectedValue === backup.manually_protected) throw new Error('protection state is unchanged')
    if ((protectedValue && (reason.trim() !== reason || reason.length < 1 || confirmation !== '')) ||
      (!protectedValue && (reason !== '' || confirmation !== backup.id))) {
      throw new Error('protection confirmation is invalid')
    }
    state.pending = 'protection'
    state.error = ''
    try {
      const updated = await client.changeBackupProtection(backup.id, {
        expected_protected: backup.manually_protected, protected: protectedValue,
        reason, confirmation,
      }, csrfToken())
      replaceBackup(updated)
      return updated
    } catch (error: unknown) {
      handleError(error, 'Backup protection could not be changed.')
      throw error
    } finally {
      state.pending = ''
    }
  }

  async function planRetention(): Promise<RetentionRun> {
    state.pending = 'retention-plan'
    state.error = ''
    try {
      const run = await client.planBackupRetention(csrfToken())
      state.retention = run
      return run
    } catch (error: unknown) {
      handleError(error, 'A retention dry-run could not be created.')
      throw error
    } finally {
      state.pending = ''
    }
  }

  async function executeRetention(confirmation: string): Promise<RetentionRun> {
    const current = state.retention
    if (current === null || current.state !== 'planned' || confirmation !== current.id) {
      throw new Error('an exact current retention plan confirmation is required')
    }
    state.pending = 'retention-execute'
    state.error = ''
    try {
      const run = await client.executeBackupRetention(current.id, confirmation, csrfToken())
      state.retention = run
      scheduleRetentionRefresh()
      return run
    } catch (error: unknown) {
      handleError(error, 'The retention plan could not be started.')
      throw error
    } finally {
      state.pending = ''
    }
  }

  async function verifyAttention(id: string): Promise<RuntimeVerification> {
    state.pending = 'verification'
    state.error = ''
    try {
      const verification = await client.verifyAttentionCase(id, csrfToken())
      state.verification = verification
      await loadOverview()
      return verification
    } catch (error: unknown) {
      handleError(error, 'Current runtime health could not resolve this attention case.')
      throw error
    } finally {
      state.pending = ''
    }
  }

  function refreshActive(): Promise<void> {
    if (refreshPromise !== null) return refreshPromise
    refreshPromise = (async () => {
      if (state.activeRestore !== null) {
        state.activeRestore = await client.getRestore(state.activeRestore.id)
        if (isTerminalRestore(state.activeRestore)) finishTrackedTask()
      } else if (state.activeRestart !== null) {
        state.activeRestart = await client.getRestart(state.activeRestart.id)
        if (isTerminalRestart(state.activeRestart)) finishTrackedTask()
      }
    })().catch((error: unknown) => {
      handleError(error, 'Operation progress could not be refreshed.')
      throw error
    }).finally(() => {
      refreshPromise = null
    })
    return refreshPromise
  }

  function connect(kind: 'restore' | 'restart', id: string): void {
    closeStream()
    state.stream = 'connecting'
    const url = kind === 'restore'
      ? `/api/v1/config/restores/${id}/events`
      : `/api/v1/nginx/restarts/${id}/events`
    const current = createStream(url)
    stream = current
    current.addEventListener('open', () => {
      if (stream === current) state.stream = 'live'
    })
    for (const event of ['snapshot', 'stage', 'terminal']) {
      current.addEventListener(event, () => {
        if (stream === current) void refreshActive()
      })
    }
    current.addEventListener('error', () => {
      if (stream === current) state.stream = 'reconnecting'
    })
  }

  function finishTrackedTask(): void {
    closeStream()
    void refreshEvidenceAfterTask()
  }

  async function refreshEvidenceAfterTask(): Promise<void> {
    for (const loadEvidence of [loadOverview, loadBackups, loadHistory]) {
      try {
        await loadEvidence()
      } catch {
        // Each loader has already preserved its scoped, session-aware error.
      }
    }
  }

  function closeStream(): void {
    stream?.close()
    stream = null
    state.stream = 'closed'
  }

  function scheduleRetentionRefresh(): void {
    stopRetentionPolling()
    retentionTimer = setTimeout(() => void refreshRetention(), 2_000)
  }

  async function refreshRetention(): Promise<void> {
    const id = state.retention?.id
    if (id === undefined || disposed) return
    try {
      const run = await client.getBackupRetention(id)
      state.retention = run
      if (run.state === 'executing') scheduleRetentionRefresh()
      else void loadBackups()
    } catch (error: unknown) {
      handleError(error, 'Retention progress could not be refreshed.')
    }
  }

  function stopRetentionPolling(): void {
    if (retentionTimer !== undefined) clearTimeout(retentionTimer)
    retentionTimer = undefined
  }

  function replaceController(): AbortController {
    activeController?.abort()
    const controller = new AbortController()
    activeController = controller
    return controller
  }

  function replaceBackup(backup: ConfigBackup): void {
    const index = state.backups.findIndex((item) => item.id === backup.id)
    if (index >= 0) state.backups[index] = backup
  }

  function handleError(error: unknown, message: string): void {
    if (!sessions.handleAPIError(error)) state.error = message
  }

  function dispose(): void {
    disposed = true
    activeController?.abort()
    activeController = null
    closeStream()
    stopRetentionPolling()
    removeExpiryListener()
  }

  return {
    state, loadOverview, loadBackups, loadHistory, loadAudit, startRestore, startRestart,
    changeProtection, planRetention, executeRetention, verifyAttention, refreshActive, dispose,
  }
}

export function isTerminalRestore(restore: ConfigRestore | null): boolean {
  return restore !== null && ['succeeded', 'failed', 'rolled_back', 'needs_attention', 'cancelled'].includes(restore.state)
}

export function isTerminalRestart(restart: NginxRestart | null): boolean {
  return restart !== null && ['succeeded', 'failed', 'needs_attention', 'cancelled'].includes(restart.state)
}

export const operationsStore = createOperationsStore(apiClient, sessionStore)

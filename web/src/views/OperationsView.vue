<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.3
-->
<template>
  <section
    class="operations-view"
    :aria-busy="state.phase === 'loading'"
  >
    <header class="operations-view__header">
      <div>
        <p class="operations-view__eyebrow">
          Configuration control
        </p>
        <h1>Recovery &amp; History</h1>
        <p>Review durable evidence, recover from verified backups, and run the fixed Nginx restart.</p>
      </div>
      <button
        type="button"
        :disabled="state.pending !== ''"
        @click="refreshAll"
      >
        {{ state.pending === 'overview' ? 'Refreshing…' : 'Refresh evidence' }}
      </button>
    </header>

    <InlineBanner
      v-if="state.error !== ''"
      kind="agent"
      :message="state.error"
    />

    <OperationsTabs
      :active="activeTab"
      @select="selectTab"
    />

    <section
      class="operations-view__attention"
      aria-labelledby="attention-title"
    >
      <header>
        <div>
          <h2 id="attention-title">
            Open attention cases
          </h2>
          <p>Production mutations remain blocked until current evidence proves a safe resolution.</p>
        </div>
        <StatusBadge
          :tone="state.attention.length === 0 ? 'success' : 'error'"
          :label="state.attention.length === 0 ? 'No open cases' : `${state.attention.length} open`"
        />
      </header>
      <p v-if="state.phase === 'loading' && state.attention.length === 0">
        Loading attention evidence…
      </p>
      <p v-else-if="state.attention.length === 0">
        No unresolved production or runtime state is recorded.
      </p>
      <article
        v-for="(attentionCase, index) in state.attention"
        :id="`attention-${attentionCase.id}`"
        :key="attentionCase.id"
        data-attention-case
        :role="index === 0 ? 'alert' : undefined"
        :aria-labelledby="`attention-title-${attentionCase.id}`"
      >
        <header>
          <div>
            <StatusBadge
              tone="error"
              label="Needs attention"
            />
            <h3 :id="`attention-title-${attentionCase.id}`">
              {{ subjectLabel(attentionCase.subject_type) }} consistency cannot be confirmed
            </h3>
          </div>
          <time :datetime="attentionCase.opened_at">Opened {{ formatTime(attentionCase.opened_at) }}</time>
        </header>
        <dl>
          <div><dt>Case</dt><dd><code>{{ abbreviate(attentionCase.id) }}</code></dd></div>
          <div><dt>Subject</dt><dd><code>{{ abbreviate(attentionCase.subject_id) }}</code></dd></div>
          <div><dt>Safe reason</dt><dd>{{ enumLabel(attentionCase.reason_code) }}</dd></div>
          <div v-if="attentionCase.backup_id !== undefined">
            <dt>Recovery point</dt><dd><code>{{ abbreviate(attentionCase.backup_id) }}</code></dd>
          </div>
        </dl>
        <div class="operations-view__actions">
          <button
            type="button"
            data-action="verify-attention"
            :disabled="state.pending !== ''"
            @click="verifyAttention(attentionCase.id)"
          >
            Verify current state
          </button>
          <button
            type="button"
            data-action="restart-for-attention"
            :disabled="!restartAvailable"
            @click="openRestart(attentionCase.id)"
          >
            Restart Nginx for this case
          </button>
          <button
            v-if="attentionBackup(attentionCase) !== null"
            type="button"
            data-action="restore-for-attention"
            :disabled="state.pending !== ''"
            @click="openAttentionRestore(attentionCase)"
          >
            Restore referenced backup
          </button>
        </div>
        <p v-if="attentionCase.backup_id !== undefined && attentionBackup(attentionCase) === null">
          The referenced backup is not present in the currently loaded evidence page.
        </p>
      </article>
    </section>

    <section
      class="operations-view__runtime"
      data-runtime-control
      aria-labelledby="runtime-control-title"
    >
      <header>
        <div>
          <h2 id="runtime-control-title">
            Runtime control
          </h2>
          <p>Fixed Agent operations only; command, PID, signal, path, and timeout input are unavailable.</p>
        </div>
        <StatusBadge
          :tone="runtimeTone"
          :label="runtimeLabel"
        />
      </header>
      <dl class="operations-view__runtime-grid">
        <div>
          <dt>Sampled</dt>
          <dd>{{ state.runtime === null ? 'Unavailable' : formatTime(state.runtime.sampled_at) }}</dd>
        </div>
        <div>
          <dt>Production validation</dt>
          <dd>{{ validationLabel }}</dd>
        </div>
        <div>
          <dt>Master</dt>
          <dd>{{ state.runtime?.master === null || state.runtime === null ? 'Not observed' : `PID ${state.runtime.master.pid}` }}</dd>
        </div>
        <div>
          <dt>Workers</dt>
          <dd>{{ state.runtime?.workers.length ?? 0 }}</dd>
        </div>
        <div>
          <dt>Agent</dt>
          <dd>{{ state.runtime?.components.agent === 'healthy' ? 'Available' : 'Unavailable' }}</dd>
        </div>
        <div>
          <dt>Latest restart</dt>
          <dd>{{ latestRestartLabel }}</dd>
        </div>
      </dl>
      <div class="operations-view__actions">
        <button
          type="button"
          data-action="restart-nginx"
          :disabled="!restartAvailable"
          @click="openRestart()"
        >
          Restart Nginx
        </button>
        <span v-if="!restartAvailable">{{ restartUnavailableReason }}</span>
      </div>
    </section>

    <section
      class="operations-view__summary"
      aria-label="Backup evidence summary"
    >
      <article>
        <strong>{{ state.backups.length }}</strong>
        <span>Indexed backups shown</span>
      </article>
      <article>
        <strong>{{ completeBackupCount }}</strong>
        <span>Complete recovery points</span>
      </article>
      <article>
        <strong>{{ protectedBackupCount }}</strong>
        <span>Protected recovery points</span>
      </article>
    </section>

    <section
      v-show="activeTab === 'overview'"
      id="operations-panel-overview"
      role="tabpanel"
      aria-labelledby="operations-tab-overview"
      tabindex="0"
      class="operations-view__panel"
    >
      <header>
        <h2>Current operation</h2>
        <p>Progress is rebuilt from durable task resources; leaving this page does not cancel a task.</p>
      </header>
      <OperationTimeline
        v-if="state.activeRestore !== null"
        title="Restore progress"
        :operation-id="state.activeRestore.id"
        :state="state.activeRestore.state"
        :stage="state.activeRestore.stage"
        :stages="state.activeRestore.stages"
        :stream-state="state.stream"
      />
      <OperationTimeline
        v-else-if="state.activeRestart !== null"
        title="Restart progress"
        :operation-id="state.activeRestart.id"
        :state="state.activeRestart.state"
        :stage="state.activeRestart.stage"
        :stages="state.activeRestart.stages"
        :stream-state="state.stream"
      />
      <p v-else>
        No restore or restart task is currently tracked in this browser session.
      </p>
      <section
        v-if="state.verification !== null"
        class="operations-view__verification"
        :data-state="state.verification.state"
        aria-labelledby="verification-title"
      >
        <h3 id="verification-title">
          Latest fixed verification
        </h3>
        <p>{{ state.verification.state === 'succeeded' ? 'Production configuration and runtime health were confirmed.' : 'Current evidence did not resolve the attention case.' }}</p>
        <p>Verification ID: <code>{{ state.verification.id }}</code></p>
      </section>
    </section>

    <section
      v-show="activeTab === 'backups'"
      id="operations-panel-backups"
      role="tabpanel"
      aria-labelledby="operations-tab-backups"
      tabindex="0"
      class="operations-view__panel"
    >
      <BackupInventory
        :backups="state.backups"
        :loading="state.pending === 'backups'"
        :next-cursor="state.backupsCursor"
        @load-more="loadMoreBackups"
        @protect="openProtection($event, true)"
        @restore="openRestore"
        @unprotect="openProtection($event, false)"
      />

      <section
        class="operations-view__retention"
        aria-labelledby="retention-title"
      >
        <header>
          <div>
            <h2 id="retention-title">
              Backup retention
            </h2>
            <p>Dry-run first. Protected, active, and minimum recovery points are never selected for deletion.</p>
          </div>
          <StatusBadge
            v-if="state.retention !== null"
            :tone="retentionTone"
            :label="enumLabel(state.retention.state)"
          />
        </header>
        <div class="operations-view__actions">
          <button
            type="button"
            data-action="plan-retention"
            :disabled="state.pending !== ''"
            @click="planRetention"
          >
            {{ state.retention === null ? 'Create retention dry-run' : 'Create fresh dry-run' }}
          </button>
          <button
            v-if="state.retention?.state === 'planned'"
            type="button"
            data-action="execute-retention"
            :disabled="state.pending !== ''"
            @click="openRetentionExecution"
          >
            Execute this exact plan
          </button>
        </div>
        <template v-if="state.retention !== null">
          <dl class="operations-view__retention-grid">
            <div><dt>Run ID</dt><dd><code>{{ state.retention.id }}</code></dd></div>
            <div><dt>Expires</dt><dd>{{ formatTime(state.retention.expires_at) }}</dd></div>
            <div><dt>Minimum complete</dt><dd>{{ state.retention.policy.minimum_complete }}</dd></div>
            <div><dt>Maximum complete</dt><dd>{{ state.retention.policy.maximum_complete }}</dd></div>
            <div><dt>Maximum bytes</dt><dd>{{ formatBytes(state.retention.policy.maximum_total_bytes) }}</dd></div>
            <div><dt>Minimum age</dt><dd>{{ formatDuration(state.retention.policy.minimum_age_seconds) }}</dd></div>
            <div><dt>Protected</dt><dd>{{ state.retention.protected_count }}</dd></div>
            <div><dt>Planned deletion</dt><dd>{{ state.retention.delete_count }} / {{ formatBytes(state.retention.delete_bytes) }}</dd></div>
          </dl>
          <p class="operations-view__dry-run">
            {{ state.retention.state === 'planned' ? 'Dry-run only: no backup has been deleted.' : `Persisted result: ${enumLabel(state.retention.state)}.` }}
          </p>
          <ol class="operations-view__retention-items">
            <li
              v-for="item in state.retention.items"
              :key="item.ordinal"
            >
              <span>{{ item.ordinal }}. <code>{{ abbreviate(item.backup_id) }}</code></span>
              <strong>{{ retentionItemLabel(item.state) }}</strong>
              <span>{{ formatBytes(item.snapshot_total_bytes) }} · {{ enumLabel(item.reason_code) }}</span>
            </li>
          </ol>
        </template>
        <p v-else>
          No retention plan is loaded. Creating a dry-run cannot delete backup content.
        </p>
      </section>
    </section>

    <section
      v-show="activeTab === 'history'"
      id="operations-panel-history"
      role="tabpanel"
      aria-labelledby="operations-tab-history"
      tabindex="0"
      class="operations-view__panel"
    >
      <header>
        <h2>Configuration history</h2>
        <p>Each group preserves the server-provided newest-first order; independently paged resources are not browser-time merged.</p>
      </header>
      <section
        v-for="group in historyGroups"
        :key="group.kind"
        class="operations-view__history-group"
        :aria-labelledby="`history-${group.kind}`"
      >
        <h3 :id="`history-${group.kind}`">
          {{ group.label }}
        </h3>
        <p v-if="group.items.length === 0">
          No {{ group.label.toLowerCase() }} are recorded on this page.
        </p>
        <article
          v-for="item in group.items"
          :key="item.id"
          class="operations-view__history-item"
        >
          <header>
            <div>
              <strong>{{ group.singular }} <code>{{ abbreviate(item.id) }}</code></strong>
              <span>{{ formatTime(item.created_at) }}</span>
            </div>
            <StatusBadge
              :tone="operationTone(item.state)"
              :label="enumLabel(item.state)"
            />
          </header>
          <details>
            <summary>Review persisted stage evidence</summary>
            <OperationTimeline
              :title="`${group.singular} evidence`"
              :operation-id="item.id"
              :state="item.state"
              :stage="item.stage"
              :stages="item.stages"
            />
          </details>
        </article>
      </section>
      <button
        v-if="hasMoreHistory"
        type="button"
        :disabled="state.pending !== ''"
        @click="loadMoreHistory"
      >
        {{ state.pending === 'history' ? 'Loading…' : 'Load more history' }}
      </button>
    </section>

    <section
      v-show="activeTab === 'audit'"
      id="operations-panel-audit"
      role="tabpanel"
      aria-labelledby="operations-tab-audit"
      tabindex="0"
      class="operations-view__panel"
    >
      <header>
        <h2>Audit evidence</h2>
        <p>Only bounded, server-whitelisted details are rendered; configuration content and raw output are unavailable.</p>
      </header>
      <p v-if="state.pending === 'audit' && state.audit.length === 0">
        Loading audit evidence…
      </p>
      <p v-else-if="state.audit.length === 0">
        No audit events are available on this page.
      </p>
      <div
        v-else
        class="operations-view__audit-table"
      >
        <table>
          <caption>Bounded configuration recovery and runtime audit events</caption>
          <thead>
            <tr>
              <th scope="col">
                Time
              </th>
              <th scope="col">
                Actor
              </th>
              <th scope="col">
                Action
              </th>
              <th scope="col">
                Object
              </th>
              <th scope="col">
                Result
              </th>
              <th scope="col">
                Request
              </th>
              <th scope="col">
                Safe details
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="event in state.audit"
              :key="event.id"
            >
              <td><time :datetime="event.occurred_at">{{ formatTime(event.occurred_at) }}</time></td>
              <td>{{ event.actor_name }}</td>
              <td>{{ actionLabel(event.action) }}</td>
              <td>{{ enumLabel(event.object_type) }} <code>{{ abbreviate(event.object_id) }}</code></td>
              <td>{{ enumLabel(event.result) }}</td>
              <td>
                <code>{{ abbreviate(event.request_id) }}</code>
                <button
                  type="button"
                  :aria-label="`Copy request ID ${event.request_id}`"
                  @click="copyRequestID(event.request_id)"
                >
                  Copy
                </button>
              </td>
              <td><span class="operations-view__safe-details">{{ safeDetails(event.details) }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div
        v-if="state.audit.length > 0"
        class="operations-view__audit-cards"
      >
        <article
          v-for="event in state.audit"
          :key="event.id"
        >
          <header>
            <strong>{{ actionLabel(event.action) }}</strong>
            <span>{{ enumLabel(event.result) }}</span>
          </header>
          <dl>
            <div><dt>Time</dt><dd>{{ formatTime(event.occurred_at) }}</dd></div>
            <div><dt>Actor</dt><dd>{{ event.actor_name }}</dd></div>
            <div><dt>Object</dt><dd>{{ enumLabel(event.object_type) }} <code>{{ abbreviate(event.object_id) }}</code></dd></div>
            <div><dt>Request</dt><dd><code>{{ event.request_id }}</code></dd></div>
            <div><dt>Safe details</dt><dd>{{ safeDetails(event.details) }}</dd></div>
          </dl>
          <button
            type="button"
            @click="copyRequestID(event.request_id)"
          >
            Copy request ID
          </button>
        </article>
      </div>
      <button
        v-if="state.auditCursor !== ''"
        type="button"
        :disabled="state.pending !== ''"
        @click="loadMoreAudit"
      >
        {{ state.pending === 'audit' ? 'Loading…' : 'Load more audit events' }}
      </button>
      <p
        v-if="copyMessage !== ''"
        class="operations-view__copy-status"
        role="status"
      >
        {{ copyMessage }}
      </p>
    </section>

    <OperationConfirmModal
      :open="modalKind !== null"
      :title="modalTitle"
      :consequence="modalConsequence"
      :confirmation-text="modalConfirmation"
      :confirm-label="modalConfirmLabel"
      :requires-reason="modalRequiresReason"
      :pending="modalSubmitting"
      :trigger="modalTrigger"
      @cancel="closeModal"
      @confirm="confirmModal"
    >
      <dl class="operations-view__modal-evidence">
        <template v-if="selectedBackup !== null">
          <div><dt>Backup ID</dt><dd><code>{{ selectedBackup.id }}</code></dd></div>
          <div><dt>Source</dt><dd>{{ sourceLabel(selectedBackup) }}</dd></div>
          <div><dt>Verified</dt><dd>{{ selectedBackup.verified_at === undefined ? 'Not verified' : formatTime(selectedBackup.verified_at) }}</dd></div>
          <div><dt>Production identity</dt><dd><code>{{ abbreviate(selectedBackup.production_digest) }}</code></dd></div>
          <div><dt>Size</dt><dd>{{ formatBytes(selectedBackup.total_bytes) }}</dd></div>
        </template>
        <template v-else-if="modalKind === 'restart'">
          <div><dt>Operation</dt><dd>Fixed Agent restart</dd></div>
          <div><dt>Files</dt><dd>Production configuration is not modified</dd></div>
          <div><dt>Health proof</dt><dd>New master, workers, validation, and loopback HTTP</dd></div>
        </template>
        <template v-else-if="modalKind === 'retention' && state.retention !== null">
          <div><dt>Run ID</dt><dd><code>{{ state.retention.id }}</code></dd></div>
          <div><dt>Deletion candidates</dt><dd>{{ state.retention.delete_count }}</dd></div>
          <div><dt>Planned bytes</dt><dd>{{ formatBytes(state.retention.delete_bytes) }}</dd></div>
          <div><dt>Expires</dt><dd>{{ formatTime(state.retention.expires_at) }}</dd></div>
        </template>
      </dl>
    </OperationConfirmModal>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import type {
  AttentionCase,
  ConfigBackup,
  ConfigRestore,
  NginxRestart,
  Release,
  RetentionItemState,
} from '../api/types'
import BackupInventory from '../components/BackupInventory.vue'
import InlineBanner from '../components/InlineBanner.vue'
import OperationConfirmModal from '../components/OperationConfirmModal.vue'
import OperationTimeline from '../components/OperationTimeline.vue'
import OperationsTabs, { type OperationsTab } from '../components/OperationsTabs.vue'
import StatusBadge, { type StatusTone } from '../components/StatusBadge.vue'
import { operationsStore, type OperationsStore } from '../operations'

interface Props {
  store?: OperationsStore
}

type ModalKind = 'restart' | 'restore' | 'protect' | 'unprotect' | 'retention'
type HistoryItem = Release | ConfigRestore | NginxRestart

const props = defineProps<Props>()
const route = useRoute()
const router = useRouter()
const operations = props.store ?? operationsStore
const state = operations.state
const modalKind = ref<ModalKind | null>(null)
const modalTrigger = ref<HTMLElement | null>(null)
const modalSubmitting = ref(false)
const selectedBackup = ref<ConfigBackup | null>(null)
const selectedAttentionID = ref<string | undefined>()
const copyMessage = ref('')
const validTabs = new Set<OperationsTab>(['overview', 'backups', 'history', 'audit'])

const activeTab = computed<OperationsTab>(() => {
  const query = route.query.tab
  return typeof query === 'string' && validTabs.has(query as OperationsTab)
    ? query as OperationsTab
    : 'overview'
})
const completeBackupCount = computed(() =>
  state.backups.filter(({ state: backupState, body_present: present }) =>
    backupState === 'complete' && present,
  ).length,
)
const protectedBackupCount = computed(() =>
  state.backups.filter(({ protected: protectedValue }) => protectedValue).length,
)
const runtimeTone = computed<StatusTone>(() => {
  switch (state.runtime?.components.nginx) {
    case 'running': return 'success'
    case 'degraded': return 'warning'
    case 'stopped': return 'error'
    default: return 'unknown'
  }
})
const runtimeLabel = computed(() =>
  `Nginx ${state.runtime?.components.nginx ?? 'unknown'}`,
)
const validationLabel = computed(() => {
  const validation = state.runtime?.startup_validation
  if (validation === null || validation === undefined) return 'No evidence'
  return `${validation.valid ? 'Valid' : 'Invalid'} at ${formatTime(validation.checked_at)}`
})
const latestRestartLabel = computed(() => {
  const latest = state.restarts[0]
  return latest === undefined
    ? 'No restart history loaded'
    : `${enumLabel(latest.state)} · ${formatTime(latest.updated_at)}`
})
const restartAvailable = computed(() =>
  state.runtime?.components.agent === 'healthy' && state.pending === '',
)
const restartUnavailableReason = computed(() => {
  if (state.runtime === null) return 'Runtime evidence is unavailable.'
  if (state.runtime.components.agent !== 'healthy') return 'Configuration Agent evidence is unavailable.'
  return 'Another request is pending.'
})
const retentionTone = computed<StatusTone>(() => {
  switch (state.retention?.state) {
    case 'succeeded': return 'success'
    case 'planned':
    case 'executing': return 'warning'
    case 'failed':
    case 'needs_attention': return 'error'
    default: return 'unknown'
  }
})
const hasMoreHistory = computed(() =>
  state.releaseCursor !== '' || state.restoreCursor !== '' || state.restartCursor !== '',
)
const historyGroups = computed<Array<{
  kind: string
  label: string
  singular: string
  items: HistoryItem[]
}>>(() => [
  { kind: 'release', label: 'Releases', singular: 'Release', items: state.releases },
  { kind: 'restore', label: 'Restores', singular: 'Restore', items: state.restores },
  { kind: 'restart', label: 'Restarts', singular: 'Restart', items: state.restarts },
])
const modalTitle = computed(() => {
  switch (modalKind.value) {
    case 'restart': return 'Restart Nginx?'
    case 'restore': return `Restore backup “${selectedBackup.value?.id ?? ''}”?`
    case 'protect': return `Protect backup “${selectedBackup.value?.id ?? ''}”?`
    case 'unprotect': return `Remove manual protection from “${selectedBackup.value?.id ?? ''}”?`
    case 'retention': return `Execute retention plan “${state.retention?.id ?? ''}”?`
    default: return 'Confirm operation'
  }
})
const modalConsequence = computed(() => {
  switch (modalKind.value) {
    case 'restart':
      return 'Nginx will briefly stop serving while the fixed supervisor operation replaces the master. Production configuration is validated first and files are not modified.'
    case 'restore':
      return 'The Agent will validate the target, create a safety backup, restore production, validate, reload, and confirm runtime health. Closing this dialog after submission does not cancel the task.'
    case 'protect':
      return 'The backup remains immutable and retention will keep this recovery point until manual protection is removed.'
    case 'unprotect':
      return 'Only manual protection is removed. System, active-task, attention, and minimum-set protection still apply.'
    case 'retention':
      return 'Only the exact, unexpired dry-run will execute. Every protected or changed item is skipped or fails closed with persisted evidence.'
    default:
      return ''
  }
})
const modalConfirmation = computed(() => {
  switch (modalKind.value) {
    case 'restart': return 'RESTART NGINX'
    case 'restore':
    case 'unprotect': return selectedBackup.value?.id ?? ''
    case 'retention': return state.retention?.id ?? ''
    default: return ''
  }
})
const modalConfirmLabel = computed(() => {
  switch (modalKind.value) {
    case 'restart': return 'Restart Nginx'
    case 'restore': return 'Start verified restore'
    case 'protect': return 'Protect backup'
    case 'unprotect': return 'Remove manual protection'
    case 'retention': return 'Execute retention plan'
    default: return 'Confirm'
  }
})
const modalRequiresReason = computed(() =>
  modalKind.value === 'restart' || modalKind.value === 'restore' || modalKind.value === 'protect',
)

onMounted(() => {
  void hydrate(activeTab.value)
})

watch(activeTab, (tab) => {
  void loadTab(tab)
})

async function hydrate(tab: OperationsTab): Promise<void> {
  const requests: Array<Promise<void>> = [
    operations.loadOverview(), operations.loadBackups(), operations.loadHistory(),
  ]
  if (tab === 'audit') requests.push(operations.loadAudit())
  await Promise.allSettled(requests)
}

async function loadTab(tab: OperationsTab): Promise<void> {
  try {
    if (tab === 'backups') await operations.loadBackups()
    else if (tab === 'history') await operations.loadHistory()
    else if (tab === 'audit') await operations.loadAudit()
  } catch {
    // The store preserves the user-facing error and session-expiry behavior.
  }
}

function selectTab(tab: OperationsTab): void {
  const query = { ...route.query }
  if (tab === 'overview') delete query.tab
  else query.tab = tab
  void router.replace({ query })
}

async function refreshAll(): Promise<void> {
  await hydrate(activeTab.value)
}

function attentionBackup(attentionCase: AttentionCase): ConfigBackup | null {
  if (attentionCase.backup_id === undefined) return null
  return state.backups.find(({ id }) => id === attentionCase.backup_id) ?? null
}

async function verifyAttention(id: string): Promise<void> {
  try {
    await operations.verifyAttention(id)
  } catch {
    // The persistent store error remains next to the evidence.
  }
}

function rememberTrigger(): void {
  modalTrigger.value = document.activeElement instanceof HTMLElement
    ? document.activeElement
    : null
}

function openRestart(attentionID?: string): void {
  rememberTrigger()
  selectedBackup.value = null
  selectedAttentionID.value = attentionID
  modalKind.value = 'restart'
}

function openRestore(backup: ConfigBackup, attentionID?: string): void {
  rememberTrigger()
  selectedBackup.value = backup
  selectedAttentionID.value = attentionID
  modalKind.value = 'restore'
}

function openAttentionRestore(attentionCase: AttentionCase): void {
  const backup = attentionBackup(attentionCase)
  if (backup !== null) openRestore(backup, attentionCase.id)
}

function openProtection(backup: ConfigBackup, protectedValue: boolean): void {
  rememberTrigger()
  selectedBackup.value = backup
  selectedAttentionID.value = undefined
  modalKind.value = protectedValue ? 'protect' : 'unprotect'
}

function openRetentionExecution(): void {
  rememberTrigger()
  selectedBackup.value = null
  selectedAttentionID.value = undefined
  modalKind.value = 'retention'
}

function closeModal(): void {
  if (modalSubmitting.value) return
  modalKind.value = null
  selectedBackup.value = null
  selectedAttentionID.value = undefined
}

async function confirmModal(reason: string, confirmation: string): Promise<void> {
  if (modalSubmitting.value || modalKind.value === null) return
  modalSubmitting.value = true
  try {
    switch (modalKind.value) {
      case 'restart':
        await operations.startRestart(reason, confirmation, selectedAttentionID.value)
        selectTab('overview')
        break
      case 'restore':
        if (selectedBackup.value === null) return
        await operations.startRestore(
          selectedBackup.value,
          reason,
          confirmation,
          selectedAttentionID.value,
        )
        selectTab('overview')
        break
      case 'protect':
        if (selectedBackup.value === null) return
        await operations.changeProtection(selectedBackup.value, true, reason, '')
        break
      case 'unprotect':
        if (selectedBackup.value === null) return
        await operations.changeProtection(selectedBackup.value, false, '', confirmation)
        break
      case 'retention':
        await operations.executeRetention(confirmation)
        break
    }
    modalKind.value = null
    selectedBackup.value = null
    selectedAttentionID.value = undefined
  } catch {
    // Keep the modal and the store's persistent error visible for a deliberate retry or cancel.
  } finally {
    modalSubmitting.value = false
  }
}

async function planRetention(): Promise<void> {
  try {
    await operations.planRetention()
  } catch {
    // The store owns the persistent error.
  }
}

async function loadMoreBackups(): Promise<void> {
  try {
    await operations.loadBackups(true)
  } catch {
    // The store owns the persistent error.
  }
}

async function loadMoreHistory(): Promise<void> {
  try {
    await operations.loadHistory(true)
  } catch {
    // The store owns the persistent error.
  }
}

async function loadMoreAudit(): Promise<void> {
  try {
    await operations.loadAudit(true)
  } catch {
    // The store owns the persistent error.
  }
}

async function copyRequestID(value: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(value)
    copyMessage.value = 'Request ID copied.'
  } catch {
    copyMessage.value = 'Request ID could not be copied. Select the visible value instead.'
  }
}

function operationTone(value: string): StatusTone {
  switch (value) {
    case 'succeeded': return 'success'
    case 'running':
    case 'rolling_back':
    case 'queued': return 'warning'
    case 'failed':
    case 'needs_attention': return 'error'
    default: return 'unknown'
  }
}

function retentionItemLabel(value: RetentionItemState): string {
  switch (value) {
    case 'skipped_protected': return 'Skipped — protected'
    case 'needs_attention': return 'Needs attention'
    default: return enumLabel(value)
  }
}

function subjectLabel(value: AttentionCase['subject_type']): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

function actionLabel(value: string): string {
  return value.split('.').map(enumLabel).join(' · ')
}

function enumLabel(value: string): string {
  const words = value.replaceAll('_', ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

function abbreviate(value: string): string {
  return value.length <= 16 ? value : `${value.slice(0, 8)}…${value.slice(-4)}`
}

function sourceLabel(backup: ConfigBackup): string {
  return `${enumLabel(backup.origin_type)} ${abbreviate(backup.origin_id)}`
}

function formatTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`
  return `${(value / (1024 * 1024)).toFixed(1)} MiB`
}

function formatDuration(seconds: number): string {
  if (seconds % 86400 === 0) return `${seconds / 86400} day(s)`
  if (seconds % 3600 === 0) return `${seconds / 3600} hour(s)`
  return `${seconds} seconds`
}

function safeDetails(details: Readonly<Record<string, string | number | boolean>>): string {
  const entries = Object.entries(details)
  if (entries.length === 0) return 'No public details'
  return entries.map(([key, value]) => `${enumLabel(key)}: ${String(value)}`).join(' · ')
}
</script>

<style scoped>
.operations-view {
  display: grid;
  min-width: 0;
  gap: var(--spacing-xl);
}

.operations-view__header,
.operations-view__attention > header,
.operations-view__attention article > header,
.operations-view__runtime > header,
.operations-view__retention > header,
.operations-view__panel > header,
.operations-view__history-item > header,
.operations-view__audit-cards article > header {
  display: flex;
  min-width: 0;
  align-items: start;
  justify-content: space-between;
  gap: var(--spacing-md);
}

.operations-view h1,
.operations-view h2,
.operations-view h3,
.operations-view p,
.operations-view dl {
  margin: 0;
}

.operations-view__header > div,
.operations-view__attention header > div,
.operations-view__runtime header > div,
.operations-view__retention header > div,
.operations-view__panel > header > div {
  min-width: 0;
}

.operations-view__header h1 {
  font-size: clamp(2rem, 5vw, 3.5rem);
  line-height: 1.05;
}

.operations-view__header p:not(.operations-view__eyebrow),
.operations-view__attention > header p,
.operations-view__runtime > header p,
.operations-view__retention > header p,
.operations-view__panel > header p {
  max-width: 72ch;
  color: var(--color-ink-muted-80);
}

.operations-view__eyebrow {
  color: var(--color-primary);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
  letter-spacing: var(--letter-spacing-caption);
  text-transform: uppercase;
}

.operations-view button,
.operations-view summary {
  min-height: var(--component-control-min-size);
}

.operations-view button {
  padding: var(--spacing-xs) var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.operations-view button:disabled {
  border-color: var(--color-hairline);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.operations-view button:active:not(:disabled) {
  transform: scale(0.97);
}

.operations-view code {
  max-width: 100%;
  overflow-wrap: anywhere;
  font-family: var(--font-code);
  font-size: 0.85em;
}

.operations-view__attention,
.operations-view__runtime,
.operations-view__retention,
.operations-view__panel {
  display: grid;
  min-width: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
  gap: var(--spacing-md);
}

.operations-view__runtime {
  min-height: var(--component-operations-summary-min-height);
}

.operations-view__attention article {
  display: grid;
  min-width: 0;
  padding: var(--spacing-md);
  border: var(--component-attention-panel-border-width) solid var(--color-state-danger-foreground);
  border-radius: var(--rounded-sm);
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
  gap: var(--spacing-md);
}

.operations-view__attention article header > div {
  display: grid;
  gap: var(--spacing-xs);
}

.operations-view__attention time,
.operations-view__attention article > p,
.operations-view__actions span {
  font-size: var(--font-size-caption);
}

.operations-view__attention dl,
.operations-view__runtime-grid,
.operations-view__retention-grid,
.operations-view__modal-evidence {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--spacing-md);
}

.operations-view__attention dl div,
.operations-view__runtime-grid div,
.operations-view__retention-grid div,
.operations-view__modal-evidence div {
  min-width: 0;
}

.operations-view dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.operations-view__attention dt {
  color: currentcolor;
}

.operations-view dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.operations-view__actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--spacing-sm);
}

.operations-view__attention .operations-view__actions button {
  border-color: currentcolor;
  background: transparent;
  color: currentcolor;
}

.operations-view__summary {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--spacing-md);
}

.operations-view__summary article {
  display: grid;
  min-height: var(--component-operations-summary-min-height);
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  align-content: center;
  background: var(--color-canvas);
  gap: var(--spacing-xs);
}

.operations-view__summary strong {
  font-size: 2rem;
}

.operations-view__summary span {
  color: var(--color-ink-muted-80);
}

.operations-view__panel[style*='display: none'] {
  padding: 0;
}

.operations-view__verification,
.operations-view__dry-run {
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.operations-view__verification[data-state='succeeded'] {
  border-color: var(--color-state-success-foreground);
  background: var(--color-state-success);
  color: var(--color-state-success-foreground);
}

.operations-view__verification[data-state='failed'] {
  border-color: var(--color-state-danger-foreground);
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}

.operations-view__retention-items {
  display: grid;
  margin: 0;
  padding: 0;
  list-style: none;
}

.operations-view__retention-items li {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  padding-block: var(--spacing-sm);
  border-block-end: 1px solid var(--color-hairline);
  gap: var(--spacing-md);
}

.operations-view__retention-items li span:last-child {
  color: var(--color-ink-muted-80);
  text-align: end;
}

.operations-view__history-group {
  display: grid;
  min-width: 0;
  gap: var(--spacing-sm);
}

.operations-view__history-item {
  display: grid;
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  gap: var(--spacing-sm);
}

.operations-view__history-item header > div {
  display: grid;
  min-width: 0;
}

.operations-view__history-item header span {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.operations-view summary {
  display: flex;
  align-items: center;
  color: var(--color-primary);
  cursor: pointer;
}

.operations-view details > :deep(.operation-timeline) {
  margin-block-start: var(--spacing-sm);
}

.operations-view__audit-table {
  min-width: 0;
  overflow-x: auto;
}

.operations-view__audit-table table {
  width: 100%;
  min-width: var(--component-operations-table-min-width);
  border-collapse: collapse;
}

.operations-view__audit-table caption {
  padding: var(--spacing-sm);
  text-align: start;
  font-size: var(--font-size-caption);
}

.operations-view__audit-table th,
.operations-view__audit-table td {
  padding: var(--spacing-sm);
  border-block-end: 1px solid var(--color-hairline);
  text-align: start;
  vertical-align: top;
}

.operations-view__audit-table th {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.operations-view__audit-table button {
  min-height: var(--component-control-min-size);
  margin-block-start: var(--spacing-xs);
  padding-inline: var(--spacing-sm);
}

.operations-view__safe-details {
  display: block;
  max-width: 32ch;
  overflow-wrap: anywhere;
}

.operations-view__audit-cards {
  display: none;
}

.operations-view__audit-cards article {
  display: grid;
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  gap: var(--spacing-sm);
}

.operations-view__audit-cards dl {
  display: grid;
  gap: var(--spacing-xs);
}

.operations-view__audit-cards dl div {
  display: grid;
  grid-template-columns: minmax(6rem, 0.35fr) minmax(0, 1fr);
  gap: var(--spacing-sm);
}

.operations-view__copy-status {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.operations-view__modal-evidence {
  grid-template-columns: 1fr;
}

@media (max-width: 833px) {
  .operations-view__attention dl,
  .operations-view__runtime-grid,
  .operations-view__retention-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .operations-view__summary {
    grid-template-columns: 1fr;
  }

  .operations-view__summary article {
    min-height: auto;
  }
}

@media (max-width: 734px) {
  .operations-view__audit-table {
    display: none;
  }

  .operations-view__audit-cards {
    display: grid;
    gap: var(--spacing-sm);
  }

  .operations-view__retention-items li {
    grid-template-columns: 1fr;
    gap: var(--spacing-xs);
  }

  .operations-view__retention-items li span:last-child {
    text-align: start;
  }
}

@media (max-width: 640px) {
  .operations-view__header,
  .operations-view__attention > header,
  .operations-view__attention article > header,
  .operations-view__runtime > header,
  .operations-view__retention > header,
  .operations-view__panel > header,
  .operations-view__history-item > header,
  .operations-view__audit-cards article > header {
    flex-direction: column;
  }

  .operations-view__attention,
  .operations-view__runtime,
  .operations-view__retention,
  .operations-view__panel {
    padding: var(--spacing-md);
  }

  .operations-view__attention dl,
  .operations-view__runtime-grid,
  .operations-view__retention-grid {
    grid-template-columns: 1fr;
  }

  .operations-view__audit-cards dl div {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>

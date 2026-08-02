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
          {{ t('operations.eyebrow') }}
        </p>
        <h1>{{ t('operations.title') }}</h1>
        <p>{{ t('operations.description') }}</p>
      </div>
      <button
        type="button"
        :disabled="state.pending !== ''"
        @click="refreshAll"
      >
        {{ state.pending === 'overview' ? t('operations.refreshing') : t('operations.refresh') }}
      </button>
    </header>

    <InlineBanner
      v-if="state.error !== ''"
      kind="agent"
      :message="operationsErrorMessage"
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
            {{ t('operations.attention.title') }}
          </h2>
          <p>{{ t('operations.attention.description') }}</p>
        </div>
        <StatusBadge
          :tone="state.attention.length === 0 ? 'success' : 'error'"
          :label="state.attention.length === 0 ? t('operations.attention.noCases') : t('operations.attention.openCount', { count: state.attention.length })"
        />
      </header>
      <p v-if="state.phase === 'loading' && state.attention.length === 0">
        {{ t('operations.attention.loading') }}
      </p>
      <p v-else-if="state.attention.length === 0">
        {{ t('operations.attention.empty') }}
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
              :label="t('operations.attention.needsAttention')"
            />
            <h3 :id="`attention-title-${attentionCase.id}`">
              {{ t('operations.attention.consistency', { subject: subjectLabel(attentionCase.subject_type) }) }}
            </h3>
          </div>
          <time :datetime="attentionCase.opened_at">{{ t('operations.attention.opened', { time: formatTime(attentionCase.opened_at) }) }}</time>
        </header>
        <dl>
          <div><dt>{{ t('operations.attention.case') }}</dt><dd><code>{{ abbreviate(attentionCase.id) }}</code></dd></div>
          <div><dt>{{ t('operations.attention.subject') }}</dt><dd><code>{{ abbreviate(attentionCase.subject_id) }}</code></dd></div>
          <div><dt>{{ t('operations.attention.safeReason') }}</dt><dd>{{ enumLabel(attentionCase.reason_code) }}</dd></div>
          <div v-if="attentionCase.backup_id !== undefined">
            <dt>{{ t('operations.attention.recoveryPoint') }}</dt><dd><code>{{ abbreviate(attentionCase.backup_id) }}</code></dd>
          </div>
        </dl>
        <div class="operations-view__actions">
          <button
            type="button"
            data-action="verify-attention"
            :disabled="state.pending !== ''"
            @click="verifyAttention(attentionCase.id)"
          >
            {{ t('operations.attention.verify') }}
          </button>
          <button
            type="button"
            data-action="restart-for-attention"
            :disabled="!restartAvailable"
            @click="openRestart(attentionCase.id)"
          >
            {{ t('operations.attention.restart') }}
          </button>
          <button
            v-if="attentionBackup(attentionCase) !== null"
            type="button"
            data-action="restore-for-attention"
            :disabled="state.pending !== ''"
            @click="openAttentionRestore(attentionCase)"
          >
            {{ t('operations.attention.restore') }}
          </button>
        </div>
        <p v-if="attentionCase.backup_id !== undefined && attentionBackup(attentionCase) === null">
          {{ t('operations.attention.missingBackup') }}
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
            {{ t('operations.runtime.title') }}
          </h2>
          <p>{{ t('operations.runtime.description') }}</p>
        </div>
        <StatusBadge
          :tone="runtimeTone"
          :label="runtimeLabel"
        />
      </header>
      <dl class="operations-view__runtime-grid">
        <div>
          <dt>{{ t('operations.runtime.sampled') }}</dt>
          <dd>{{ state.runtime === null ? t('operations.runtime.unavailable') : formatTime(state.runtime.sampled_at) }}</dd>
        </div>
        <div>
          <dt>{{ t('operations.runtime.productionValidation') }}</dt>
          <dd>{{ validationLabel }}</dd>
        </div>
        <div>
          <dt>{{ t('operations.runtime.master') }}</dt>
          <dd>{{ state.runtime?.master === null || state.runtime === null ? t('operations.runtime.notObserved') : `PID ${state.runtime.master.pid}` }}</dd>
        </div>
        <div>
          <dt>{{ t('operations.runtime.workers') }}</dt>
          <dd>{{ state.runtime?.workers.length ?? 0 }}</dd>
        </div>
        <div>
          <dt>{{ t('operations.runtime.agent') }}</dt>
          <dd>{{ state.runtime?.components.agent === 'healthy' ? t('operations.runtime.available') : t('operations.runtime.unavailable') }}</dd>
        </div>
        <div>
          <dt>{{ t('operations.runtime.latestRestart') }}</dt>
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
          {{ t('operations.runtime.restart') }}
        </button>
        <span v-if="!restartAvailable">{{ restartUnavailableReason }}</span>
      </div>
    </section>

    <section
      class="operations-view__summary"
      :aria-label="t('operations.summary.label')"
    >
      <article>
        <strong>{{ state.backups.length }}</strong>
        <span>{{ t('operations.summary.indexed') }}</span>
      </article>
      <article>
        <strong>{{ completeBackupCount }}</strong>
        <span>{{ t('operations.summary.complete') }}</span>
      </article>
      <article>
        <strong>{{ protectedBackupCount }}</strong>
        <span>{{ t('operations.summary.protected') }}</span>
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
        <h2>{{ t('operations.overview.title') }}</h2>
        <p>{{ t('operations.overview.description') }}</p>
      </header>
      <OperationTimeline
        v-if="state.activeRestore !== null"
        :title="t('operations.overview.restoreProgress')"
        :operation-id="state.activeRestore.id"
        :state="state.activeRestore.state"
        :stage="state.activeRestore.stage"
        :stages="state.activeRestore.stages"
        :stream-state="state.stream"
      />
      <OperationTimeline
        v-else-if="state.activeRestart !== null"
        :title="t('operations.overview.restartProgress')"
        :operation-id="state.activeRestart.id"
        :state="state.activeRestart.state"
        :stage="state.activeRestart.stage"
        :stages="state.activeRestart.stages"
        :stream-state="state.stream"
      />
      <p v-else>
        {{ t('operations.overview.empty') }}
      </p>
      <section
        v-if="state.verification !== null"
        class="operations-view__verification"
        :data-state="state.verification.state"
        aria-labelledby="verification-title"
      >
        <h3 id="verification-title">
          {{ t('operations.overview.verificationTitle') }}
        </h3>
        <p>{{ state.verification.state === 'succeeded' ? t('operations.overview.verificationSucceeded') : t('operations.overview.verificationFailed') }}</p>
        <p>{{ t('operations.overview.verificationId') }} <code>{{ state.verification.id }}</code></p>
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
              {{ t('operations.retention.title') }}
            </h2>
            <p>{{ t('operations.retention.description') }}</p>
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
            {{ state.retention === null ? t('operations.retention.create') : t('operations.retention.createFresh') }}
          </button>
          <button
            v-if="state.retention?.state === 'planned'"
            type="button"
            data-action="execute-retention"
            :disabled="state.pending !== ''"
            @click="openRetentionExecution"
          >
            {{ t('operations.retention.execute') }}
          </button>
        </div>
        <template v-if="state.retention !== null">
          <dl class="operations-view__retention-grid">
            <div><dt>{{ t('operations.retention.runId') }}</dt><dd><code>{{ state.retention.id }}</code></dd></div>
            <div><dt>{{ t('operations.retention.expires') }}</dt><dd>{{ formatTime(state.retention.expires_at) }}</dd></div>
            <div><dt>{{ t('operations.retention.minimumComplete') }}</dt><dd>{{ n(state.retention.policy.minimum_complete, 'decimal') }}</dd></div>
            <div><dt>{{ t('operations.retention.maximumComplete') }}</dt><dd>{{ n(state.retention.policy.maximum_complete, 'decimal') }}</dd></div>
            <div><dt>{{ t('operations.retention.maximumBytes') }}</dt><dd>{{ formatBytes(state.retention.policy.maximum_total_bytes) }}</dd></div>
            <div><dt>{{ t('operations.retention.minimumAge') }}</dt><dd>{{ formatDuration(state.retention.policy.minimum_age_seconds) }}</dd></div>
            <div><dt>{{ t('operations.retention.protected') }}</dt><dd>{{ n(state.retention.protected_count, 'decimal') }}</dd></div>
            <div><dt>{{ t('operations.retention.plannedDeletion') }}</dt><dd>{{ n(state.retention.delete_count, 'decimal') }} / {{ formatBytes(state.retention.delete_bytes) }}</dd></div>
          </dl>
          <p class="operations-view__dry-run">
            {{ state.retention.state === 'planned' ? t('operations.retention.dryRun') : t('operations.retention.persisted', { result: enumLabel(state.retention.state) }) }}
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
          {{ t('operations.retention.noPlan') }}
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
        <h2>{{ t('operations.history.title') }}</h2>
        <p>{{ t('operations.history.description') }}</p>
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
          {{ t('operations.history.empty', { group: group.label.toLocaleLowerCase(locale) }) }}
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
            <summary>{{ t('operations.history.reviewEvidence') }}</summary>
            <OperationTimeline
              :title="t('operations.history.evidence', { kind: group.singular })"
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
        {{ state.pending === 'history' ? t('common.loading') : t('operations.history.loadMore') }}
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
        <h2>{{ t('operations.audit.title') }}</h2>
        <p>{{ t('operations.audit.description') }}</p>
      </header>
      <p v-if="state.pending === 'audit' && state.audit.length === 0">
        {{ t('operations.audit.loading') }}
      </p>
      <p v-else-if="state.audit.length === 0">
        {{ t('operations.audit.empty') }}
      </p>
      <div
        v-else
        class="operations-view__audit-table"
      >
        <table>
          <caption>{{ t('operations.audit.caption') }}</caption>
          <thead>
            <tr>
              <th scope="col">
                {{ t('operations.audit.time') }}
              </th>
              <th scope="col">
                {{ t('operations.audit.actor') }}
              </th>
              <th scope="col">
                {{ t('operations.audit.action') }}
              </th>
              <th scope="col">
                {{ t('operations.audit.object') }}
              </th>
              <th scope="col">
                {{ t('operations.audit.result') }}
              </th>
              <th scope="col">
                {{ t('operations.audit.request') }}
              </th>
              <th scope="col">
                {{ t('operations.audit.safeDetails') }}
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
                  :aria-label="t('operations.audit.copyRequestAria', { id: event.request_id })"
                  @click="copyRequestID(event.request_id)"
                >
                  {{ t('common.copy') }}
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
            <div><dt>{{ t('operations.audit.time') }}</dt><dd>{{ formatTime(event.occurred_at) }}</dd></div>
            <div><dt>{{ t('operations.audit.actor') }}</dt><dd>{{ event.actor_name }}</dd></div>
            <div><dt>{{ t('operations.audit.object') }}</dt><dd>{{ enumLabel(event.object_type) }} <code>{{ abbreviate(event.object_id) }}</code></dd></div>
            <div><dt>{{ t('operations.audit.request') }}</dt><dd><code>{{ event.request_id }}</code></dd></div>
            <div><dt>{{ t('operations.audit.safeDetails') }}</dt><dd>{{ safeDetails(event.details) }}</dd></div>
          </dl>
          <button
            type="button"
            @click="copyRequestID(event.request_id)"
          >
            {{ t('operations.audit.copyRequest') }}
          </button>
        </article>
      </div>
      <button
        v-if="state.auditCursor !== ''"
        type="button"
        :disabled="state.pending !== ''"
        @click="loadMoreAudit"
      >
        {{ state.pending === 'audit' ? t('common.loading') : t('operations.audit.loadMore') }}
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
          <div><dt>{{ t('operations.modal.backupId') }}</dt><dd><code>{{ selectedBackup.id }}</code></dd></div>
          <div><dt>{{ t('operations.modal.source') }}</dt><dd>{{ sourceLabel(selectedBackup) }}</dd></div>
          <div><dt>{{ t('operations.modal.verified') }}</dt><dd>{{ selectedBackup.verified_at === undefined ? t('operations.modal.notVerified') : formatTime(selectedBackup.verified_at) }}</dd></div>
          <div><dt>{{ t('operations.modal.productionIdentity') }}</dt><dd><code>{{ abbreviate(selectedBackup.production_digest) }}</code></dd></div>
          <div><dt>{{ t('operations.modal.size') }}</dt><dd>{{ formatBytes(selectedBackup.total_bytes) }}</dd></div>
        </template>
        <template v-else-if="modalKind === 'restart'">
          <div><dt>{{ t('operations.modal.operation') }}</dt><dd>{{ t('operations.modal.fixedRestart') }}</dd></div>
          <div><dt>{{ t('operations.modal.files') }}</dt><dd>{{ t('operations.modal.filesUnchanged') }}</dd></div>
          <div><dt>{{ t('operations.modal.healthProof') }}</dt><dd>{{ t('operations.modal.healthProofValue') }}</dd></div>
        </template>
        <template v-else-if="modalKind === 'retention' && state.retention !== null">
          <div><dt>{{ t('operations.retention.runId') }}</dt><dd><code>{{ state.retention.id }}</code></dd></div>
          <div><dt>{{ t('operations.modal.deletionCandidates') }}</dt><dd>{{ n(state.retention.delete_count, 'decimal') }}</dd></div>
          <div><dt>{{ t('operations.modal.plannedBytes') }}</dt><dd>{{ formatBytes(state.retention.delete_bytes) }}</dd></div>
          <div><dt>{{ t('operations.retention.expires') }}</dt><dd>{{ formatTime(state.retention.expires_at) }}</dd></div>
        </template>
      </dl>
    </OperationConfirmModal>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
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

const { d, locale, n, t } = useI18n()

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
const copyResult = ref<'' | 'copied' | 'failed'>('')
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
const copyMessage = computed(() => {
  if (copyResult.value === 'copied') return t('operations.audit.copied')
  if (copyResult.value === 'failed') return t('operations.audit.copyFailed')
  return ''
})
const operationsErrorMessage = computed(() => {
  const labels: Record<string, string> = {
    session_expired: t('operations.errors.sessionExpired'),
    overview_failed: t('operations.errors.overview'),
    backups_failed: t('operations.errors.backups'),
    history_failed: t('operations.errors.history'),
    audit_failed: t('operations.errors.audit'),
    restore_failed: t('operations.errors.restore'),
    restart_failed: t('operations.errors.restart'),
    protection_failed: t('operations.errors.protection'),
    retention_plan_failed: t('operations.errors.retentionPlan'),
    retention_execute_failed: t('operations.errors.retentionExecute'),
    verification_failed: t('operations.errors.verification'),
    progress_failed: t('operations.errors.progress'),
    retention_progress_failed: t('operations.errors.retentionProgress'),
  }
  return labels[state.error] ?? state.error
})
const runtimeTone = computed<StatusTone>(() => {
  switch (state.runtime?.components.nginx) {
    case 'running': return 'success'
    case 'degraded': return 'warning'
    case 'stopped': return 'error'
    default: return 'unknown'
  }
})
const runtimeLabel = computed(() =>
  t('operations.runtime.label', {
    state: runtimeStateLabel(state.runtime?.components.nginx ?? 'unknown'),
  }),
)
const validationLabel = computed(() => {
  const validation = state.runtime?.startup_validation
  if (validation === null || validation === undefined) return t('operations.runtime.noEvidence')
  return t(validation.valid ? 'operations.runtime.validAt' : 'operations.runtime.invalidAt', {
    time: formatTime(validation.checked_at),
  })
})
const latestRestartLabel = computed(() => {
  const latest = state.restarts[0]
  return latest === undefined
    ? t('operations.runtime.noRestart')
    : `${enumLabel(latest.state)} · ${formatTime(latest.updated_at)}`
})
const restartAvailable = computed(() =>
  state.runtime?.components.agent === 'healthy' && state.pending === '',
)
const restartUnavailableReason = computed(() => {
  if (state.runtime === null) return t('operations.runtime.evidenceUnavailable')
  if (state.runtime.components.agent !== 'healthy') return t('operations.runtime.agentUnavailable')
  return t('operations.runtime.requestPending')
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
  { kind: 'release', label: t('operations.history.releases'), singular: t('operations.history.release'), items: state.releases },
  { kind: 'restore', label: t('operations.history.restores'), singular: t('operations.history.restore'), items: state.restores },
  { kind: 'restart', label: t('operations.history.restarts'), singular: t('operations.history.restart'), items: state.restarts },
])
const modalTitle = computed(() => {
  switch (modalKind.value) {
    case 'restart': return t('operations.modal.restartTitle')
    case 'restore': return t('operations.modal.restoreTitle', { id: selectedBackup.value?.id ?? '' })
    case 'protect': return t('operations.modal.protectTitle', { id: selectedBackup.value?.id ?? '' })
    case 'unprotect': return t('operations.modal.unprotectTitle', { id: selectedBackup.value?.id ?? '' })
    case 'retention': return t('operations.modal.retentionTitle', { id: state.retention?.id ?? '' })
    default: return t('operations.modal.confirmOperation')
  }
})
const modalConsequence = computed(() => {
  switch (modalKind.value) {
    case 'restart': return t('operations.modal.restartConsequence')
    case 'restore': return t('operations.modal.restoreConsequence')
    case 'protect': return t('operations.modal.protectConsequence')
    case 'unprotect': return t('operations.modal.unprotectConsequence')
    case 'retention': return t('operations.modal.retentionConsequence')
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
    case 'restart': return t('operations.modal.restart')
    case 'restore': return t('operations.modal.restore')
    case 'protect': return t('operations.modal.protect')
    case 'unprotect': return t('operations.modal.unprotect')
    case 'retention': return t('operations.modal.retention')
    default: return t('operations.modal.confirm')
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
    copyResult.value = 'copied'
  } catch {
    copyResult.value = 'failed'
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

function runtimeStateLabel(value: 'degraded' | 'running' | 'stopped' | 'unknown'): string {
  const labels = {
    running: t('operations.runtime.states.running'),
    degraded: t('operations.runtime.states.degraded'),
    stopped: t('operations.runtime.states.stopped'),
    unknown: t('operations.runtime.states.unknown'),
  }
  return labels[value]
}

function retentionItemLabel(value: RetentionItemState): string {
  switch (value) {
    case 'skipped_protected': return t('operations.retention.skippedProtected')
    case 'needs_attention': return t('operations.retention.needsAttention')
    default: return enumLabel(value)
  }
}

function subjectLabel(value: AttentionCase['subject_type']): string {
  return enumLabel(value)
}

function actionLabel(value: string): string {
  return value.split('.').map(enumLabel).join(' · ')
}

function enumLabel(value: string): string {
  const labels: Record<string, string> = {
    unknown: t('operations.enums.unknown'),
    running: t('operations.enums.running'),
    degraded: t('operations.enums.degraded'),
    stopped: t('operations.enums.stopped'),
    healthy: t('operations.enums.healthy'),
    unavailable: t('operations.enums.unavailable'),
    valid: t('operations.enums.valid'),
    invalid: t('operations.enums.invalid'),
    queued: t('operations.enums.queued'),
    rolling_back: t('operations.enums.rollingBack'),
    failed: t('operations.enums.failed'),
    needs_attention: t('operations.enums.needsAttention'),
    succeeded: t('operations.enums.succeeded'),
    rolled_back: t('operations.enums.rolledBack'),
    cancelled: t('operations.enums.cancelled'),
    planned: t('operations.enums.planned'),
    executing: t('operations.enums.executing'),
    expired: t('operations.enums.expired'),
    kept: t('operations.enums.kept'),
    deleting: t('operations.enums.deleting'),
    deleted: t('operations.enums.deleted'),
    skipped_protected: t('operations.enums.skippedProtected'),
    open: t('operations.enums.open'),
    resolved: t('operations.enums.resolved'),
    workspace: t('operations.enums.workspace'),
    release: t('operations.enums.release'),
    restore: t('operations.enums.restore'),
    restart: t('operations.enums.restart'),
    verification: t('operations.enums.verification'),
    maximum_complete: t('operations.enums.maximumComplete'),
    minimum_complete: t('operations.enums.minimumComplete'),
    maximum_total_bytes: t('operations.enums.maximumTotalBytes'),
    minimum_age: t('operations.enums.minimumAge'),
    manual_protection: t('operations.enums.manualProtection'),
    runtime_unknown: t('operations.enums.runtimeUnknown'),
    production_changed: t('operations.enums.productionChanged'),
    success: t('operations.enums.success'),
    warning: t('operations.enums.warning'),
    pending: t('operations.enums.pending'),
    config: t('operations.enums.config'),
    backup: t('operations.enums.backup'),
    attention: t('operations.enums.attention'),
    retention: t('operations.enums.retention'),
    publish_check: t('operations.enums.publishCheck'),
    structured: t('operations.enums.structured'),
    file: t('operations.enums.file'),
    groups: t('operations.enums.groups'),
    create: t('operations.enums.create'),
    update: t('operations.enums.update'),
    delete: t('operations.enums.delete'),
    protect: t('operations.enums.protect'),
    unprotect: t('operations.enums.unprotect'),
    start: t('operations.enums.start'),
    stage: t('operations.enums.stage'),
    result: t('operations.enums.result'),
    plan: t('operations.enums.plan'),
    verify: t('operations.enums.verify'),
    resolve: t('operations.enums.resolve'),
  }
  const known = labels[value]
  if (known !== undefined) return known
  const words = value.replaceAll('_', ' ')
  return locale.value === 'en-US'
    ? words.charAt(0).toUpperCase() + words.slice(1)
    : value
}

function abbreviate(value: string): string {
  return value.length <= 16 ? value : `${value.slice(0, 8)}…${value.slice(-4)}`
}

function sourceLabel(backup: ConfigBackup): string {
  const key = backup.origin_type === 'release' ? 'backups.releaseSource' : 'backups.restoreSource'
  return t(key, { id: abbreviate(backup.origin_id) })
}

function formatTime(value: string): string {
  return d(new Date(value), 'short')
}

function formatBytes(value: number): string {
  if (value < 1024) return `${n(value, 'decimal')} B`
  if (value < 1024 * 1024) return `${n(value / 1024, 'decimal')} KiB`
  return `${n(value / (1024 * 1024), 'decimal')} MiB`
}

function formatDuration(seconds: number): string {
  if (seconds % 86400 === 0) return t('operations.durationDays', { count: n(seconds / 86400, 'decimal') })
  if (seconds % 3600 === 0) return t('operations.durationHours', { count: n(seconds / 3600, 'decimal') })
  return t('operations.durationSeconds', { count: n(seconds, 'decimal') })
}

function safeDetails(details: Readonly<Record<string, string | number | boolean>>): string {
  const entries = Object.entries(details)
  if (entries.length === 0) return t('operations.audit.noDetails')
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

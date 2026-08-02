<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.3
-->
<template>
  <section
    class="operation-timeline"
    :aria-labelledby="titleId"
  >
    <header>
      <div>
        <h3 :id="titleId">
          {{ title }}
        </h3>
        <p>{{ t('operations.timeline.operationId') }} <code>{{ operationId }}</code></p>
      </div>
      <span data-stream-state>{{ streamLabel }}</span>
    </header>
    <p
      class="operation-timeline__current"
      aria-live="polite"
      aria-atomic="true"
    >
      {{ t('operations.timeline.currentStage', { stage: stageLabel(stage) }) }}
    </p>
    <ol>
      <li
        v-for="item in stages"
        :key="item.sequence"
        :data-result="item.result"
      >
        <span
          class="operation-timeline__marker"
          aria-hidden="true"
        >{{ resultIcon(item.result) }}</span>
        <div>
          <strong>{{ stageLabel(item.stage) }}</strong>
          <span>{{ resultLabel(item.result) }}</span>
        </div>
        <time :datetime="item.occurred_at">{{ formatTime(item.occurred_at) }}</time>
      </li>
    </ol>
    <div
      v-if="terminal"
      class="operation-timeline__terminal"
      :data-state="state"
      :role="state === 'needs_attention' ? 'alert' : 'status'"
    >
      <strong>{{ terminalTitle }}</strong>
      <p>{{ terminalMessage }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ReleaseStageResult } from '../api/types'

const { d, t } = useI18n()

export interface OperationStageEvidence {
  sequence: number
  stage: string
  result: ReleaseStageResult
  code?: string
  occurred_at: string
}

const props = withDefaults(defineProps<{
  operationId: string
  stage: string
  stages: OperationStageEvidence[]
  state: string
  streamState?: 'closed' | 'connecting' | 'live' | 'reconnecting'
  title: string
}>(), {
  streamState: 'closed',
})

const titleId = useId()
const terminalStates = new Set([
  'succeeded', 'failed', 'rolled_back', 'needs_attention', 'cancelled', 'expired',
])
const terminal = computed(() => terminalStates.has(props.state))
const streamLabel = computed(() => {
  switch (props.streamState) {
    case 'live': return t('release.stream.live')
    case 'connecting': return t('release.stream.connecting')
    case 'reconnecting': return t('release.stream.reconnecting')
    default: return t('release.stream.persisted')
  }
})
const terminalTitle = computed(() => {
  switch (props.state) {
    case 'succeeded': return t('operations.timeline.operationSucceeded')
    case 'rolled_back': return t('operations.timeline.backupRestored')
    case 'needs_attention': return t('operations.timeline.attentionRequired')
    case 'cancelled': return t('operations.timeline.cancelled')
    case 'expired': return t('operations.timeline.planExpired')
    default: return t('operations.timeline.operationFailed')
  }
})
const terminalMessage = computed(() => {
  switch (props.state) {
    case 'succeeded': return t('operations.timeline.succeededMessage')
    case 'rolled_back': return t('operations.timeline.rolledBackMessage')
    case 'needs_attention': return t('operations.timeline.needsAttentionMessage')
    case 'cancelled': return t('operations.timeline.cancelledMessage')
    case 'expired': return t('operations.timeline.expiredMessage')
    default: return t('operations.timeline.failedMessage')
  }
})

function stageLabel(value: string): string {
  const labels: Record<string, string> = {
    queued: t('release.stages.queued'),
    rechecking: t('release.stages.rechecking'),
    backup_creating: t('release.stages.backupCreating'),
    backup_verified: t('release.stages.backupVerified'),
    candidate_validated: t('release.stages.candidateValidated'),
    files_applying: t('release.stages.filesApplying'),
    files_applied: t('release.stages.filesApplied'),
    production_validated: t('release.stages.productionValidated'),
    reload_requested: t('release.stages.reloadRequested'),
    runtime_confirmed: t('release.stages.runtimeConfirmed'),
    committed: t('release.stages.committed'),
    rollback_applying: t('release.stages.rollbackApplying'),
    rollback_files_restored: t('release.stages.rollbackFilesRestored'),
    rollback_validated: t('release.stages.rollbackValidated'),
    rollback_reload_requested: t('release.stages.rollbackReloadRequested'),
    rolled_back: t('release.stages.rolledBack'),
    failed: t('release.stages.failed'),
    needs_attention: t('release.stages.needsAttention'),
    target_verifying: t('operations.timeline.stages.targetVerifying'),
    target_validated: t('operations.timeline.stages.targetValidated'),
    safety_backup_creating: t('operations.timeline.stages.safetyBackupCreating'),
    safety_backup_verified: t('operations.timeline.stages.safetyBackupVerified'),
    files_restoring: t('operations.timeline.stages.filesRestoring'),
    files_restored: t('operations.timeline.stages.filesRestored'),
    production_validating: t('operations.timeline.stages.productionValidating'),
    runtime_sampling: t('operations.timeline.stages.runtimeSampling'),
    restart_requested: t('operations.timeline.stages.restartRequested'),
    runtime_confirming: t('operations.timeline.stages.runtimeConfirming'),
    succeeded: t('operations.timeline.stages.succeeded'),
  }
  return labels[value] ?? value
}

function resultLabel(value: ReleaseStageResult): string {
  const labels: Record<ReleaseStageResult, string> = {
    pending: t('release.results.pending'),
    running: t('release.results.running'),
    success: t('release.results.success'),
    failed: t('release.results.failed'),
    warning: t('release.results.warning'),
  }
  return labels[value]
}

function resultIcon(value: ReleaseStageResult): string {
  switch (value) {
    case 'success': return '✓'
    case 'failed': return '×'
    case 'warning': return '!'
    case 'running': return '◌'
    default: return '·'
  }
}

function formatTime(value: string): string {
  return d(new Date(value), 'short')
}
</script>

<style scoped>
.operation-timeline {
  display: grid;
  width: var(--component-operations-detail-width);
  max-width: 100%;
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  gap: var(--spacing-md);
}

.operation-timeline header,
.operation-timeline li,
.operation-timeline li div {
  display: flex;
  min-width: 0;
}

.operation-timeline header {
  align-items: start;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.operation-timeline h3,
.operation-timeline p,
.operation-timeline ol {
  margin: 0;
}

.operation-timeline code {
  overflow-wrap: anywhere;
}

.operation-timeline__current {
  font-weight: var(--font-weight-semibold);
}

.operation-timeline ol {
  display: grid;
  padding: 0;
  list-style: none;
}

.operation-timeline li {
  position: relative;
  display: grid;
  grid-template-columns: var(--component-release-timeline-marker) minmax(0, 1fr) auto;
  align-items: start;
  padding-block: var(--spacing-xs);
  gap: var(--spacing-sm);
}

.operation-timeline li:not(:last-child)::after {
  position: absolute;
  width: 1px;
  background: var(--color-hairline);
  content: '';
  inset-block: calc(var(--spacing-xs) + var(--component-release-timeline-marker)) calc(-1 * var(--spacing-xs));
  inset-inline-start: calc(var(--component-release-timeline-marker) / 2);
}

.operation-timeline__marker {
  display: inline-grid;
  width: var(--component-release-timeline-marker);
  height: var(--component-release-timeline-marker);
  border: 1px solid currentcolor;
  border-radius: var(--rounded-pill);
  place-items: center;
  font-size: var(--font-size-nav);
}

.operation-timeline li div {
  flex-direction: column;
  overflow-wrap: anywhere;
}

.operation-timeline time,
.operation-timeline li div span,
.operation-timeline [data-stream-state] {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.operation-timeline__terminal {
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.operation-timeline__terminal p {
  margin-block-start: var(--spacing-xs);
}

.operation-timeline__terminal[data-state='succeeded'] {
  border-color: var(--color-state-success-foreground);
  background: var(--color-state-success);
  color: var(--color-state-success-foreground);
}

.operation-timeline__terminal[data-state='rolled_back'],
.operation-timeline__terminal[data-state='expired'] {
  border-color: var(--color-state-warning-foreground);
  background: var(--color-state-warning);
  color: var(--color-state-warning-foreground);
}

.operation-timeline__terminal[data-state='failed'],
.operation-timeline__terminal[data-state='needs_attention'] {
  border-color: var(--color-state-danger-foreground);
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}

@media (max-width: 640px) {
  .operation-timeline header,
  .operation-timeline li {
    grid-template-columns: var(--component-release-timeline-marker) minmax(0, 1fr);
  }

  .operation-timeline header {
    display: grid;
  }

  .operation-timeline time {
    grid-column: 2;
  }
}
</style>

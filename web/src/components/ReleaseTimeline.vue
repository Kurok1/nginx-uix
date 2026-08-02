<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.2
-->
<template>
  <section
    class="release-timeline"
    aria-labelledby="release-timeline-title"
  >
    <header>
      <div>
        <h2 id="release-timeline-title">
          {{ t('release.title') }}
        </h2>
        <p>{{ t('release.releaseId') }} <code>{{ release.id }}</code></p>
        <p v-if="release.backup_id !== undefined">
          {{ t('release.backupId') }} <code>{{ release.backup_id }}</code>
        </p>
      </div>
      <span data-stream-state>{{ streamLabel }}</span>
    </header>
    <p
      aria-live="polite"
      aria-atomic="true"
      class="release-timeline__current"
    >
      {{ currentStage }}
    </p>
    <ol>
      <li
        v-for="stage in release.stages"
        :key="stage.sequence"
        :data-result="stage.result"
      >
        <span
          class="release-timeline__marker"
          aria-hidden="true"
        >{{ resultIcon(stage.result) }}</span>
        <div>
          <strong>{{ stageLabel(stage.stage) }}</strong>
          <span>{{ resultLabel(stage.result) }}</span>
        </div>
        <time :datetime="stage.occurred_at">{{ formatTime(stage.occurred_at) }}</time>
      </li>
    </ol>
    <div
      v-if="terminalMessage !== ''"
      data-terminal
      :role="release.state === 'needs_attention' ? 'alert' : 'status'"
      :data-state="release.state"
    >
      <strong>{{ terminalTitle }}</strong>
      <p>{{ terminalMessage }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Release, ReleaseStage } from '../api/types'

const { d, t } = useI18n()

const props = defineProps<{
  release: Release
  streamState: 'closed' | 'connecting' | 'live' | 'reconnecting'
}>()

const currentStage = computed(() => t('release.currentStage', { stage: stageLabel(props.release.stage) }))
const streamLabel = computed(() => {
  switch (props.streamState) {
    case 'live': return t('release.stream.live')
    case 'connecting': return t('release.stream.connecting')
    case 'reconnecting': return t('release.stream.reconnecting')
    default: return t('release.stream.persisted')
  }
})
const terminalTitle = computed(() => {
  switch (props.release.state) {
    case 'succeeded': return t('release.terminalTitle.succeeded')
    case 'rolled_back': return t('release.terminalTitle.rolledBack')
    case 'needs_attention': return t('release.terminalTitle.needsAttention')
    case 'cancelled': return t('release.terminalTitle.cancelled')
    default: return t('release.terminalTitle.failed')
  }
})
const terminalMessage = computed(() => {
  switch (props.release.state) {
    case 'succeeded': return t('release.terminalMessage.succeeded')
    case 'failed': return t('release.terminalMessage.failed')
    case 'rolled_back': return t('release.terminalMessage.rolledBack')
    case 'needs_attention': return t('release.terminalMessage.needsAttention')
    case 'cancelled': return t('release.terminalMessage.cancelled')
    default: return ''
  }
})

function stageLabel(stage: Release['stage']): string {
  const labels: Record<Release['stage'], string> = {
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
  }
  return labels[stage]
}

function resultLabel(result: ReleaseStage['result']): string {
  const labels: Record<ReleaseStage['result'], string> = {
    pending: t('release.results.pending'),
    running: t('release.results.running'),
    success: t('release.results.success'),
    failed: t('release.results.failed'),
    warning: t('release.results.warning'),
  }
  return labels[result]
}

function resultIcon(result: ReleaseStage['result']): string {
  switch (result) {
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
.release-timeline {
  display: grid;
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  gap: var(--spacing-md);
}

.release-timeline header,
.release-timeline li,
.release-timeline li div {
  display: flex;
  min-width: 0;
}

.release-timeline header {
  align-items: start;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.release-timeline h2,
.release-timeline p,
.release-timeline ol {
  margin: 0;
}

.release-timeline code {
  overflow-wrap: anywhere;
}

.release-timeline__current {
  font-weight: var(--font-weight-semibold);
}

.release-timeline ol {
  display: grid;
  padding: 0;
  list-style: none;
}

.release-timeline li {
  position: relative;
  display: grid;
  grid-template-columns: var(--component-release-timeline-marker) minmax(0, 1fr) auto;
  align-items: start;
  padding-block: var(--spacing-xs);
  gap: var(--spacing-sm);
}

.release-timeline li:not(:last-child)::after {
  position: absolute;
  width: 1px;
  background: var(--color-hairline);
  content: '';
  inset-block: calc(var(--spacing-xs) + var(--component-release-timeline-marker)) calc(-1 * var(--spacing-xs));
  inset-inline-start: calc(var(--component-release-timeline-marker) / 2);
}

.release-timeline__marker {
  display: inline-grid;
  width: var(--component-release-timeline-marker);
  height: var(--component-release-timeline-marker);
  border: 1px solid currentcolor;
  border-radius: var(--rounded-pill);
  place-items: center;
  font-size: var(--font-size-nav);
  line-height: 1;
}

.release-timeline li div {
  flex-direction: column;
  overflow-wrap: anywhere;
}

.release-timeline time,
.release-timeline li div span,
.release-timeline [data-stream-state] {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.release-timeline [data-terminal] {
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.release-timeline [data-state='succeeded'] {
  border-color: var(--color-state-success-foreground);
  background: var(--color-state-success);
  color: var(--color-state-success-foreground);
}

.release-timeline [data-state='rolled_back'] {
  border-color: var(--color-state-warning-foreground);
  background: var(--color-state-warning);
  color: var(--color-state-warning-foreground);
}

.release-timeline [data-state='failed'],
.release-timeline [data-state='needs_attention'] {
  border-color: var(--color-state-danger-foreground);
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}

@media (max-width: 480px) {
  .release-timeline header {
    flex-direction: column;
  }

  .release-timeline li {
    grid-template-columns: var(--component-release-timeline-marker) minmax(0, 1fr);
  }

  .release-timeline time {
    grid-column: 2;
  }
}
</style>

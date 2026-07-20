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
        <p>Operation ID: <code>{{ operationId }}</code></p>
      </div>
      <span data-stream-state>{{ streamLabel }}</span>
    </header>
    <p
      class="operation-timeline__current"
      aria-live="polite"
      aria-atomic="true"
    >
      Current stage: {{ stageLabel(stage) }}
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

import type { ReleaseStageResult } from '../api/types'

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
    case 'live': return 'Live progress connected'
    case 'connecting': return 'Connecting to progress…'
    case 'reconnecting': return 'Reconnecting to progress…'
    default: return 'Persisted progress'
  }
})
const terminalTitle = computed(() => {
  switch (props.state) {
    case 'succeeded': return 'Operation succeeded'
    case 'rolled_back': return 'Safety backup restored'
    case 'needs_attention': return 'Administrator attention required'
    case 'cancelled': return 'Operation cancelled'
    case 'expired': return 'Plan expired'
    default: return 'Operation failed'
  }
})
const terminalMessage = computed(() => {
  switch (props.state) {
    case 'succeeded': return 'The persisted operation evidence confirms a healthy terminal result.'
    case 'rolled_back': return 'The requested change failed; the safety backup was restored and confirmed healthy.'
    case 'needs_attention': return 'Production or runtime state cannot be uniquely confirmed. Ordinary production changes remain blocked.'
    case 'cancelled': return 'No successful terminal result was recorded.'
    case 'expired': return 'Create a fresh dry-run before executing retention.'
    default: return 'The operation ended without a successful result.'
  }
})

function stageLabel(value: string): string {
  const words = value.replaceAll('_', ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

function resultLabel(value: ReleaseStageResult): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
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
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(new Date(value))
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

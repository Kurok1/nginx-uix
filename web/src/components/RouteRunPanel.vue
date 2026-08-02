<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.4.0
-->
<template>
  <section
    class="route-run"
    aria-labelledby="route-run-title"
  >
    <header>
      <div>
        <p class="route-run__eyebrow">
          {{ t('routeLab.runEyebrow') }}
        </p>
        <h2 id="route-run-title">
          {{ t('routeLab.run.title') }}
        </h2>
      </div>
      <StatusBadge
        :tone="runTone"
        :label="run === null ? t('routeLab.run.notRun') : runStateLabel(run.state)"
      />
    </header>

    <p
      v-if="run === null"
      class="route-run__empty"
    >
      {{ t('routeLab.run.empty') }}
    </p>

    <template v-else>
      <div class="route-run__identity">
        <p>{{ t('routeLab.run.runId') }} <code>{{ run.id }}</code></p>
        <span data-stream-state>{{ streamLabel }}</span>
      </div>

      <div
        v-if="!terminal"
        class="route-run__control"
      >
        <p
          aria-live="polite"
          aria-atomic="true"
        >
          {{ run.cancel_requested_at === undefined ? t('routeLab.run.currentStage', { stage: stageLabel(run.stage) }) : t('routeLab.run.cancelling') }}
        </p>
        <button
          type="button"
          data-action="cancel-route-test"
          :disabled="run.cancel_requested_at !== undefined"
          :aria-describedby="run.cancel_requested_at === undefined ? undefined : 'route-cancel-reason'"
          @click="$emit('cancel')"
        >
          {{ run.cancel_requested_at === undefined ? t('routeLab.run.cancel') : t('routeLab.run.cancellationRequested') }}
        </button>
        <p
          v-if="run.cancel_requested_at !== undefined"
          id="route-cancel-reason"
        >
          {{ t('routeLab.run.cancellationRecorded') }}
        </p>
      </div>

      <section
        class="route-run__timeline"
        aria-labelledby="route-run-timeline-title"
      >
        <h3 id="route-run-timeline-title">
          {{ t('routeLab.run.persistedStages') }}
        </h3>
        <p v-if="run.stages.length === 0">
          {{ t('routeLab.run.noStages') }}
        </p>
        <ol v-else>
          <li
            v-for="stage in run.stages"
            :key="stage.sequence"
            :data-result="stage.result"
          >
            <span
              class="route-run__marker"
              aria-hidden="true"
            >{{ resultIcon(stage.result) }}</span>
            <div>
              <strong>{{ stageLabel(stage.stage) }}</strong>
              <span>{{ resultLabel(stage.result) }}<template v-if="stage.code !== undefined"> · {{ stage.code }}</template></span>
            </div>
            <time :datetime="stage.occurred_at">{{ formatTime(stage.occurred_at) }}</time>
          </li>
        </ol>
      </section>

      <div
        v-if="terminal && run.terminal_result === undefined"
        class="route-run__terminal-error"
        :role="run.last_error_code === 'route_cleanup_failed' ? 'alert' : 'status'"
      >
        <strong>{{ terminalTitle }}</strong>
        <p>{{ terminalMessage }}</p>
        <p v-if="run.last_error_code !== undefined">
          {{ t('routeLab.run.safeErrorCode') }} <code>{{ run.last_error_code }}</code>
        </p>
      </div>

      <template v-if="result !== null">
        <section
          class="route-run__comparison"
          aria-labelledby="route-comparison-title"
        >
          <h3 id="route-comparison-title">
            {{ t('routeLab.run.comparison') }}
          </h3>
          <div>
            <article>
              <span>{{ t('routeLab.run.predicted') }}</span>
              <strong>{{ t('routeLab.run.server') }}</strong>
              <code>{{ run.static_analysis.predicted_server_route_id ?? t('routeLab.run.indeterminate') }}</code>
              <strong>{{ t('routeLab.run.location') }}</strong>
              <code>{{ run.static_analysis.predicted_location_route_id ?? t('routeLab.run.serverContext') }}</code>
            </article>
            <article>
              <span>{{ t('routeLab.run.observed') }}</span>
              <strong>{{ t('routeLab.run.server') }}</strong>
              <code>{{ result.evidence.server_route_id }}</code>
              <strong>{{ t('routeLab.run.location') }}</strong>
              <code>{{ result.evidence.route_id }}</code>
            </article>
          </div>
          <p
            v-if="routeMismatch"
            class="route-run__mismatch"
          >
            <span aria-hidden="true">△</span> {{ t('routeLab.run.mismatch') }}
          </p>
        </section>

        <section
          class="route-run__result"
          aria-labelledby="route-http-result-title"
        >
          <h3 id="route-http-result-title">
            {{ t('routeLab.run.httpResult') }}
          </h3>
          <dl>
            <div><dt>{{ t('routeLab.run.status') }}</dt><dd>{{ result.response.status_code }}</dd></div>
            <div><dt>{{ t('routeLab.run.finalUri') }}</dt><dd><code>{{ result.evidence.final_uri }}</code></dd></div>
            <div><dt>{{ t('routeLab.run.upstream') }}</dt><dd><code>{{ result.evidence.upstream || t('routeLab.run.noneObserved') }}</code></dd></div>
            <div><dt>{{ t('routeLab.run.upstreamStatus') }}</dt><dd>{{ result.evidence.upstream_status || t('routeLab.run.noneObserved') }}</dd></div>
            <div><dt>{{ t('routeLab.run.requestTime') }}</dt><dd>{{ t('routeLab.run.milliseconds', { value: result.evidence.request_time_ms }) }}</dd></div>
            <div><dt>{{ t('routeLab.run.totalTime') }}</dt><dd>{{ t('routeLab.run.milliseconds', { value: result.response.duration_ms }) }}</dd></div>
            <div><dt>{{ t('routeLab.run.responseBytes') }}</dt><dd>{{ result.response.body_bytes }}</dd></div>
            <div><dt>{{ t('routeLab.run.assertions') }}</dt><dd>{{ assertionSummary }}</dd></div>
          </dl>

          <div v-if="result.response.headers.length > 0">
            <h4>{{ t('routeLab.run.safeHeaders') }}</h4>
            <dl class="route-run__headers">
              <div
                v-for="header in result.response.headers"
                :key="header.name"
              >
                <dt>{{ header.name }}</dt><dd>{{ header.value }}</dd>
              </div>
            </dl>
          </div>

          <div v-if="result.response.assertions.results.length > 0">
            <h4>{{ t('routeLab.run.assertionOutcomes') }}</h4>
            <ul class="route-run__assertions">
              <li
                v-for="assertion in result.response.assertions.results"
                :key="assertion.kind"
              >
                <span aria-hidden="true">{{ assertion.passed && assertion.complete ? '✓' : '×' }}</span>
                {{ assertionLabel(assertion.kind) }} —
                {{ !assertion.complete ? t('routeLab.run.assertionIncomplete') : assertion.passed ? t('routeLab.run.passed') : t('routeLab.run.failed') }}
              </li>
            </ul>
          </div>

          <div v-if="!result.response.snippet_omitted">
            <h4>{{ t('routeLab.run.snippet') }}</h4>
            <pre
              tabindex="0"
              :aria-label="t('routeLab.run.snippetLabel')"
            ><code>{{ result.response.body_snippet }}</code></pre>
            <p v-if="result.response.body_truncated">
              {{ t('routeLab.run.snippetTruncated') }}
            </p>
          </div>
          <p v-else>
            {{ t('routeLab.run.snippetOmitted') }}
          </p>
        </section>

        <section
          class="route-run__cleanup"
          :class="{ 'route-run__cleanup--failed': !cleanupConfirmed }"
          :role="cleanupConfirmed ? 'status' : 'alert'"
          aria-labelledby="route-cleanup-title"
        >
          <h3 id="route-cleanup-title">
            {{ cleanupConfirmed ? t('routeLab.run.cleanupConfirmed') : t('routeLab.run.cleanupUnconfirmed') }}
          </h3>
          <ul>
            <li>{{ result.cleanup.master_reaped ? '✓' : '×' }} {{ t('routeLab.run.masterReaped') }}</li>
            <li>{{ result.cleanup.port_closed ? '✓' : '×' }} {{ t('routeLab.run.portClosed') }}</li>
            <li>{{ result.cleanup.stage_removed ? '✓' : '×' }} {{ t('routeLab.run.stageRemoved') }}</li>
          </ul>
        </section>

        <section
          v-if="result.diagnostics.length > 0"
          class="route-run__diagnostics"
          aria-labelledby="route-diagnostics-title"
        >
          <h3 id="route-diagnostics-title">
            {{ t('routeLab.run.diagnostics') }}
          </h3>
          <ul>
            <li
              v-for="diagnostic in result.diagnostics"
              :key="`${diagnostic.code}:${diagnostic.path}:${diagnostic.line}`"
            >
              <code>{{ diagnostic.code }}</code> — {{ diagnostic.summary }}<template v-if="diagnostic.path !== ''">
                ({{ diagnostic.path }}:{{ diagnostic.line }})
              </template>
            </li>
          </ul>
        </section>
      </template>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  isTerminalRouteRun,
  type RouteAssertionKind,
  type RouteRunState,
  type RouteRunStageName,
  type RouteStageResult,
  type RouteTestRun,
} from '../api/route_lab'
import StatusBadge, { type StatusTone } from './StatusBadge.vue'

const { d, t } = useI18n()

const props = withDefaults(defineProps<{
  run: RouteTestRun | null
  streamState?: 'closed' | 'connecting' | 'live' | 'reconnecting'
}>(), {
  streamState: 'closed',
})
defineEmits<{ cancel: [] }>()

const terminal = computed(() => isTerminalRouteRun(props.run))
const result = computed(() => props.run?.terminal_result?.agent_result ?? null)
const cleanupConfirmed = computed(() => {
  const cleanup = result.value?.cleanup
  return cleanup !== undefined && cleanup.master_reaped && cleanup.port_closed && cleanup.stage_removed
})
const routeMismatch = computed(() => {
  if (props.run === null || result.value === null) return false
  return (
    props.run.static_analysis.predicted_server_route_id !== result.value.evidence.server_route_id ||
    (props.run.static_analysis.predicted_location_route_id ?? result.value.evidence.server_route_id) !==
      result.value.evidence.route_id
  )
})
const runTone = computed<StatusTone>(() => {
  switch (props.run?.state) {
    case 'succeeded': return result.value?.response.assertions.passed === false ? 'warning' : 'success'
    case 'failed':
    case 'timed_out': return 'error'
    case 'cancelled': return 'warning'
    default: return 'unknown'
  }
})
const streamLabel = computed(() => {
  switch (props.streamState) {
    case 'live': return t('routeLab.run.stream.live')
    case 'connecting': return t('routeLab.run.stream.connecting')
    case 'reconnecting': return t('routeLab.run.stream.reconnecting')
    default: return t('routeLab.run.stream.persisted')
  }
})
const assertionSummary = computed(() => {
  const assertions = result.value?.response.assertions
  if (assertions === undefined || assertions.results.length === 0) return t('routeLab.run.assertionSummary.none')
  if (!assertions.complete) return t('routeLab.run.assertionSummary.indeterminate')
  return assertions.passed
    ? t('routeLab.run.assertionSummary.passed')
    : t('routeLab.run.assertionSummary.failed')
})
const terminalTitle = computed(() => {
  switch (props.run?.state) {
    case 'cancelled': return t('routeLab.run.terminalTitle.cancelled')
    case 'timed_out': return t('routeLab.run.terminalTitle.timedOut')
    default: return t('routeLab.run.terminalTitle.failed')
  }
})
const terminalMessage = computed(() => {
  switch (props.run?.state) {
    case 'cancelled': return t('routeLab.run.terminalMessage.cancelled')
    case 'timed_out': return t('routeLab.run.terminalMessage.timedOut')
    default: return t('routeLab.run.terminalMessage.failed')
  }
})

function runStateLabel(state: RouteRunState): string {
  const labels: Record<RouteRunState, string> = {
    queued: t('routeLab.run.states.queued'),
    running: t('routeLab.run.states.running'),
    succeeded: t('routeLab.run.states.succeeded'),
    failed: t('routeLab.run.states.failed'),
    cancelled: t('routeLab.run.states.cancelled'),
    timed_out: t('routeLab.run.states.timedOut'),
  }
  return labels[state]
}

function stageLabel(value: RouteRunStageName): string {
  const labels: Record<RouteRunStageName, string> = {
    queued: t('routeLab.run.stages.queued'),
    preparing: t('routeLab.run.stages.preparing'),
    validating: t('routeLab.run.stages.validating'),
    starting: t('routeLab.run.stages.starting'),
    requesting: t('routeLab.run.stages.requesting'),
    collecting: t('routeLab.run.stages.collecting'),
    completed: t('routeLab.run.stages.completed'),
    failed: t('routeLab.run.stages.failed'),
    cancelled: t('routeLab.run.stages.cancelled'),
    timed_out: t('routeLab.run.stages.timedOut'),
  }
  return labels[value]
}

function resultLabel(value: RouteStageResult): string {
  const labels: Record<RouteStageResult, string> = {
    pending: t('routeLab.run.results.pending'),
    running: t('routeLab.run.results.running'),
    success: t('routeLab.run.results.success'),
    failed: t('routeLab.run.results.failed'),
    warning: t('routeLab.run.results.warning'),
  }
  return labels[value]
}

function resultIcon(value: RouteStageResult): string {
  switch (value) {
    case 'success': return '✓'
    case 'failed': return '×'
    case 'warning': return '!'
    case 'running': return '◌'
    case 'pending': return '·'
  }
}

function assertionLabel(kind: RouteAssertionKind): string {
  const labels: Record<RouteAssertionKind, string> = {
    status_code: t('routeLab.run.assertionLabels.statusCode'),
    contains_text: t('routeLab.run.assertionLabels.containsText'),
    forbidden_text: t('routeLab.run.assertionLabels.forbiddenText'),
  }
  return labels[kind]
}

function formatTime(value: string): string {
  return d(new Date(value), 'short')
}
</script>

<style scoped>
.route-run,
.route-run section,
.route-run__result dl,
.route-run__headers {
  display: grid;
  min-width: 0;
  align-content: start;
  gap: var(--spacing-md);
}

.route-run {
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.route-run header,
.route-run__identity,
.route-run__control,
.route-run__comparison > div {
  display: flex;
  min-width: 0;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.route-run h2,
.route-run h3,
.route-run h4,
.route-run p,
.route-run ol,
.route-run ul,
.route-run dl {
  margin: 0;
}

.route-run__eyebrow,
.route-run__identity > span,
.route-run__timeline time,
.route-run__timeline li div span {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.route-run__empty,
.route-run__control,
.route-run__terminal-error {
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.route-run__control {
  align-items: center;
  flex-wrap: wrap;
}

.route-run button {
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.route-run button:disabled {
  border-color: var(--color-ink-muted-48);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.route-run__timeline ol {
  display: grid;
  padding: 0;
  list-style: none;
}

.route-run__timeline li {
  position: relative;
  display: grid;
  min-width: 0;
  grid-template-columns: var(--component-release-timeline-marker) minmax(0, 1fr) auto;
  padding-block: var(--spacing-xs);
  gap: var(--spacing-sm);
}

.route-run__timeline li:not(:last-child)::after {
  position: absolute;
  width: 1px;
  background: var(--color-hairline);
  content: '';
  inset-block: calc(var(--spacing-xs) + var(--component-release-timeline-marker)) calc(-1 * var(--spacing-xs));
  inset-inline-start: calc(var(--component-release-timeline-marker) / 2);
}

.route-run__marker {
  display: inline-grid;
  width: var(--component-release-timeline-marker);
  height: var(--component-release-timeline-marker);
  border: 1px solid currentcolor;
  border-radius: var(--rounded-pill);
  place-items: center;
}

.route-run__timeline li div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  overflow-wrap: anywhere;
}

.route-run__comparison > div {
  align-items: stretch;
}

.route-run__comparison article {
  display: grid;
  min-width: 0;
  flex: 1 1 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  gap: var(--spacing-xxs);
}

.route-run__comparison article > span,
.route-run dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.route-run code,
.route-run dd {
  overflow-wrap: anywhere;
}

.route-run__mismatch,
.route-run__terminal-error {
  border-color: var(--color-state-warning-foreground);
  background: var(--color-state-warning);
  color: var(--color-state-warning-foreground);
}

.route-run__result dl {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--spacing-sm);
}

.route-run__result dl > div {
  min-width: 0;
  padding: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.route-run dd {
  margin: var(--spacing-xxs) 0 0;
}

.route-run__headers {
  grid-template-columns: minmax(120px, 0.4fr) minmax(0, 1fr);
  gap: 0;
}

.route-run__headers > div {
  display: contents;
}

.route-run__headers dt,
.route-run__headers dd {
  padding: var(--spacing-xs);
  border-bottom: 1px solid var(--color-hairline);
}

.route-run pre {
  max-width: 100%;
  max-height: var(--component-release-diagnostic-max-height);
  overflow: auto;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas-parchment);
  white-space: pre;
}

.route-run__assertions,
.route-run__cleanup ul,
.route-run__diagnostics ul {
  padding-inline-start: var(--spacing-lg);
}

.route-run__cleanup {
  padding: var(--spacing-md);
  border: 1px solid var(--color-state-success-foreground);
  border-radius: var(--rounded-sm);
  background: var(--color-state-success);
  color: var(--color-state-success-foreground);
}

.route-run__cleanup--failed {
  border-color: var(--color-state-danger-foreground);
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}

@media (max-width: 480px) {
  .route-run__comparison > div {
    flex-direction: column;
  }

  .route-run__result dl {
    grid-template-columns: minmax(0, 1fr);
  }

  .route-run__timeline li {
    grid-template-columns: var(--component-release-timeline-marker) minmax(0, 1fr);
  }

  .route-run__timeline time {
    grid-column: 2;
  }
}
</style>

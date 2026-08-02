<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.4.0
-->
<template>
  <section
    class="route-history"
    aria-labelledby="route-history-title"
    :aria-busy="loading"
  >
    <header>
      <div>
        <p>{{ t('routeLab.history.eyebrow') }}</p>
        <h2 id="route-history-title">
          {{ t('routeLab.history.title') }}
        </h2>
      </div>
      <span>{{ t('routeLab.history.loaded', { count: runs.length }) }}</span>
    </header>

    <p
      v-if="runs.length === 0 && !loading"
      class="route-history__empty"
    >
      {{ t('routeLab.history.empty') }}
    </p>
    <p
      v-else-if="runs.length === 0"
      class="route-history__empty"
      aria-live="polite"
    >
      <span aria-hidden="true">◌</span> {{ t('routeLab.history.loading') }}
    </p>

    <ul
      v-else-if="compact"
      class="route-history__cards"
      data-route-history-cards
    >
      <li
        v-for="run in runs"
        :key="run.id"
      >
        <div>
          <code>{{ abbreviate(run.id) }}</code>
          <StatusBadge
            :tone="stateTone(run)"
            :label="stateLabel(run)"
          />
        </div>
        <dl>
          <div><dt>{{ t('routeLab.history.request') }}</dt><dd>{{ requestSummary(run) }}</dd></div>
          <div><dt>{{ t('routeLab.history.workspace') }}</dt><dd><code>{{ abbreviate(run.workspace_id) }}</code></dd></div>
          <div><dt>{{ t('routeLab.history.assertions') }}</dt><dd>{{ assertionSummary(run) }}</dd></div>
          <div><dt>{{ t('routeLab.history.created') }}</dt><dd><time :datetime="run.created_at">{{ formatTime(run.created_at) }}</time></dd></div>
        </dl>
        <p v-if="!run.replayable">
          {{ t('routeLab.history.unavailable') }}
        </p>
        <div class="route-history__actions">
          <button
            type="button"
            data-action="view-route-run"
            @click="$emit('select', run)"
          >
            {{ t('routeLab.history.view') }}
          </button>
          <button
            type="button"
            data-action="use-route-parameters"
            @click="$emit('use', run)"
          >
            {{ t('routeLab.history.use') }}
          </button>
        </div>
      </li>
    </ul>

    <div
      v-else-if="runs.length > 0"
      class="route-history__table-wrap"
      tabindex="0"
      :aria-label="t('routeLab.history.tableLabel')"
    >
      <table data-route-history-table>
        <caption>{{ t('routeLab.history.caption') }}</caption>
        <thead>
          <tr>
            <th scope="col">
              {{ t('routeLab.history.run') }}
            </th>
            <th scope="col">
              {{ t('routeLab.history.request') }}
            </th>
            <th scope="col">
              {{ t('routeLab.history.state') }}
            </th>
            <th scope="col">
              {{ t('routeLab.history.assertions') }}
            </th>
            <th scope="col">
              {{ t('routeLab.history.created') }}
            </th>
            <th scope="col">
              {{ t('routeLab.history.actions') }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="run in runs"
            :key="run.id"
          >
            <th scope="row">
              <code>{{ abbreviate(run.id) }}</code>
            </th>
            <td>
              <strong>{{ requestSummary(run) }}</strong>
              <span>{{ t('routeLab.history.workspaceId', { id: abbreviate(run.workspace_id) }) }}</span>
              <span v-if="!run.replayable">{{ t('routeLab.history.omitted') }}</span>
            </td>
            <td>
              <StatusBadge
                :tone="stateTone(run)"
                :label="stateLabel(run)"
              />
            </td>
            <td>{{ assertionSummary(run) }}</td>
            <td><time :datetime="run.created_at">{{ formatTime(run.created_at) }}</time></td>
            <td>
              <div class="route-history__actions">
                <button
                  type="button"
                  data-action="view-route-run"
                  @click="$emit('select', run)"
                >
                  {{ t('routeLab.history.view') }}
                </button>
                <button
                  type="button"
                  data-action="use-route-parameters"
                  @click="$emit('use', run)"
                >
                  {{ t('routeLab.history.use') }}
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <button
      v-if="nextCursor !== ''"
      class="route-history__more"
      type="button"
      data-action="load-more-route-history"
      :disabled="loading"
      @click="$emit('loadMore')"
    >
      {{ loading ? t('routeLab.history.loadingMore') : t('routeLab.history.loadMore') }}
    </button>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { RouteTestRun } from '../api/route_lab'
import StatusBadge, { type StatusTone } from './StatusBadge.vue'

const { d, t } = useI18n()

withDefaults(defineProps<{
  loading?: boolean
  nextCursor?: string
  runs: RouteTestRun[]
}>(), {
  loading: false,
  nextCursor: '',
})
defineEmits<{
  loadMore: []
  select: [run: RouteTestRun]
  use: [run: RouteTestRun]
}>()

const compact = ref(false)
let media: MediaQueryList | null = null

function updateCompact(event: MediaQueryList | MediaQueryListEvent): void {
  compact.value = event.matches
}

onMounted(() => {
  if (typeof window.matchMedia !== 'function') return
  media = window.matchMedia('(max-width: 734px)')
  updateCompact(media)
  media.addEventListener('change', updateCompact)
})

onBeforeUnmount(() => {
  media?.removeEventListener('change', updateCompact)
})

function stateTone(run: RouteTestRun): StatusTone {
  switch (run.state) {
    case 'succeeded':
      return run.terminal_result?.agent_result.response.assertions.passed === false ? 'warning' : 'success'
    case 'failed':
    case 'timed_out': return 'error'
    case 'cancelled': return 'warning'
    default: return 'unknown'
  }
}

function stateLabel(run: RouteTestRun): string {
  if (run.state === 'succeeded' && run.terminal_result?.agent_result.response.assertions.passed === false) {
    return t('routeLab.history.states.assertionsFailed')
  }
  const labels: Record<RouteTestRun['state'], string> = {
    queued: t('routeLab.history.states.queued'),
    running: t('routeLab.history.states.running'),
    succeeded: t('routeLab.history.states.succeeded'),
    failed: t('routeLab.history.states.failed'),
    cancelled: t('routeLab.history.states.cancelled'),
    timed_out: t('routeLab.history.states.timedOut'),
  }
  return labels[run.state]
}

function requestSummary(run: RouteTestRun): string {
  const request = run.safe_request
  return `${request.method} ${request.scheme}://${request.host}:${request.port}${request.uri}`
}

function assertionSummary(run: RouteTestRun): string {
  const outcome = run.terminal_result?.agent_result.response.assertions
  if (outcome === undefined) return t('routeLab.history.assertionStates.notEvaluated')
  if (!outcome.complete) return t('routeLab.history.assertionStates.indeterminate')
  return outcome.passed
    ? t('routeLab.history.assertionStates.passed')
    : t('routeLab.history.assertionStates.failed')
}

function abbreviate(value: string): string {
  return value.length <= 12 ? value : `${value.slice(0, 8)}…${value.slice(-4)}`
}

function formatTime(value: string): string {
  return d(new Date(value), 'short')
}
</script>

<style scoped>
.route-history {
  display: grid;
  min-width: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
  gap: var(--spacing-md);
}

.route-history header,
.route-history__cards > li > div:first-child,
.route-history__actions {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.route-history h2,
.route-history p,
.route-history dl,
.route-history ul {
  margin: 0;
}

.route-history header p,
.route-history header > span,
.route-history td span,
.route-history__cards dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.route-history__empty {
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.route-history__table-wrap {
  max-width: 100%;
  overflow-x: auto;
}

.route-history table {
  width: 100%;
  min-width: var(--component-route-history-min-width);
  border-collapse: collapse;
  text-align: start;
}

.route-history caption {
  padding-block-end: var(--spacing-sm);
  text-align: start;
}

.route-history th,
.route-history td {
  padding: var(--spacing-sm);
  border-bottom: 1px solid var(--color-hairline);
  text-align: start;
  vertical-align: top;
}

.route-history td:nth-child(2) {
  display: grid;
  gap: var(--spacing-xxs);
}

.route-history__actions {
  justify-content: flex-start;
  flex-wrap: wrap;
}

.route-history button {
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.route-history button:disabled {
  border-color: var(--color-ink-muted-48);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.route-history__more {
  justify-self: start;
}

.route-history__cards {
  display: grid;
  padding: 0;
  gap: var(--spacing-sm);
  list-style: none;
}

.route-history__cards > li {
  display: grid;
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  gap: var(--spacing-sm);
}

.route-history__cards dl {
  display: grid;
  gap: var(--spacing-xs);
}

.route-history__cards dl > div {
  display: grid;
  grid-template-columns: minmax(80px, 0.35fr) minmax(0, 1fr);
  gap: var(--spacing-xs);
}

.route-history__cards dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}
</style>

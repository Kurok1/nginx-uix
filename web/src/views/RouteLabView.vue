<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.4.0
-->
<template>
  <section class="route-lab-page">
    <header class="route-lab-page__header">
      <div>
        <p class="route-lab-page__eyebrow">
          Draft-only isolated verification
        </p>
        <h1>Route Lab</h1>
        <p>Predict a workspace route, then optionally confirm it in a second isolated Nginx master. Production is never reloaded.</p>
      </div>
      <button
        type="button"
        :disabled="workspacePhase === 'loading' || pendingAction !== ''"
        @click="refreshWorkspaces"
      >
        {{ workspacePhase === 'loading' ? 'Refreshing…' : 'Refresh workspaces' }}
      </button>
    </header>

    <div
      v-if="pageError !== ''"
      class="route-lab-page__error"
      role="alert"
    >
      <span aria-hidden="true">◇!</span> {{ pageError }}
    </div>

    <section
      class="route-lab-page__workspace"
      aria-labelledby="route-workspace-title"
      :aria-busy="workspacePhase === 'loading'"
    >
      <div>
        <h2 id="route-workspace-title">
          Candidate workspace
        </h2>
        <p>Only a current <code>ready</code> draft can be analyzed or executed.</p>
      </div>
      <label v-if="readyWorkspaces.length > 0">
        <span>Ready workspace</span>
        <select
          :value="workspace?.id ?? ''"
          :disabled="workspacePhase === 'loading' || pendingAction !== ''"
          @change="selectWorkspace"
        >
          <option
            v-for="candidate in readyWorkspaces"
            :key="candidate.id"
            :value="candidate.id"
          >{{ candidate.name }}</option>
        </select>
      </label>
      <dl v-if="workspace !== null">
        <div><dt>State</dt><dd>{{ workspace.state }}</dd></div>
        <div><dt>Draft revision</dt><dd><code>{{ abbreviateETag(workspace.draft_etag) }}</code></dd></div>
        <div><dt>Entries</dt><dd>{{ workspace.entry_count }}</dd></div>
      </dl>
      <p
        v-else-if="workspacePhase === 'ready'"
        class="route-lab-page__empty"
      >
        No ready workspace is available. <RouterLink to="/config/workspaces">
          Create or repair a configuration workspace
        </RouterLink> before using Route Lab.
      </p>
    </section>

    <nav
      class="route-lab-page__tabs"
      aria-label="Route Lab tasks"
    >
      <button
        v-for="task in tasks"
        :key="task.id"
        type="button"
        :aria-pressed="activeTask === task.id"
        @click="activeTask = task.id"
      >
        {{ task.label }}
      </button>
    </nav>

    <div class="route-lab-page__workbench">
      <div
        class="route-lab-page__request route-lab-page__task"
        :class="{ 'route-lab-page__task--active': activeTask === 'request' }"
      >
        <RouteRequestForm
          v-model="request"
          :disabled="workspace === null || workspace.state !== 'ready'"
          :pending-action="pendingAction"
          @analyze="analyze"
          @run="requestRuntime"
        />
      </div>

      <div class="route-lab-page__evidence">
        <div
          class="route-lab-page__task"
          :class="{ 'route-lab-page__task--active': activeTask === 'analysis' }"
        >
          <RouteAnalysisPanel
            :analysis="state.analysis"
            :loading="pendingAction === 'analyze'"
          />
        </div>
        <div
          class="route-lab-page__task"
          :class="{ 'route-lab-page__task--active': activeTask === 'result' }"
        >
          <RouteRunPanel
            :run="state.activeRun"
            :stream-state="state.stream"
            @cancel="cancelRun"
          />
        </div>
      </div>
    </div>

    <p
      v-if="copyMessage !== ''"
      class="route-lab-page__copy-message"
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      {{ copyMessage }}
    </p>
    <p
      v-if="state.historyError !== ''"
      class="route-lab-page__error"
      role="alert"
    >
      {{ state.historyError }}
    </p>

    <div
      class="route-lab-page__task"
      :class="{ 'route-lab-page__task--active': activeTask === 'history' }"
    >
      <RouteHistory
        :runs="state.history"
        :next-cursor="state.historyCursor"
        :loading="state.historyLoading"
        @select="selectHistoryRun"
        @use="useHistoryParameters"
        @load-more="loadMoreHistory"
      />
    </div>

    <ConfirmModal
      :open="confirmationOpen"
      title="Run a potentially side-effecting request?"
      consequence="The request connects only to an isolated loopback Nginx, but its selected route may still reach a configured upstream and cause a real side effect. Closing this dialog after submission does not cancel the server task."
      :object-name="ROUTE_SIDE_EFFECT_CONFIRMATION"
      confirm-label="Run isolated test"
      :trigger="confirmationTrigger"
      @cancel="closeConfirmation"
      @confirm="confirmRuntime"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import { APIRequestError, apiClient } from '../api/client'
import { replayRouteRequest, type RouteTestRequest, type RouteTestRun } from '../api/route_lab'
import type { WorkspaceDetail, WorkspaceSummary } from '../api/types'
import ConfirmModal from '../components/ConfirmModal.vue'
import RouteAnalysisPanel from '../components/RouteAnalysisPanel.vue'
import RouteHistory from '../components/RouteHistory.vue'
import RouteRequestForm from '../components/RouteRequestForm.vue'
import RouteRunPanel from '../components/RouteRunPanel.vue'
import {
  requiresRouteConfirmation,
  ROUTE_SIDE_EFFECT_CONFIRMATION,
  routeLabStore,
  type RouteLabStore,
} from '../route_lab'

interface RouteLabWorkspaceClient {
  listWorkspaces: (signal?: AbortSignal) => Promise<WorkspaceSummary[]>
  getWorkspace: (id: string, signal?: AbortSignal) => Promise<WorkspaceDetail>
}

const props = withDefaults(defineProps<{
  client?: RouteLabWorkspaceClient
  store?: RouteLabStore
}>(), {
  client: () => apiClient,
  store: () => routeLabStore,
})

type Task = 'request' | 'analysis' | 'result' | 'history'

const tasks: ReadonlyArray<{ id: Task; label: string }> = [
  { id: 'request', label: 'Request' },
  { id: 'analysis', label: 'Analysis' },
  { id: 'result', label: 'Result' },
  { id: 'history', label: 'History' },
]
const routeStore = props.store
const state = routeStore.state
const workspaces = ref<WorkspaceSummary[]>([])
const workspace = ref<WorkspaceDetail | null>(null)
const workspacePhase = ref<'loading' | 'ready' | 'error'>('loading')
const localError = ref('')
const pendingAction = ref<'' | 'analyze' | 'run'>('')
const activeTask = ref<Task>('request')
const confirmationOpen = ref(false)
const confirmationTrigger = ref<HTMLElement | null>(null)
const pendingRuntimeRequest = ref<RouteTestRequest | null>(null)
const copyMessage = ref('')
const request = ref<RouteTestRequest>(defaultRequest())

const readyWorkspaces = computed(() => workspaces.value.filter(({ state: value }) => value === 'ready'))
const pageError = computed(() => localError.value || state.error)

watch(
  () => request.value.scheme,
  (scheme, previous) => {
    if (scheme === previous) return
    const previousDefault = previous === 'https' ? 443 : 80
    const nextDefault = scheme === 'https' ? 443 : 80
    request.value = {
      ...request.value,
      port: request.value.port === previousDefault ? nextDefault : request.value.port,
      sni: scheme === 'http' ? '' : request.value.sni,
    }
  },
)

onMounted(() => {
  void loadWorkspaces()
  void routeStore.loadHistory().catch(() => undefined)
})

async function loadWorkspaces(preferredId = workspace.value?.id ?? ''): Promise<void> {
  workspacePhase.value = 'loading'
  localError.value = ''
  try {
    workspaces.value = await props.client.listWorkspaces()
    const ready = workspaces.value.filter(({ state }) => state === 'ready')
    const selected = ready.find(({ id }) => id === preferredId) ?? ready[0]
    if (selected === undefined) {
      workspace.value = null
      workspacePhase.value = 'ready'
      return
    }
    await openWorkspace(selected.id)
    workspacePhase.value = 'ready'
  } catch {
    workspacePhase.value = 'error'
    localError.value = 'Ready workspaces could not be loaded.'
  }
}

function refreshWorkspaces(): void {
  void loadWorkspaces()
}

async function openWorkspace(id: string): Promise<void> {
  const selected = await props.client.getWorkspace(id)
  if (selected.state !== 'ready') {
    workspace.value = null
    throw new Error('the selected workspace is no longer ready')
  }
  const analysisMatches =
    state.analysisWorkspaceId === selected.id && state.analysisETag === selected.draft_etag
  workspace.value = selected
  if (!analysisMatches) routeStore.clearAnalysis()
}

function selectWorkspace(event: Event): void {
  const id = (event.currentTarget as HTMLSelectElement).value
  workspacePhase.value = 'loading'
  localError.value = ''
  void openWorkspace(id)
    .then(() => {
      workspacePhase.value = 'ready'
      activeTask.value = 'request'
    })
    .catch(() => {
      workspacePhase.value = 'error'
      localError.value = 'The selected ready workspace could not be opened.'
    })
}

async function analyze(input: RouteTestRequest): Promise<void> {
  if (workspace.value === null || pendingAction.value !== '') return
  pendingAction.value = 'analyze'
  localError.value = ''
  copyMessage.value = ''
  try {
    await routeStore.analyze(workspace.value, input)
    activeTask.value = 'analysis'
  } catch {
    localError.value = state.error || 'Static route analysis could not be completed.'
  } finally {
    pendingAction.value = ''
  }
}

function requestRuntime(input: RouteTestRequest, trigger: HTMLElement | null): void {
  if (workspace.value === null || pendingAction.value !== '') return
  pendingRuntimeRequest.value = cloneRequest(input)
  confirmationTrigger.value = trigger
  if (requiresRouteConfirmation(input)) {
    confirmationOpen.value = true
    return
  }
  void queueRuntime(input, '')
}

async function queueRuntime(input: RouteTestRequest, confirmation: string): Promise<void> {
  if (workspace.value === null || pendingAction.value !== '') return
  pendingAction.value = 'run'
  localError.value = ''
  copyMessage.value = ''
  try {
    await routeStore.queue(workspace.value, input, confirmation)
    clearTransientSecrets()
    pendingRuntimeRequest.value = null
    confirmationOpen.value = false
    activeTask.value = 'result'
  } catch (error: unknown) {
    if (
      confirmation === '' &&
      error instanceof APIRequestError &&
      error.apiError?.code === 'ROUTE_CONFIRMATION_REQUIRED'
    ) {
      confirmationOpen.value = true
      localError.value = ''
    } else {
      localError.value = state.error || 'The isolated route test could not be queued.'
    }
  } finally {
    pendingAction.value = ''
  }
}

function confirmRuntime(): void {
  const input = pendingRuntimeRequest.value
  if (input !== null) void queueRuntime(input, ROUTE_SIDE_EFFECT_CONFIRMATION)
}

function closeConfirmation(): void {
  confirmationOpen.value = false
  pendingRuntimeRequest.value = null
}

async function cancelRun(): Promise<void> {
  localError.value = ''
  try {
    await routeStore.cancel()
  } catch {
    localError.value = state.error || 'Cancellation could not be recorded.'
  }
}

async function selectHistoryRun(run: RouteTestRun): Promise<void> {
  localError.value = ''
  try {
    await routeStore.resume(run.id)
    activeTask.value = 'result'
  } catch {
    localError.value = state.error || 'The selected route-test evidence could not be loaded.'
  }
}

function useHistoryParameters(run: RouteTestRun): void {
  request.value = replayRouteRequest(run)
  activeTask.value = 'request'
  const omitted: string[] = []
  if (run.body_bytes > 0) omitted.push('Body')
  if (run.sensitive_header_names.length > 0) omitted.push('sensitive headers')
  copyMessage.value = omitted.length === 0
    ? 'Safe request parameters were copied into the in-memory form.'
    : `${omitted.join(' and ')} were not copied. Re-enter them before running if they are required.`
}

function loadMoreHistory(): void {
  void routeStore.loadHistory(state.historyWorkspaceId, true).catch(() => undefined)
}

function clearTransientSecrets(): void {
  request.value = {
    ...request.value,
    body: '',
    confirmation: '',
    headers: request.value.headers.map((header) =>
      isSensitiveHeader(header.name) ? { ...header, value: '' } : { ...header },
    ),
  }
}

function cloneRequest(input: RouteTestRequest): RouteTestRequest {
  return {
    ...input,
    headers: input.headers.map((header) => ({ ...header })),
    assertions: { ...input.assertions },
  }
}

function defaultRequest(): RouteTestRequest {
  return {
    scheme: 'http',
    host: '',
    port: 80,
    sni: '',
    method: 'GET',
    uri: '/',
    query: '',
    headers: [],
    body: '',
    timeout_ms: 5000,
    assertions: { status_code: 0, contains_text: '', forbidden_text: '' },
    confirmation: '',
  }
}

function isSensitiveHeader(name: string): boolean {
  const lower = name.trim().toLowerCase()
  return (
    lower === 'authorization' ||
    lower === 'proxy-authorization' ||
    lower === 'cookie' ||
    lower.includes('token') ||
    lower.includes('secret') ||
    lower.includes('api-key')
  )
}

function abbreviateETag(value: string): string {
  const digest = value.replace(/^"draft-v1:/, '').replace(/"$/, '')
  return `${digest.slice(0, 8)}…${digest.slice(-4)}`
}
</script>

<style scoped>
.route-lab-page {
  display: grid;
  min-width: 0;
  gap: var(--spacing-lg);
}

.route-lab-page__header,
.route-lab-page__workspace,
.route-lab-page__workspace dl,
.route-lab-page__tabs {
  display: flex;
  min-width: 0;
}

.route-lab-page__header {
  align-items: start;
  justify-content: space-between;
  gap: var(--spacing-lg);
}

.route-lab-page__header h1,
.route-lab-page__header p,
.route-lab-page__workspace h2,
.route-lab-page__workspace p,
.route-lab-page__workspace dl {
  margin: 0;
}

.route-lab-page__eyebrow {
  color: var(--color-primary);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
}

.route-lab-page__header h1 {
  margin-block: var(--spacing-xxs);
  font-size: var(--font-size-display-lg);
  letter-spacing: var(--letter-spacing-display);
}

.route-lab-page__header button,
.route-lab-page__tabs button {
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.route-lab-page__header button:disabled {
  border-color: var(--color-ink-muted-48);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.route-lab-page__workspace {
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  align-items: end;
  flex-wrap: wrap;
  background: var(--color-canvas);
  gap: var(--spacing-lg);
}

.route-lab-page__workspace > div:first-child {
  flex: 1 1 280px;
}

.route-lab-page__workspace label {
  display: grid;
  min-width: min(100%, 280px);
  gap: var(--spacing-xxs);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
}

.route-lab-page__workspace select {
  min-height: var(--component-control-min-size);
  padding-inline: var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.route-lab-page__workspace dl {
  flex: 1 1 300px;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--spacing-md);
}

.route-lab-page__workspace dl div {
  min-width: 80px;
}

.route-lab-page__workspace dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.route-lab-page__workspace dd {
  margin: var(--spacing-xxs) 0 0;
}

.route-lab-page__error,
.route-lab-page__copy-message,
.route-lab-page__empty {
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.route-lab-page__error {
  border-color: var(--color-state-danger-foreground);
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}

.route-lab-page__copy-message {
  border-color: var(--color-state-info-foreground);
  background: var(--color-state-info);
  color: var(--color-state-info-foreground);
}

.route-lab-page__workbench {
  display: grid;
  min-width: 0;
  grid-template-columns: var(--component-route-request-width) minmax(var(--component-route-evidence-min-width), 1fr);
  align-items: start;
  gap: var(--spacing-lg);
}

.route-lab-page__request,
.route-lab-page__evidence,
.route-lab-page__task {
  min-width: 0;
}

.route-lab-page__evidence {
  display: grid;
  gap: var(--spacing-lg);
}

.route-lab-page__tabs {
  display: none;
  overflow-x: auto;
  gap: var(--spacing-xs);
}

.route-lab-page__tabs button[aria-pressed='true'] {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

@media (max-width: 1068px) {
  .route-lab-page__workbench {
    grid-template-columns: minmax(300px, 320px) minmax(0, 1fr);
  }
}

@media (max-width: 833px) {
  .route-lab-page__workbench {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 640px) {
  .route-lab-page__header {
    flex-direction: column;
  }

  .route-lab-page__tabs {
    display: flex;
  }

  .route-lab-page__task {
    display: none;
  }

  .route-lab-page__task--active {
    display: block;
  }

  .route-lab-page__evidence {
    display: contents;
  }

  .route-lab-page__workbench {
    display: contents;
  }
}
</style>

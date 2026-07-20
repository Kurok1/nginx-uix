<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.3.0
-->
<template>
  <main class="structured-page">
    <header class="structured-page__header">
      <div>
        <p class="structured-page__eyebrow">
          {{ workspace?.name ?? 'Configuration workspace' }}
        </p>
        <h1>{{ mode === 'upstreams' ? 'Upstreams' : 'Servers & Locations' }}</h1>
        <p>Draft only — full Nginx validation has not run</p>
      </div>
      <button
        type="button"
        :disabled="phase === 'loading' || pending !== null"
        @click="refresh"
      >
        {{ phase === 'loading' ? 'Refreshing…' : 'Refresh structure' }}
      </button>
    </header>

    <nav
      class="structured-page__navigation"
      aria-label="Workspace configuration modes"
    >
      <RouterLink
        :to="upstreamsPath"
        @click="guardNavigation"
      >
        Upstreams
      </RouterLink>
      <RouterLink
        :to="serversPath"
        @click="guardNavigation"
      >
        Servers &amp; Locations
      </RouterLink>
      <RouterLink
        :to="rawEditorPath"
        data-fallback="raw-editor"
        @click="guardNavigation"
      >
        Open raw editor
      </RouterLink>
    </nav>

    <p
      v-if="successMessage !== ''"
      class="structured-page__success"
      role="status"
      aria-live="polite"
    >
      <span aria-hidden="true">✓</span>
      {{ successMessage }}
    </p>
    <p
      v-if="pageError !== ''"
      class="structured-page__error"
      role="alert"
    >
      <span aria-hidden="true">◇!</span>
      {{ pageError }}
    </p>
    <p
      v-if="phase === 'loading' && catalog === null"
      class="structured-page__loading"
      aria-busy="true"
      aria-live="polite"
    >
      <span aria-hidden="true">◌</span>
      Loading structured workspace…
    </p>

    <template v-if="workspace !== null && catalog !== null">
      <dl class="structured-page__identity">
        <div>
          <dt>Workspace state</dt>
          <dd>{{ workspace.state }}</dd>
        </div>
        <div>
          <dt>Draft revision</dt>
          <dd><code>{{ abbreviateETag(catalog.draft_etag) }}</code></dd>
        </div>
        <div>
          <dt>Projection</dt>
          <dd>{{ catalog.complete ? 'Complete' : 'Incomplete — raw editing only' }}</dd>
        </div>
      </dl>

      <StructuredDiagnosticList
        :raw-editor-path="rawEditorPath"
        :project-diagnostics="catalog.project_diagnostics"
        :diagnostics="catalog.diagnostics"
      />

      <p
        v-if="!catalog.complete"
        class="structured-page__error"
        role="alert"
      >
        <span aria-hidden="true">◇!</span>
        Structured edits are blocked because the include graph or syntax projection is incomplete.
        Use the raw editor to resolve the listed diagnostics.
      </p>

      <nav
        class="structured-task-tabs"
        aria-label="Structured workspace tasks"
      >
        <button
          type="button"
          :aria-pressed="activeTask === 'browse'"
          @click="activeTask = 'browse'"
        >
          Browse
        </button>
        <button
          type="button"
          :aria-pressed="activeTask === 'edit'"
          @click="activeTask = 'edit'"
        >
          Edit
        </button>
        <button
          type="button"
          :aria-pressed="activeTask === 'review'"
          @click="activeTask = 'review'"
        >
          Review
        </button>
      </nav>

      <label
        v-if="mode === 'upstreams'"
        class="structured-resource-selector"
      >
        <span>Upstream</span>
        <select
          :value="selectedUpstreamId ?? ''"
          @change="selectUpstreamFromControl"
        >
          <option value="">Create new upstream</option>
          <option
            v-for="candidate in catalog.upstreams"
            :key="candidate.id"
            :value="candidate.id"
          >
            {{ candidate.name }}
          </option>
        </select>
      </label>
      <label
        v-else
        class="structured-resource-selector"
        data-structured-selector="server"
      >
        <span>HTTP server</span>
        <select
          :value="selectedServerId ?? ''"
          @change="selectServerFromControl"
        >
          <option
            v-for="candidate in catalog.servers"
            :key="candidate.id"
            :value="candidate.id"
          >
            {{ serverLabel(candidate) }}
          </option>
        </select>
      </label>
      <label
        v-if="mode === 'servers' && selectedServer !== null"
        class="structured-resource-selector"
        data-structured-selector="location"
      >
        <span>Location</span>
        <select
          :value="selectedLocationId ?? ''"
          @change="selectLocationFromControl"
        >
          <option value="">Add a root location</option>
          <option
            v-for="candidate in locationOptions"
            :key="candidate.id"
            :value="candidate.id"
          >
            {{ candidate.label }}
          </option>
        </select>
      </label>

      <div class="structured-workbench">
        <aside
          class="structured-workbench__browse structured-task-panel"
          :class="{ 'structured-task-panel--active': activeTask === 'browse' }"
        >
          <template v-if="mode === 'upstreams'">
            <div class="structured-workbench__list-action">
              <button
                type="button"
                :disabled="mutationDisabled || catalog.http_blocks.every((block) => !block.editable)"
                @click="selectUpstream(null)"
              >
                Create upstream
              </button>
            </div>
            <StructuredResourceList
              label="Upstreams"
              :resources="upstreamResources"
              :selected-id="selectedUpstreamId"
              @select="selectUpstream"
            />
          </template>
          <template v-else>
            <StructuredResourceList
              label="HTTP servers"
              :resources="serverResources"
              :selected-id="selectedServerId"
              @select="selectServer"
            />
            <LocationTree
              v-if="selectedServer !== null"
              :locations="selectedServer.locations"
              :selected-id="selectedLocationId"
              @select="selectLocation"
            />
          </template>
        </aside>

        <section
          class="structured-workbench__detail structured-task-panel"
          :class="{ 'structured-task-panel--active': activeTask === 'edit' }"
        >
          <UpstreamEditor
            v-if="mode === 'upstreams'"
            :upstream="selectedUpstream"
            :http-blocks="catalog.http_blocks"
            :disabled="mutationDisabled"
            @dirty-change="editorDirty = $event"
            @form-change="handleFormChange"
            @preview="requestPreview"
          />
          <LocationEditor
            v-else
            :server="selectedServer"
            :location="selectedLocation"
            :upstreams="catalog.upstreams"
            :disabled="mutationDisabled"
            @dirty-change="editorDirty = $event"
            @form-change="handleFormChange"
            @preview="requestPreview"
          />
        </section>

        <aside
          class="structured-workbench__review structured-task-panel"
          :class="{ 'structured-task-panel--active': activeTask === 'review' }"
        >
          <StructuredChangeReview
            :preview="preview"
            :pending="pending === 'apply'"
            :confirmation="confirmation"
            :confirmation-target="confirmationTarget"
            :error-message="mutationError"
            @update:confirmation="confirmation = $event"
            @apply="applyPreview"
          />
        </aside>
      </div>

      <button
        ref="reviewTrigger"
        type="button"
        class="structured-review-trigger"
        :disabled="preview === null"
        @click="reviewDrawerOpen = true"
      >
        Review generated change
      </button>

      <ReviewDrawer
        :open="reviewDrawerOpen"
        title="Structured change review"
        :trigger="reviewTrigger"
        @close="reviewDrawerOpen = false"
      >
        <StructuredChangeReview
          :preview="preview"
          :pending="pending === 'apply'"
          :confirmation="confirmation"
          :confirmation-target="confirmationTarget"
          :error-message="mutationError"
          closable
          @update:confirmation="confirmation = $event"
          @apply="applyPreview"
          @close="reviewDrawerOpen = false"
        />
      </ReviewDrawer>
    </template>
  </main>
</template>

<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue'
import { RouterLink } from 'vue-router'

import { apiClient, APIRequestError } from '../api/client'
import type {
  StructuredChangePreview,
  StructuredChangeResult,
  StructuredConfig,
  StructuredHTTPServer,
  StructuredLocation,
  StructuredOperation,
} from '../api/structured'
import type { WorkspaceDetail } from '../api/types'
import LocationEditor from '../components/LocationEditor.vue'
import LocationTree from '../components/LocationTree.vue'
import ReviewDrawer from '../components/ReviewDrawer.vue'
import StructuredChangeReview from '../components/StructuredChangeReview.vue'
import StructuredDiagnosticList from '../components/StructuredDiagnosticList.vue'
import StructuredResourceList, {
  type StructuredResourceItem,
} from '../components/StructuredResourceList.vue'
import UpstreamEditor from '../components/UpstreamEditor.vue'
import { sessionStore } from '../session'

export type StructuredViewMode = 'servers' | 'upstreams'

export interface StructuredConfigClient {
  getWorkspace: (id: string, signal?: AbortSignal) => Promise<WorkspaceDetail>
  getStructuredConfig: (id: string, signal?: AbortSignal) => Promise<StructuredConfig>
  previewStructuredChange: (
    id: string,
    operation: StructuredOperation,
    csrfToken: string,
    signal?: AbortSignal,
  ) => Promise<StructuredChangePreview>
  applyStructuredChange: (
    id: string,
    operation: StructuredOperation,
    previewID: string,
    etag: string,
    csrfToken: string,
    signal?: AbortSignal,
  ) => Promise<StructuredChangeResult>
}

const props = withDefaults(
  defineProps<{
    workspaceId: string
    mode: StructuredViewMode
    client?: StructuredConfigClient
    csrfToken?: string
    confirmDiscard?: (message: string) => boolean
  }>(),
  {
    client: () => apiClient,
    csrfToken: '',
    confirmDiscard: undefined,
  },
)
const workspace = shallowRef<WorkspaceDetail | null>(null)
const catalog = shallowRef<StructuredConfig | null>(null)
const preview = shallowRef<StructuredChangePreview | null>(null)
const operation = shallowRef<StructuredOperation | null>(null)
const phase = ref<'error' | 'loading' | 'ready'>('loading')
const pending = ref<'apply' | 'preview' | null>(null)
const pageError = ref('')
const mutationError = ref('')
const successMessage = ref('')
const confirmation = ref('')
const selectedUpstreamId = ref<string | null>(null)
const selectedServerId = ref<string | null>(null)
const selectedLocationId = ref<string | null>(null)
const editorDirty = ref(false)
const activeTask = ref<'browse' | 'edit' | 'review'>('browse')
const reviewDrawerOpen = ref(false)
const reviewTrigger = ref<HTMLElement | null>(null)
let readController: AbortController | null = null
let mutationController: AbortController | null = null
let beforeUnloadInstalled = false

const rawEditorPath = computed(() => '/config/workspaces/' + props.workspaceId)
const upstreamsPath = computed(
  () => '/config/workspaces/' + props.workspaceId + '/upstreams',
)
const serversPath = computed(
  () => '/config/workspaces/' + props.workspaceId + '/servers',
)
const csrfToken = computed(
  () => props.csrfToken || sessionStore.state.session?.csrf_token || '',
)
const selectedUpstream = computed(
  () =>
    catalog.value?.upstreams.find((candidate) => candidate.id === selectedUpstreamId.value) ??
    null,
)
const selectedServer = computed(
  () =>
    catalog.value?.servers.find((candidate) => candidate.id === selectedServerId.value) ??
    null,
)
const selectedLocation = computed(() =>
  findLocation(selectedServer.value?.locations ?? [], selectedLocationId.value),
)
const mutationDisabled = computed(
  () =>
    pending.value !== null ||
    workspace.value?.state !== 'ready' ||
    catalog.value === null ||
    !catalog.value.complete,
)
const upstreamResources = computed<StructuredResourceItem[]>(() =>
  (catalog.value?.upstreams ?? []).map((candidate) => ({
    id: candidate.id,
    label: candidate.name,
    meta:
      String(candidate.servers.length) +
      ' server' +
      (candidate.servers.length === 1 ? '' : 's') +
      ' · ' +
      String(candidate.references.length) +
      ' reference' +
      (candidate.references.length === 1 ? '' : 's'),
    editable: candidate.editable,
    problem: resourceHasProblem(candidate.id),
  })),
)
const serverResources = computed<StructuredResourceItem[]>(() =>
  (catalog.value?.servers ?? []).map((candidate) => ({
    id: candidate.id,
    label: serverLabel(candidate),
    meta:
      (candidate.listens.length === 0 ? 'No listen summary' : candidate.listens.join(', ')) +
      ' · ' +
      String(locationCount(candidate.locations)) +
      ' locations',
    editable: candidate.editable,
    problem: resourceHasProblem(candidate.id),
  })),
)
const locationOptions = computed(() =>
  flattenLocationOptions(selectedServer.value?.locations ?? []),
)
const confirmationTarget = computed(() => {
  const current = operation.value
  if (current === null) return ''
  switch (current.kind) {
    case 'upstream.rename':
      return current.input.new_name
    case 'upstream.delete':
      return current.input.confirm_name
    case 'location.delete':
      return current.input.confirm_matcher
    default:
      return ''
  }
})

watch(
  [() => props.workspaceId, () => props.mode],
  () => {
    void load()
  },
)
watch(editorDirty, (dirty) => {
  if (dirty && !beforeUnloadInstalled) {
    window.addEventListener('beforeunload', handleBeforeUnload)
    beforeUnloadInstalled = true
  } else if (!dirty && beforeUnloadInstalled) {
    window.removeEventListener('beforeunload', handleBeforeUnload)
    beforeUnloadInstalled = false
  }
})

onMounted(() => {
  void load()
})
onBeforeUnmount(() => {
  readController?.abort()
  mutationController?.abort()
  if (beforeUnloadInstalled) {
    window.removeEventListener('beforeunload', handleBeforeUnload)
  }
})

async function load(): Promise<void> {
  readController?.abort()
  const controller = new AbortController()
  readController = controller
  phase.value = 'loading'
  pageError.value = ''
  try {
    const [nextWorkspace, nextCatalog] = await Promise.all([
      props.client.getWorkspace(props.workspaceId, controller.signal),
      props.client.getStructuredConfig(props.workspaceId, controller.signal),
    ])
    if (
      nextWorkspace.id !== props.workspaceId ||
      nextCatalog.workspace_id !== props.workspaceId ||
      nextWorkspace.draft_etag !== nextCatalog.draft_etag
    ) {
      throw new Error('workspace revision changed during structured read')
    }
    workspace.value = nextWorkspace
    catalog.value = nextCatalog
    phase.value = 'ready'
    resetSelection()
    clearPreview()
    editorDirty.value = false
    activeTask.value = 'browse'
  } catch (error) {
    if (controller.signal.aborted) return
    phase.value = 'error'
    pageError.value = errorMessage(error, 'Could not load the structured workspace.')
  } finally {
    if (readController === controller) readController = null
  }
}

function resetSelection(): void {
  const currentCatalog = catalog.value
  if (currentCatalog === null) return
  if (!currentCatalog.upstreams.some((candidate) => candidate.id === selectedUpstreamId.value)) {
    selectedUpstreamId.value = currentCatalog.upstreams[0]?.id ?? null
  }
  if (!currentCatalog.servers.some((candidate) => candidate.id === selectedServerId.value)) {
    selectedServerId.value = currentCatalog.servers[0]?.id ?? null
  }
  const server = currentCatalog.servers.find(
    (candidate) => candidate.id === selectedServerId.value,
  )
  if (
    selectedLocationId.value === null ||
    findLocation(server?.locations ?? [], selectedLocationId.value) === null
  ) {
    selectedLocationId.value = server?.locations[0]?.id ?? null
  }
}

function clearPreview(): void {
  preview.value = null
  operation.value = null
  confirmation.value = ''
  mutationError.value = ''
  reviewDrawerOpen.value = false
}

function handleFormChange(): void {
  if (pending.value === 'preview') mutationController?.abort()
  if (preview.value !== null || operation.value !== null) clearPreview()
}

function canDiscard(): boolean {
  if (!editorDirty.value) return true
  const message = 'Discard the current unsaved structured form values?'
  return props.confirmDiscard?.(message) ?? window.confirm(message)
}

function selectUpstream(id: string | null): boolean {
  if (id === selectedUpstreamId.value) return true
  if (!canDiscard()) return false
  selectedUpstreamId.value = id
  editorDirty.value = false
  clearPreview()
  return true
}

function selectServer(id: string): boolean {
  if (id === selectedServerId.value) return true
  if (!canDiscard()) return false
  selectedServerId.value = id
  selectedLocationId.value = selectedServer.value?.locations[0]?.id ?? null
  editorDirty.value = false
  clearPreview()
  return true
}

function selectLocation(id: string): boolean {
  if (id === selectedLocationId.value) return true
  if (!canDiscard()) return false
  selectedLocationId.value = id
  editorDirty.value = false
  clearPreview()
  return true
}

function selectUpstreamFromControl(event: Event): void {
  if (event.target instanceof HTMLSelectElement) {
    if (selectUpstream(event.target.value === '' ? null : event.target.value)) {
      activeTask.value = 'edit'
    }
  }
}

function selectServerFromControl(event: Event): void {
  if (event.target instanceof HTMLSelectElement && event.target.value !== '') {
    selectServer(event.target.value)
  }
}

function selectLocationFromControl(event: Event): void {
  if (!(event.target instanceof HTMLSelectElement)) return
  if (event.target.value === '') {
    if (!canDiscard()) return
    selectedLocationId.value = null
    editorDirty.value = false
    clearPreview()
    activeTask.value = 'edit'
    return
  }
  if (selectLocation(event.target.value)) activeTask.value = 'edit'
}

function refresh(): void {
  if (!canDiscard()) return
  editorDirty.value = false
  void load()
}

function guardNavigation(event: MouseEvent): void {
  if (canDiscard()) {
    editorDirty.value = false
    return
  }
  event.preventDefault()
}

async function requestPreview(nextOperation: StructuredOperation): Promise<void> {
  if (mutationDisabled.value || csrfToken.value === '') {
    mutationError.value = 'A current authenticated session is required.'
    return
  }
  mutationController?.abort()
  const controller = new AbortController()
  mutationController = controller
  pending.value = 'preview'
  mutationError.value = ''
  successMessage.value = ''
  try {
    const nextPreview = await props.client.previewStructuredChange(
      props.workspaceId,
      nextOperation,
      csrfToken.value,
      controller.signal,
    )
    if (
      nextPreview.workspace_id !== props.workspaceId ||
      nextPreview.draft_etag !== catalog.value?.draft_etag ||
      nextPreview.operation_kind !== nextOperation.kind
    ) {
      throw new Error('structured preview identity mismatch')
    }
    operation.value = nextOperation
    preview.value = nextPreview
    confirmation.value = ''
    activeTask.value = 'review'
    if (shouldOpenReviewDrawer()) reviewDrawerOpen.value = true
  } catch (error) {
    if (controller.signal.aborted) return
    mutationError.value = errorMessage(error, 'Could not generate a safe structured preview.')
  } finally {
    if (mutationController === controller) mutationController = null
    pending.value = null
  }
}

async function applyPreview(): Promise<void> {
  const currentPreview = preview.value
  const currentOperation = operation.value
  const currentWorkspace = workspace.value
  if (
    currentPreview === null ||
    currentOperation === null ||
    currentWorkspace === null ||
    !currentPreview.complete ||
    currentPreview.draft_etag !== currentWorkspace.draft_etag ||
    (confirmationTarget.value !== '' && confirmation.value !== confirmationTarget.value) ||
    csrfToken.value === ''
  ) {
    mutationError.value = 'The preview is no longer ready to apply.'
    return
  }
  mutationController?.abort()
  const controller = new AbortController()
  mutationController = controller
  pending.value = 'apply'
  mutationError.value = ''
  try {
    const result = await props.client.applyStructuredChange(
      props.workspaceId,
      currentOperation,
      currentPreview.preview_id,
      currentPreview.draft_etag,
      csrfToken.value,
      controller.signal,
    )
    successMessage.value =
      'Draft updated: ' +
      String(result.changed_paths.length) +
      ' file' +
      (result.changed_paths.length === 1 ? '' : 's') +
      ' changed. Full Nginx validation has not run.'
    editorDirty.value = false
    await load()
  } catch (error) {
    if (controller.signal.aborted) return
    mutationError.value = errorMessage(error, 'Could not update the workspace draft.')
  } finally {
    if (mutationController === controller) mutationController = null
    pending.value = null
  }
}

function shouldOpenReviewDrawer(): boolean {
  return (
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(min-width: 641px) and (max-width: 1068px)').matches
  )
}

function resourceHasProblem(id: string): boolean {
  return (
    catalog.value?.diagnostics.some(
      (diagnostic) =>
        diagnostic.related_id === id &&
        diagnostic.severity === 'blocking',
    ) ?? false
  )
}

function findLocation(
  locations: readonly StructuredLocation[],
  id: string | null,
): StructuredLocation | null {
  if (id === null) return null
  for (const candidate of locations) {
    if (candidate.id === id) return candidate
    const child = findLocation(candidate.children, id)
    if (child !== null) return child
  }
  return null
}

function locationCount(locations: readonly StructuredLocation[]): number {
  return locations.reduce(
    (count, location) => count + 1 + locationCount(location.children),
    0,
  )
}

function flattenLocationOptions(
  locations: readonly StructuredLocation[],
  level = 0,
): Array<{ id: string; label: string }> {
  return locations.flatMap((location) => [
    {
      id: location.id,
      label: '—'.repeat(level) + (level === 0 ? '' : ' ') + location.matcher,
    },
    ...flattenLocationOptions(location.children, level + 1),
  ])
}

function serverLabel(server: StructuredHTTPServer): string {
  if (server.server_names.length > 0) return server.server_names.join(', ')
  if (server.listens.length > 0) return 'Server on ' + server.listens.join(', ')
  return 'Unnamed server at ' + server.source.path + ':' + String(server.source.start_line)
}

function abbreviateETag(etag: string): string {
  const separator = etag.indexOf(':')
  return separator < 0 ? etag : etag.slice(separator + 1, separator + 9)
}

function errorMessage(error: unknown, fallback: string): string {
  if (!(error instanceof APIRequestError) || error.kind !== 'api') return fallback
  switch (error.apiError?.code) {
    case 'STRUCTURED_PREVIEW_STALE':
    case 'CONFIG_WORKSPACE_CONFLICT':
      return 'The workspace revision changed. Form values and preview were kept; refresh before retrying.'
    case 'STRUCTURED_LIMIT_EXCEEDED':
      return 'The structured project or generated diff exceeded its safe bound.'
    case 'STRUCTURED_PARSE_FAILED':
      return 'The affected configuration cannot be parsed safely. Open the raw editor.'
    case 'STRUCTURED_CONTEXT_AMBIGUOUS':
      return 'The selected syntax appears in more than one include context and is read only.'
    case 'STRUCTURED_EDIT_CONFLICT':
      return 'The selected source spans changed and the edit could not be verified.'
    case 'UPSTREAM_REFERENCED':
      return 'The upstream is still referenced. Review the visible reference locations.'
    case 'UPSTREAM_REFERENCE_INCOMPLETE':
      return 'Dynamic or unknown proxy_pass syntax prevents complete reference analysis.'
    case 'UPSTREAM_DUPLICATE':
      return 'That upstream name is already in use.'
    case 'LOCATION_DUPLICATE':
      return 'An identical location rule already exists under this parent.'
    case 'UPSTREAM_INVALID':
    case 'LOCATION_INVALID':
    case 'PROXY_PASS_INVALID':
      return 'The structured fields are not valid for this Nginx context.'
    case 'CONFIG_WORKSPACE_STALE':
      return 'Production configuration changed. Create a new workspace to continue.'
    case 'CONFIG_WORKSPACE_NEEDS_ATTENTION':
      return 'Workspace consistency cannot be confirmed; structured editing is unavailable.'
    default:
      return error.apiError?.message ?? fallback
  }
}

function handleBeforeUnload(event: BeforeUnloadEvent): void {
  if (!editorDirty.value) return
  event.preventDefault()
  event.returnValue = ''
}
</script>

<style scoped>
.structured-page {
  display: grid;
  min-width: 0;
  gap: var(--spacing-lg);
}

.structured-page__header,
.structured-page__navigation,
.structured-page__success,
.structured-page__error,
.structured-page__loading,
.structured-task-tabs,
.structured-workbench__list-action {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--spacing-sm);
}

.structured-page__header {
  justify-content: space-between;
}

.structured-page__header h1,
.structured-page__header p {
  margin: 0;
}

.structured-page__eyebrow {
  color: var(--color-primary);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
}

.structured-page__header button,
.structured-page__navigation a,
.structured-task-tabs button,
.structured-workbench__list-action button,
.structured-review-trigger {
  display: inline-flex;
  min-height: var(--component-control-min-size);
  padding-inline: var(--spacing-md);
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  text-decoration: none;
  cursor: pointer;
}

.structured-page__navigation a.router-link-exact-active {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.structured-page button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.structured-page__success,
.structured-page__error,
.structured-page__loading {
  margin: 0;
  padding: var(--spacing-sm) var(--spacing-md);
  border: 1px solid currentcolor;
  border-radius: var(--rounded-sm);
}

.structured-page__success {
  background: var(--color-state-success);
  color: var(--color-state-success-foreground);
}

.structured-page__error {
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}

.structured-page__loading {
  background: var(--color-state-info);
  color: var(--color-state-info-foreground);
}

.structured-page__identity {
  display: grid;
  margin: 0;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--spacing-sm);
}

.structured-page__identity div {
  min-width: 0;
  padding: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.structured-page__identity dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.structured-page__identity dd {
  margin: var(--spacing-xxs) 0 0;
  overflow-wrap: anywhere;
  font-weight: var(--font-weight-semibold);
}

.structured-workbench {
  display: grid;
  min-width: 0;
  grid-template-columns:
    var(--component-structured-list-width)
    minmax(var(--component-structured-detail-min-width), 1fr)
    var(--component-structured-review-width);
  gap: var(--spacing-md);
  align-items: start;
}

.structured-workbench__browse,
.structured-workbench__detail,
.structured-workbench__review {
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.structured-workbench__browse {
  display: grid;
  gap: var(--spacing-lg);
}

.structured-workbench__review {
  max-height: calc(100vh - var(--component-global-nav-height) - var(--component-sub-nav-height) - var(--spacing-xl));
  overflow: auto;
}

.structured-task-tabs,
.structured-resource-selector,
.structured-review-trigger {
  display: none;
}

@media (max-width: 1068px) {
  .structured-workbench {
    grid-template-columns:
      var(--component-structured-list-width)
      minmax(var(--component-structured-detail-min-width), 1fr);
  }

  .structured-workbench__review {
    display: none;
  }

  .structured-review-trigger {
    display: inline-flex;
    justify-self: end;
  }
}

@media (min-width: 735px) and (max-width: 833px) {
  .structured-workbench {
    grid-template-columns:
      var(--component-workspace-tree-width-narrow)
      minmax(var(--component-structured-detail-min-width), 1fr);
  }
}

@media (max-width: 734px) {
  .structured-page__identity {
    grid-template-columns: minmax(0, 1fr);
  }

  .structured-resource-selector {
    display: grid;
    min-width: 0;
    gap: var(--spacing-xs);
    font-size: var(--font-size-caption);
    font-weight: var(--font-weight-semibold);
  }

  .structured-resource-selector select {
    width: 100%;
    min-width: 0;
    min-height: var(--component-control-min-size);
    padding: var(--spacing-xs) var(--spacing-sm);
    border: 1px solid var(--color-ink-muted-48);
    border-radius: var(--rounded-sm);
    background: var(--color-canvas);
  }

  .structured-workbench {
    grid-template-columns: minmax(0, 1fr);
  }

  .structured-workbench__browse {
    display: none;
  }
}

@media (max-width: 640px) {
  .structured-task-tabs {
    display: flex;
  }

  .structured-task-tabs button[aria-pressed='true'] {
    background: var(--color-primary);
    color: var(--color-body-on-dark);
  }

  .structured-review-trigger {
    display: none;
  }

  .structured-workbench__browse,
  .structured-workbench__detail,
  .structured-workbench__review {
    display: none;
  }

  .structured-workbench .structured-task-panel--active {
    display: block;
  }

  .structured-workbench__browse.structured-task-panel--active {
    display: grid;
  }
}
</style>

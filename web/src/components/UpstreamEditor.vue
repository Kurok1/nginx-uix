<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.3.0
-->
<template>
  <section class="upstream-editor">
    <header>
      <div>
        <h2>{{ upstream === null ? t('structured.upstreamEditor.create') : t('structured.upstreamEditor.details') }}</h2>
        <p v-if="upstream !== null">
          {{ upstream.source.path }}:{{ upstream.source.start_line }}
        </p>
      </div>
      <span
        v-if="upstream !== null"
        class="upstream-editor__state"
      >
        <span aria-hidden="true">{{ upstream.editable ? '●' : '◇' }}</span>
        {{ upstream.editable ? t('structured.resources.editable') : t('structured.resources.readOnly') }}
      </span>
    </header>

    <fieldset :disabled="disabled || (upstream !== null && !upstream.editable)">
      <legend>{{ upstream === null ? t('structured.upstreamEditor.new') : t('structured.upstreamEditor.identity') }}</legend>

      <label v-if="upstream === null && httpBlocks.length > 1">
        <span>{{ t('structured.upstreamEditor.httpContext') }}</span>
        <select
          v-model="httpBlockId"
          name="http-block"
        >
          <option
            v-for="block in editableHTTPBlocks"
            :key="block.id"
            :value="block.id"
          >
            {{ block.source.path }}:{{ block.source.start_line }}
          </option>
        </select>
      </label>

      <label>
        <span>{{ t('structured.upstreamEditor.name') }}</span>
        <input
          v-model="upstreamName"
          name="upstream-name"
          type="text"
          autocomplete="off"
          :disabled="upstream !== null && serverDirty"
        >
      </label>

      <p v-if="upstream !== null && upstream.preserved_directives.length > 0">
        {{ t('structured.upstreamEditor.preservedDirectives') }}
        <code
          v-for="directive in upstream.preserved_directives"
          :key="directive.name"
        >{{ directive.name }}</code>
      </p>

      <section
        v-if="upstream !== null"
        class="upstream-editor__references"
        aria-labelledby="upstream-reference-title"
      >
        <h3 id="upstream-reference-title">
          {{ t('structured.upstreamEditor.references', { count: upstream.references.length }) }}
        </h3>
        <p v-if="upstream.references.length === 0">
          {{ t('structured.upstreamEditor.noReferences') }}
        </p>
        <ul v-else>
          <li
            v-for="reference in upstream.references"
            :key="reference.id"
          >
            <code>{{ reference.source.path }}:{{ reference.source.start_line }}:{{ reference.source.start_column }}</code>
            <span>{{ referenceStateLabel(reference.state) }}</span>
          </li>
        </ul>
      </section>

      <p
        v-if="upstream !== null && nameDirty"
        class="upstream-editor__notice"
      >
        {{ t('structured.upstreamEditor.finishName') }}
      </p>
      <p
        v-if="upstream !== null && serverDirty"
        class="upstream-editor__notice"
      >
        {{ t('structured.upstreamEditor.finishServer') }}
      </p>

      <div class="upstream-editor__actions">
        <button
          type="button"
          data-action="review-upstream"
          :disabled="!canReviewUpstream"
          @click="reviewUpstream"
        >
          {{ upstream === null ? t('structured.upstreamEditor.reviewCreation') : t('structured.upstreamEditor.reviewRename') }}
        </button>
        <button
          v-if="upstream !== null && nameDirty"
          type="button"
          data-action="reset-upstream-name"
          @click="resetUpstreamName"
        >
          {{ t('structured.upstreamEditor.resetName') }}
        </button>
        <button
          v-if="upstream !== null"
          type="button"
          data-action="delete-upstream"
          :disabled="disabled || !upstream.editable || nameDirty || serverDirty || upstream.references.length > 0"
          @click="reviewUpstreamDeletion"
        >
          {{ t('structured.upstreamEditor.reviewDeletion') }}
        </button>
      </div>
    </fieldset>

    <fieldset
      v-if="upstream !== null"
      :disabled="disabled || !upstream.editable || nameDirty"
    >
      <legend>{{ t('structured.upstreamEditor.server') }}</legend>
      <div class="upstream-editor__server-picker">
        <label>
          <span>{{ t('structured.upstreamEditor.serverEntry') }}</span>
          <select
            v-model="selectedServerId"
            name="upstream-server"
            :disabled="serverMode === 'create' || upstream.servers.length === 0 || serverDirty || nameDirty"
          >
            <option
              v-for="server in upstream.servers"
              :key="server.id"
              :value="server.id"
            >
              {{ endpointLabel(server) }}
            </option>
          </select>
        </label>
        <button
          type="button"
          data-action="add-server"
          :disabled="serverDirty || nameDirty"
          @click="beginServerCreation"
        >
          {{ t('structured.upstreamEditor.addServer') }}
        </button>
      </div>

      <div class="structured-field-grid">
        <label>
          <span>{{ t('structured.upstreamEditor.address') }}</span>
          <input
            v-model="serverForm.address"
            name="server-address"
            type="text"
            autocomplete="off"
          >
        </label>
        <label>
          <span>{{ t('structured.upstreamEditor.port') }}</span>
          <input
            v-model="serverForm.port"
            name="server-port"
            type="number"
            min="1"
            max="65535"
            :disabled="serverForm.unix"
          >
        </label>
        <label>
          <span>{{ t('structured.upstreamEditor.weight') }}</span>
          <input
            v-model="serverForm.weight"
            name="server-weight"
            type="number"
            min="1"
          >
        </label>
        <label>
          <span>{{ t('structured.upstreamEditor.maxFailures') }}</span>
          <input
            v-model="serverForm.maxFails"
            name="server-max-fails"
            type="number"
            min="0"
          >
        </label>
        <label>
          <span>{{ t('structured.upstreamEditor.failureTimeout') }}</span>
          <input
            v-model="serverForm.failTimeout"
            name="server-fail-timeout"
            type="text"
            autocomplete="off"
            placeholder="10s"
          >
        </label>
      </div>

      <div class="upstream-editor__checks">
        <label>
          <input
            v-model="serverForm.unix"
            name="server-unix"
            type="checkbox"
          >
          {{ t('structured.upstreamEditor.unixSocket') }}
        </label>
        <label>
          <input
            v-model="serverForm.backup"
            name="server-backup"
            type="checkbox"
          >
          {{ t('structured.upstreamEditor.backupPeer') }}
        </label>
        <label>
          <input
            v-model="serverForm.down"
            name="server-down"
            type="checkbox"
          >
          {{ t('structured.upstreamEditor.administrativelyDown') }}
        </label>
      </div>

      <p
        v-if="selectedServer !== null && selectedServer.preserved_parameters.length > 0"
        class="upstream-editor__preserved"
      >
        {{ t('structured.upstreamEditor.preservedParameters') }}
        <code
          v-for="parameter in selectedServer.preserved_parameters"
          :key="parameter.name"
        >{{ parameter.name }}</code>
      </p>
      <p
        v-if="serverError !== ''"
        class="upstream-editor__error"
        role="alert"
      >
        {{ serverError }}
      </p>

      <div class="upstream-editor__actions">
        <button
          type="button"
          data-action="review-server"
          :disabled="!canReviewServer"
          @click="reviewServer"
        >
          {{ serverMode === 'create' ? t('structured.upstreamEditor.reviewServerCreation') : t('structured.upstreamEditor.reviewServerUpdate') }}
        </button>
        <button
          v-if="serverMode === 'update' && selectedServer !== null"
          type="button"
          data-action="delete-server"
          :disabled="!selectedServer.editable || serverDirty || nameDirty"
          @click="reviewServerDeletion"
        >
          {{ t('structured.upstreamEditor.reviewServerDeletion') }}
        </button>
        <button
          v-if="serverMode === 'update' && serverDirty"
          type="button"
          data-action="reset-server"
          @click="resetServer"
        >
          {{ t('structured.upstreamEditor.resetServer') }}
        </button>
        <button
          v-if="serverMode === 'create' && upstream.servers.length > 0"
          type="button"
          @click="cancelServerCreation"
        >
          {{ t('common.cancel') }}
        </button>
      </div>
    </fieldset>

    <fieldset
      v-else
      :disabled="disabled"
    >
      <legend>{{ t('structured.upstreamEditor.firstServer') }}</legend>
      <div class="structured-field-grid">
        <label>
          <span>{{ t('structured.upstreamEditor.address') }}</span>
          <input
            v-model="serverForm.address"
            name="server-address"
            type="text"
            autocomplete="off"
          >
        </label>
        <label>
          <span>{{ t('structured.upstreamEditor.port') }}</span>
          <input
            v-model="serverForm.port"
            name="server-port"
            type="number"
            min="1"
            max="65535"
            :disabled="serverForm.unix"
          >
        </label>
      </div>
      <label class="upstream-editor__checkbox">
        <input
          v-model="serverForm.unix"
          name="server-unix"
          type="checkbox"
        >
        {{ t('structured.upstreamEditor.unixSocket') }}
      </label>
      <p
        v-if="serverError !== ''"
        class="upstream-editor__error"
        role="alert"
      >
        {{ serverError }}
      </p>
    </fieldset>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  StructuredHTTPBlock,
  StructuredOperation,
  StructuredReferenceState,
  StructuredUpstream,
  StructuredUpstreamServer,
  StructuredUpstreamServerInput,
} from '../api/structured'

const { t } = useI18n()

interface ServerForm {
  address: string
  port: string
  weight: string
  backup: boolean
  down: boolean
  maxFails: string
  failTimeout: string
  unix: boolean
}

const props = defineProps<{
  upstream: StructuredUpstream | null
  httpBlocks: readonly StructuredHTTPBlock[]
  disabled: boolean
}>()
const emit = defineEmits<{
  preview: [operation: StructuredOperation]
  'dirty-change': [dirty: boolean]
  'form-change': []
}>()
const upstreamName = ref('')
const httpBlockId = ref('')
const selectedServerId = ref('')
const serverMode = ref<'create' | 'update'>('update')
const serverForm = reactive<ServerForm>(emptyServerForm())
const editableHTTPBlocks = computed(() => props.httpBlocks.filter((block) => block.editable))
const selectedServer = computed(
  () =>
    props.upstream?.servers.find((server) => server.id === selectedServerId.value) ?? null,
)
const parsedServer = computed(() => parseServerForm(serverForm))
const serverError = computed(() => parsedServer.value.error)
const nameDirty = computed(
  () => props.upstream !== null && upstreamName.value !== props.upstream.name,
)
const serverDirty = computed(() => {
  if (props.upstream === null || serverMode.value === 'create') {
    return JSON.stringify(serverForm) !== JSON.stringify(emptyServerForm())
  }
  return (
    selectedServer.value !== null &&
    JSON.stringify(parsedServer.value.value) !==
      JSON.stringify(serverInput(selectedServer.value))
  )
})
const dirty = computed(
  () =>
    nameDirty.value ||
    serverDirty.value ||
    (props.upstream === null && (upstreamName.value !== '' || serverForm.address !== '')),
)
const canReviewUpstream = computed(() => {
  if (props.disabled || upstreamName.value.trim() === '') return false
  if (props.upstream !== null) {
    return props.upstream.editable && nameDirty.value && !serverDirty.value
  }
  return (
    editableHTTPBlocks.value.some((block) => block.id === httpBlockId.value) &&
    parsedServer.value.value !== null
  )
})
const canReviewServer = computed(
  () =>
    !props.disabled &&
    props.upstream !== null &&
    props.upstream.editable &&
    parsedServer.value.value !== null &&
    serverDirty.value &&
    !nameDirty.value &&
    (serverMode.value === 'create' || selectedServer.value?.editable === true),
)

watch(
  () => props.upstream,
  () => resetUpstream(),
  { immediate: true },
)
watch(selectedServerId, () => {
  if (serverMode.value === 'update') resetServer()
})
watch(dirty, (value) => emit('dirty-change', value), { immediate: true })
watch(
  [
    upstreamName,
    httpBlockId,
    selectedServerId,
    serverMode,
    () => serverForm.address,
    () => serverForm.port,
    () => serverForm.weight,
    () => serverForm.backup,
    () => serverForm.down,
    () => serverForm.maxFails,
    () => serverForm.failTimeout,
    () => serverForm.unix,
  ],
  () => emit('form-change'),
)

function emptyServerForm(): ServerForm {
  return {
    address: '',
    port: '',
    weight: '',
    backup: false,
    down: false,
    maxFails: '',
    failTimeout: '',
    unix: false,
  }
}

function resetUpstream(): void {
  upstreamName.value = props.upstream?.name ?? ''
  httpBlockId.value = editableHTTPBlocks.value[0]?.id ?? ''
  selectedServerId.value = props.upstream?.servers[0]?.id ?? ''
  serverMode.value = props.upstream?.servers.length === 0 ? 'create' : 'update'
  resetServer()
}

function resetServer(): void {
  const next = selectedServer.value
  Object.assign(
    serverForm,
    next === null
      ? emptyServerForm()
      : {
          address: next.endpoint.address,
          port: next.endpoint.port === null ? '' : String(next.endpoint.port),
          weight: next.weight === null ? '' : String(next.weight),
          backup: next.backup,
          down: next.down,
          maxFails: next.max_fails === null ? '' : String(next.max_fails),
          failTimeout: next.fail_timeout ?? '',
          unix: next.endpoint.unix,
        },
  )
}

function serverInput(server: StructuredUpstreamServer): StructuredUpstreamServerInput {
  return {
    address: server.endpoint.address,
    port: server.endpoint.port,
    unix: server.endpoint.unix,
    weight: server.weight,
    backup: server.backup,
    down: server.down,
    max_fails: server.max_fails,
    fail_timeout: server.fail_timeout,
  }
}

function parseServerForm(
  form: ServerForm,
): { value: StructuredUpstreamServerInput | null; error: string } {
  const address = form.address.trim()
  if (address === '') return { value: null, error: t('structured.upstreamEditor.addressRequired') }
  const port = form.unix ? null : optionalInteger(form.port, 1, 65_535)
  const weight = optionalInteger(form.weight, 1)
  const maxFails = optionalInteger(form.maxFails, 0)
  if (port === undefined) return { value: null, error: t('structured.upstreamEditor.invalidPort') }
  if (weight === undefined) return { value: null, error: t('structured.upstreamEditor.invalidWeight') }
  if (maxFails === undefined) return { value: null, error: t('structured.upstreamEditor.invalidMaxFailures') }
  return {
    value: {
      address,
      port,
      unix: form.unix,
      weight,
      backup: form.backup,
      down: form.down,
      max_fails: maxFails,
      fail_timeout: form.failTimeout.trim() === '' ? null : form.failTimeout.trim(),
    },
    error: '',
  }
}

function optionalInteger(raw: string, minimum: number, maximum = Number.MAX_SAFE_INTEGER): number | null | undefined {
  if (raw === '') return null
  const value = Number(raw)
  return Number.isSafeInteger(value) && value >= minimum && value <= maximum
    ? value
    : undefined
}

function reviewUpstream(): void {
  if (!canReviewUpstream.value) return
  if (props.upstream === null) {
    const server = parsedServer.value.value
    if (server === null) return
    emit('preview', {
      kind: 'upstream.create',
      input: {
        http_block_id: httpBlockId.value,
        name: upstreamName.value.trim(),
        servers: [server],
      },
    })
    return
  }
  emit('preview', {
    kind: 'upstream.rename',
    input: { upstream_id: props.upstream.id, new_name: upstreamName.value.trim() },
  })
}

function reviewUpstreamDeletion(): void {
  if (
    props.upstream === null ||
    !props.upstream.editable ||
    props.disabled ||
    nameDirty.value ||
    serverDirty.value ||
    props.upstream.references.length > 0
  ) return
  emit('preview', {
    kind: 'upstream.delete',
    input: { upstream_id: props.upstream.id, confirm_name: props.upstream.name },
  })
}

function beginServerCreation(): void {
  if (serverDirty.value || nameDirty.value) return
  serverMode.value = 'create'
  selectedServerId.value = ''
  resetServer()
}

function cancelServerCreation(): void {
  serverMode.value = 'update'
  selectedServerId.value = props.upstream?.servers[0]?.id ?? ''
  resetServer()
}

function reviewServer(): void {
  const upstream = props.upstream
  const value = parsedServer.value.value
  if (!canReviewServer.value || upstream === null || value === null) return
  if (serverMode.value === 'create') {
    emit('preview', {
      kind: 'upstream_server.create',
      input: { upstream_id: upstream.id, server: value },
    })
    return
  }
  const server = selectedServer.value
  if (server === null) return
  emit('preview', {
    kind: 'upstream_server.update',
    input: { upstream_id: upstream.id, server_id: server.id, server: value },
  })
}

function reviewServerDeletion(): void {
  const upstream = props.upstream
  const server = selectedServer.value
  if (
    upstream === null ||
    server === null ||
    !server.editable ||
    props.disabled ||
    serverDirty.value ||
    nameDirty.value
  ) return
  emit('preview', {
    kind: 'upstream_server.delete',
    input: { upstream_id: upstream.id, server_id: server.id },
  })
}

function resetUpstreamName(): void {
  upstreamName.value = props.upstream?.name ?? ''
}

function endpointLabel(server: StructuredUpstreamServer): string {
  if (server.endpoint.unix) return 'unix:' + server.endpoint.address
  return (
    server.endpoint.address +
    (server.endpoint.port === null ? '' : ':' + String(server.endpoint.port))
  )
}

function referenceStateLabel(state: StructuredReferenceState): string {
  const labels: Record<StructuredReferenceState, string> = {
    resolved: t('structured.upstreamEditor.referenceStates.resolved'),
    dangling: t('structured.upstreamEditor.referenceStates.dangling'),
    external: t('structured.upstreamEditor.referenceStates.external'),
    dynamic: t('structured.upstreamEditor.referenceStates.dynamic'),
    ambiguous: t('structured.upstreamEditor.referenceStates.ambiguous'),
    unknown: t('structured.upstreamEditor.referenceStates.unknown'),
  }
  return labels[state]
}
</script>

<style scoped>
.upstream-editor,
.upstream-editor fieldset,
.upstream-editor label,
.structured-field-grid {
  display: grid;
  min-width: 0;
}

.upstream-editor {
  gap: var(--spacing-lg);
}

.upstream-editor header,
.upstream-editor__actions,
.upstream-editor__server-picker,
.upstream-editor__checks {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--spacing-sm);
}

.upstream-editor header {
  justify-content: space-between;
}

.upstream-editor h2,
.upstream-editor header p {
  margin: 0;
}

.upstream-editor header p {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.upstream-editor fieldset {
  gap: var(--spacing-md);
  margin: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
}

.upstream-editor legend {
  padding-inline: var(--spacing-xs);
  font-weight: var(--font-weight-semibold);
}

.upstream-editor label {
  gap: var(--spacing-xs);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
}

.upstream-editor input,
.upstream-editor select,
.upstream-editor button {
  min-height: var(--component-control-min-size);
}

.upstream-editor input,
.upstream-editor select {
  width: 100%;
  min-width: 0;
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  color: var(--color-ink);
}

.upstream-editor button {
  padding-inline: var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.upstream-editor button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.structured-field-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--spacing-md);
}

.upstream-editor__server-picker label {
  min-width: min(100%, var(--component-structured-detail-min-width));
  flex: 1;
}

.upstream-editor__checks label,
.upstream-editor__checkbox {
  display: inline-flex;
  min-height: var(--component-control-min-size);
  grid-auto-flow: column;
  align-items: center;
  justify-content: start;
}

.upstream-editor__checks input,
.upstream-editor__checkbox input {
  width: auto;
  min-width: var(--spacing-md);
}

.upstream-editor__preserved code,
.upstream-editor fieldset > p code {
  margin-inline-start: var(--spacing-xs);
}

.upstream-editor__references,
.upstream-editor__references ul {
  display: grid;
  min-width: 0;
  gap: var(--spacing-xs);
}

.upstream-editor__references h3,
.upstream-editor__references p,
.upstream-editor__references ul {
  margin: 0;
}

.upstream-editor__references ul {
  padding-inline-start: var(--spacing-lg);
}

.upstream-editor__references li {
  overflow-wrap: anywhere;
}

.upstream-editor__references li span {
  margin-inline-start: var(--spacing-xs);
}

.upstream-editor__notice {
  margin: 0;
  color: var(--color-state-warning-foreground);
}

.upstream-editor__state {
  display: inline-flex;
  gap: var(--spacing-xxs);
  color: var(--color-state-info-foreground);
}

.upstream-editor__error {
  margin: 0;
  color: var(--color-state-danger-foreground);
}

@media (max-width: 734px) {
  .structured-field-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

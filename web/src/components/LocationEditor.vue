<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.3.0
-->
<template>
  <section class="location-editor">
    <header>
      <div>
        <h2>Location details</h2>
        <p v-if="location !== null">
          {{ location.source.path }}:{{ location.source.start_line }}
        </p>
      </div>
      <div class="location-editor__create-actions">
        <button
          type="button"
          data-action="add-root"
          :disabled="disabled || dirty || server === null || !server.editable"
          @click="beginCreation('root')"
        >
          Add root location
        </button>
        <button
          v-if="location !== null"
          type="button"
          data-action="add-child"
          :disabled="disabled || dirty || !location.editable"
          @click="beginCreation('child')"
        >
          Add child
        </button>
      </div>
    </header>

    <p
      v-if="server === null"
      class="location-editor__empty"
      role="status"
    >
      Select an HTTP server to manage its locations.
    </p>
    <p
      v-else-if="mode === 'empty'"
      class="location-editor__empty"
      role="status"
    >
      Select a location or add a root location.
    </p>

    <form
      v-else
      class="location-editor__form"
      @submit.prevent="reviewLocation"
    >
      <fieldset :disabled="formDisabled">
        <legend>
          {{ mode === 'edit' ? 'Location matcher' : mode === 'create-child' ? 'New child matcher' : 'New root matcher' }}
        </legend>
        <div class="structured-field-grid">
          <label>
            <span>Matcher type</span>
            <select
              v-model="matcherType"
              name="location-type"
            >
              <option value="exact">Exact (=)</option>
              <option value="prefix">Prefix</option>
              <option value="prefix_priority">Priority prefix (^~)</option>
              <option value="regex">Regular expression (~)</option>
              <option value="regex_insensitive">Case-insensitive regex (~*)</option>
              <option value="named">Named (@)</option>
            </select>
          </label>
          <label>
            <span>Matcher</span>
            <input
              v-model="matcher"
              name="location-matcher"
              type="text"
              autocomplete="off"
            >
          </label>
        </div>

        <p v-if="location !== null && location.unknown_directive_count > 0">
          {{ location.unknown_directive_count }} preserved directive{{ location.unknown_directive_count === 1 ? '' : 's' }}
          remain read only and will not be rewritten.
        </p>
      </fieldset>

      <fieldset :disabled="formDisabled || (mode === 'edit' && location?.proxy_pass_editable === false)">
        <legend>proxy_pass</legend>
        <label>
          <span>Proxy behavior</span>
          <select
            v-model="proxyMode"
            name="proxy-mode"
          >
            <option
              v-if="mode === 'edit'"
              value="preserve"
            >
              Preserve current directive
            </option>
            <option value="set">Set structured upstream</option>
            <option value="remove">No proxy_pass</option>
          </select>
        </label>

        <div
          v-if="proxyMode === 'set'"
          class="structured-field-grid"
        >
          <label>
            <span>Upstream</span>
            <select
              v-model="proxyUpstreamId"
              name="proxy-upstream"
            >
              <option value="">Select upstream</option>
              <option
                v-for="candidate in upstreams"
                :key="candidate.id"
                :value="candidate.id"
              >
                {{ candidate.name }}
              </option>
            </select>
          </label>
          <label>
            <span>Scheme</span>
            <select
              v-model="proxyScheme"
              name="proxy-scheme"
            >
              <option value="http">http</option>
              <option value="https">https</option>
            </select>
          </label>
          <label>
            <span>Port</span>
            <input
              v-model="proxyPort"
              name="proxy-port"
              type="number"
              min="1"
              max="65535"
            >
          </label>
          <label>
            <span>URI suffix</span>
            <input
              v-model="proxyURI"
              name="proxy-uri"
              type="text"
              autocomplete="off"
              placeholder="/api/"
            >
          </label>
        </div>

        <p v-if="mode === 'edit' && location !== null && !location.proxy_pass_editable">
          proxy_pass is read only: {{ location.proxy_pass_read_only_reason ?? 'unsupported syntax' }}.
        </p>
      </fieldset>

      <p
        v-if="validationError !== ''"
        class="location-editor__error"
        role="alert"
      >
        {{ validationError }}
      </p>

      <div class="location-editor__actions">
        <button
          type="button"
          data-action="review-location"
          :disabled="!canReview"
          @click="reviewLocation"
        >
          {{ mode === 'edit' ? 'Review location update' : 'Review location creation' }}
        </button>
        <button
          v-if="mode === 'edit' && location !== null"
          type="button"
          data-action="delete-location"
          :disabled="disabled || dirty || !location.editable"
          @click="reviewDeletion"
        >
          Review location deletion
        </button>
        <button
          v-if="mode === 'edit' && dirty"
          type="button"
          data-action="reset-location"
          @click="resetEditor"
        >
          Reset location changes
        </button>
        <button
          v-if="mode !== 'edit'"
          type="button"
          @click="cancelCreation"
        >
          Cancel
        </button>
      </div>
    </form>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import type {
  EditableMatcherType,
  StructuredHTTPServer,
  StructuredLocation,
  StructuredOperation,
  StructuredProxyMode,
  StructuredProxyPassInput,
  StructuredUpstream,
} from '../api/structured'

type EditorMode = 'create-child' | 'create-root' | 'edit' | 'empty'

const props = defineProps<{
  server: StructuredHTTPServer | null
  location: StructuredLocation | null
  upstreams: readonly StructuredUpstream[]
  disabled: boolean
}>()
const emit = defineEmits<{
  preview: [operation: StructuredOperation]
  'dirty-change': [dirty: boolean]
  'form-change': []
}>()
const mode = ref<EditorMode>('empty')
const matcherType = ref<EditableMatcherType>('prefix')
const matcher = ref('')
const proxyMode = ref<StructuredProxyMode>('preserve')
const proxyUpstreamId = ref('')
const proxyScheme = ref<'http' | 'https'>('http')
const proxyPort = ref('')
const proxyURI = ref('')
const proxyPass = computed(() => parseProxyPass())
const validationError = computed(() => {
  if (matcher.value.trim() === '') return 'Location matcher is required.'
  if (proxyMode.value === 'set' && proxyPass.value === null) {
    return 'Select an upstream and enter a valid optional port.'
  }
  return ''
})
const formDisabled = computed(
  () =>
    props.disabled ||
    props.server === null ||
    (mode.value === 'edit' && props.location?.editable !== true),
)
const dirty = computed(() => {
  if (mode.value === 'create-child' || mode.value === 'create-root') {
    return matcherType.value !== 'prefix' || matcher.value !== '' || proxyMode.value !== 'remove'
  }
  if (mode.value !== 'edit' || props.location === null) return false
  return (
    matcherType.value !== props.location.type ||
    matcher.value !== props.location.matcher ||
    proxyMode.value !== 'preserve'
  )
})
const canReview = computed(
  () =>
    !formDisabled.value &&
    validationError.value === '' &&
    dirty.value &&
    (mode.value !== 'edit' || proxyMode.value === 'preserve' || props.location?.proxy_pass_editable === true),
)

watch(
  [() => props.server, () => props.location],
  () => resetEditor(),
  { immediate: true },
)
watch(proxyMode, (next) => {
  if (next === 'set' && proxyUpstreamId.value === '') {
    proxyUpstreamId.value = props.upstreams[0]?.id ?? ''
  }
})
watch(dirty, (value) => emit('dirty-change', value), { immediate: true })
watch(
  [mode, matcherType, matcher, proxyMode, proxyUpstreamId, proxyScheme, proxyPort, proxyURI],
  () => emit('form-change'),
)

function resetEditor(): void {
  if (props.location === null) {
    mode.value = 'empty'
    matcherType.value = 'prefix'
    matcher.value = ''
    proxyMode.value = 'remove'
    resetProxy()
    return
  }
  mode.value = 'edit'
  matcherType.value = props.location.type === 'unknown' ? 'prefix' : props.location.type
  matcher.value = props.location.matcher
  proxyMode.value = 'preserve'
  const reference = props.location.proxy_passes[0]
  proxyUpstreamId.value = reference?.upstream_id ?? props.upstreams[0]?.id ?? ''
  proxyScheme.value = reference?.scheme ?? 'http'
  proxyPort.value = reference?.port === null || reference?.port === undefined ? '' : String(reference.port)
  proxyURI.value = reference?.uri ?? ''
}

function resetProxy(): void {
  proxyUpstreamId.value = props.upstreams[0]?.id ?? ''
  proxyScheme.value = 'http'
  proxyPort.value = ''
  proxyURI.value = ''
}

function beginCreation(scope: 'child' | 'root'): void {
  if (props.server === null || props.disabled || dirty.value) return
  mode.value = scope === 'child' && props.location !== null ? 'create-child' : 'create-root'
  matcherType.value = 'prefix'
  matcher.value = ''
  proxyMode.value = 'remove'
  resetProxy()
}

function cancelCreation(): void {
  resetEditor()
}

function parseProxyPass(): StructuredProxyPassInput | null {
  if (proxyMode.value !== 'set' || proxyUpstreamId.value === '') return null
  const port = optionalPort(proxyPort.value)
  if (port === undefined) return null
  return {
    upstream_id: proxyUpstreamId.value,
    scheme: proxyScheme.value,
    port,
    uri: proxyURI.value,
  }
}

function optionalPort(raw: string): number | null | undefined {
  if (raw === '') return null
  const value = Number(raw)
  return Number.isSafeInteger(value) && value >= 1 && value <= 65_535
    ? value
    : undefined
}

function reviewLocation(): void {
  if (!canReview.value || props.server === null) return
  if (mode.value === 'edit' && props.location !== null) {
    emit('preview', {
      kind: 'location.update',
      input: {
        location_id: props.location.id,
        type: matcherType.value,
        matcher: matcher.value.trim(),
        proxy_mode: proxyMode.value,
        proxy_pass: proxyMode.value === 'set' ? proxyPass.value : null,
      },
    })
    return
  }
  const parentId =
    mode.value === 'create-child' && props.location !== null
      ? props.location.id
      : props.server.id
  emit('preview', {
    kind: 'location.create',
    input: {
      parent_id: parentId,
      type: matcherType.value,
      matcher: matcher.value.trim(),
      proxy_pass: proxyMode.value === 'set' ? proxyPass.value : null,
    },
  })
}

function reviewDeletion(): void {
  if (props.location === null || !props.location.editable || props.disabled || dirty.value) return
  emit('preview', {
    kind: 'location.delete',
    input: {
      location_id: props.location.id,
      confirm_matcher: props.location.matcher,
    },
  })
}
</script>

<style scoped>
.location-editor,
.location-editor__form,
.location-editor fieldset,
.location-editor label,
.structured-field-grid {
  display: grid;
  min-width: 0;
}

.location-editor,
.location-editor__form {
  gap: var(--spacing-lg);
}

.location-editor header,
.location-editor__create-actions,
.location-editor__actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--spacing-sm);
}

.location-editor header {
  justify-content: space-between;
}

.location-editor h2,
.location-editor header p {
  margin: 0;
}

.location-editor header p {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.location-editor fieldset {
  gap: var(--spacing-md);
  margin: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
}

.location-editor legend {
  padding-inline: var(--spacing-xs);
  font-weight: var(--font-weight-semibold);
}

.location-editor label {
  gap: var(--spacing-xs);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
}

.location-editor input,
.location-editor select,
.location-editor button {
  min-height: var(--component-control-min-size);
}

.location-editor input,
.location-editor select {
  width: 100%;
  min-width: 0;
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  color: var(--color-ink);
}

.location-editor button {
  padding-inline: var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.location-editor button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.structured-field-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--spacing-md);
}

.location-editor__error {
  margin: 0;
  color: var(--color-state-danger-foreground);
}

.location-editor__empty {
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

@media (max-width: 734px) {
  .structured-field-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

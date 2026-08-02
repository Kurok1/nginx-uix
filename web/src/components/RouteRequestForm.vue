<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.4.0
-->
<template>
  <form
    class="route-request-form"
    aria-labelledby="route-request-title"
    @submit.prevent="emitAnalyze"
  >
    <header>
      <div>
        <p class="route-request-form__eyebrow">
          {{ t('routeLab.request.eyebrow') }}
        </p>
        <h2 id="route-request-title">
          {{ t('routeLab.request.title') }}
        </h2>
      </div>
      <span>{{ pending ? t('routeLab.request.working') : t('routeLab.request.notSaved') }}</span>
    </header>

    <fieldset :disabled="disabled || pending">
      <legend>{{ t('routeLab.request.connection') }}</legend>
      <div class="route-request-form__grid route-request-form__grid--connection">
        <label>
          <span>{{ t('routeLab.request.scheme') }}</span>
          <select
            name="scheme"
            :value="modelValue.scheme"
            @change="updateText('scheme', $event)"
          >
            <option value="http">HTTP</option>
            <option value="https">HTTPS</option>
          </select>
        </label>
        <label>
          <span>{{ t('routeLab.request.port') }}</span>
          <input
            name="port"
            type="number"
            min="0"
            max="65535"
            inputmode="numeric"
            :value="modelValue.port"
            @input="updateNumber('port', $event)"
          >
        </label>
      </div>
      <label>
        <span>{{ t('routeLab.request.host') }} <strong aria-hidden="true">*</strong></span>
        <input
          name="host"
          type="text"
          required
          maxlength="255"
          autocomplete="off"
          spellcheck="false"
          :value="modelValue.host"
          aria-describedby="route-host-help"
          @input="updateText('host', $event)"
        >
        <small id="route-host-help">{{ t('routeLab.request.hostHelp') }}</small>
      </label>
      <label>
        <span>{{ t('routeLab.request.tlsSni') }}</span>
        <input
          name="sni"
          type="text"
          maxlength="253"
          autocomplete="off"
          spellcheck="false"
          :disabled="modelValue.scheme !== 'https'"
          :value="modelValue.sni"
          aria-describedby="route-sni-help"
          @input="updateText('sni', $event)"
        >
        <small id="route-sni-help">{{ t('routeLab.request.sniHelp') }}</small>
      </label>
    </fieldset>

    <fieldset :disabled="disabled || pending">
      <legend>{{ t('routeLab.request.httpRequest') }}</legend>
      <div class="route-request-form__grid route-request-form__grid--request-line">
        <label>
          <span>{{ t('routeLab.request.method') }}</span>
          <select
            name="method"
            :value="modelValue.method"
            @change="updateText('method', $event)"
          >
            <option
              v-for="method in methods"
              :key="method"
              :value="method"
            >{{ method }}</option>
          </select>
        </label>
        <label>
          <span>{{ t('routeLab.request.uriPath') }} <strong aria-hidden="true">*</strong></span>
          <input
            name="uri"
            type="text"
            required
            pattern="/.*"
            autocomplete="off"
            spellcheck="false"
            :value="modelValue.uri"
            @input="updateText('uri', $event)"
          >
        </label>
      </div>
      <label>
        <span>{{ t('routeLab.request.query') }}</span>
        <input
          name="query"
          type="text"
          autocomplete="off"
          spellcheck="false"
          :placeholder="t('routeLab.request.queryPlaceholder')"
          :value="modelValue.query"
          @input="updateText('query', $event)"
        >
      </label>

      <fieldset class="route-request-form__headers">
        <legend>{{ t('routeLab.request.headers') }}</legend>
        <p id="route-header-help">
          {{ t('routeLab.request.headerHelp') }}
        </p>
        <div
          v-for="(header, index) in modelValue.headers"
          :key="index"
          class="route-request-form__header-row"
        >
          <label>
            <span>{{ t('routeLab.request.headerName', { number: index + 1 }) }}</span>
            <input
              type="text"
              autocomplete="off"
              spellcheck="false"
              :value="header.name"
              aria-describedby="route-header-help"
              @input="updateHeader(index, 'name', $event)"
            >
          </label>
          <label>
            <span>{{ t('routeLab.request.headerValue', { number: index + 1 }) }}</span>
            <input
              :type="isSensitiveHeader(header.name) ? 'password' : 'text'"
              autocomplete="off"
              spellcheck="false"
              :value="header.value"
              aria-describedby="route-header-help"
              @input="updateHeader(index, 'value', $event)"
            >
          </label>
          <button
            type="button"
            :aria-label="t('routeLab.request.removeHeaderLabel', { number: index + 1 })"
            @click="removeHeader(index)"
          >
            {{ t('routeLab.request.remove') }}
          </button>
        </div>
        <button
          type="button"
          :disabled="modelValue.headers.length >= 32"
          @click="addHeader"
        >
          {{ t('routeLab.request.addHeader') }}
        </button>
      </fieldset>

      <label>
        <span>{{ t('routeLab.request.body') }}</span>
        <textarea
          name="body"
          maxlength="65536"
          autocomplete="off"
          spellcheck="false"
          :value="modelValue.body"
          aria-describedby="route-body-help"
          @input="updateText('body', $event)"
        />
        <small id="route-body-help">{{ t('routeLab.request.bodyHelp') }}</small>
      </label>
      <label>
        <span>{{ t('routeLab.request.timeout') }}</span>
        <input
          name="timeout_ms"
          type="number"
          min="100"
          max="30000"
          step="100"
          inputmode="numeric"
          :value="modelValue.timeout_ms"
          @input="updateNumber('timeout_ms', $event)"
        >
      </label>
    </fieldset>

    <fieldset :disabled="disabled || pending">
      <legend>{{ t('routeLab.request.assertions') }}</legend>
      <label>
        <span>{{ t('routeLab.request.expectedStatus') }}</span>
        <input
          name="assert_status"
          type="number"
          min="0"
          max="599"
          inputmode="numeric"
          :value="modelValue.assertions.status_code"
          @input="updateAssertionNumber('status_code', $event)"
        >
      </label>
      <label>
        <span>{{ t('routeLab.request.responseContains') }}</span>
        <input
          name="assert_contains"
          type="text"
          maxlength="1024"
          autocomplete="off"
          :value="modelValue.assertions.contains_text"
          @input="updateAssertionText('contains_text', $event)"
        >
      </label>
      <label>
        <span>{{ t('routeLab.request.responseExcludes') }}</span>
        <input
          name="assert_forbidden"
          type="text"
          maxlength="1024"
          autocomplete="off"
          :value="modelValue.assertions.forbidden_text"
          @input="updateAssertionText('forbidden_text', $event)"
        >
      </label>
    </fieldset>

    <p
      v-if="sideEffecting"
      class="route-request-form__warning"
      role="status"
    >
      <span aria-hidden="true">△</span> {{ t('routeLab.request.sideEffectWarning') }}
    </p>

    <div class="route-request-form__actions">
      <button
        type="submit"
        data-action="analyze-route"
        :disabled="disabled || pending"
      >
        {{ pendingAction === 'analyze' ? t('routeLab.request.analyzing') : t('routeLab.request.analyze') }}
      </button>
      <button
        type="button"
        data-action="run-route-test"
        :disabled="disabled || pending"
        @click="emitRun"
      >
        {{ pendingAction === 'run' ? t('routeLab.request.queuing') : t('routeLab.request.run') }}
      </button>
    </div>
  </form>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  RouteAssertionsInput,
  RouteMethod,
  RouteTestRequest,
} from '../api/route_lab'
import { requiresRouteConfirmation } from '../route_lab'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  disabled?: boolean
  modelValue: RouteTestRequest
  pendingAction?: '' | 'analyze' | 'run'
}>(), {
  disabled: false,
  pendingAction: '',
})
const emit = defineEmits<{
  'update:modelValue': [value: RouteTestRequest]
  analyze: [value: RouteTestRequest]
  run: [value: RouteTestRequest, trigger: HTMLElement | null]
}>()

const methods: readonly RouteMethod[] = ['GET', 'HEAD', 'OPTIONS', 'POST', 'PUT', 'PATCH', 'DELETE']
const pending = computed(() => props.pendingAction !== '')
const sideEffecting = computed(() => requiresRouteConfirmation(props.modelValue))

function cloneRequest(): RouteTestRequest {
  return {
    ...props.modelValue,
    headers: props.modelValue.headers.map((header) => ({ ...header })),
    assertions: { ...props.modelValue.assertions },
  }
}

function update<Key extends keyof RouteTestRequest>(key: Key, value: RouteTestRequest[Key]): void {
  emit('update:modelValue', { ...cloneRequest(), [key]: value })
}

function updateText(
  key: 'scheme' | 'host' | 'sni' | 'method' | 'uri' | 'query' | 'body',
  event: Event,
): void {
  update(key, (event.currentTarget as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement).value as never)
}

function updateNumber(key: 'port' | 'timeout_ms', event: Event): void {
  update(key, Number((event.currentTarget as HTMLInputElement).value))
}

function updateHeader(index: number, key: 'name' | 'value', event: Event): void {
  const headers = props.modelValue.headers.map((header) => ({ ...header }))
  const header = headers[index]
  if (header === undefined) return
  header[key] = (event.currentTarget as HTMLInputElement).value
  update('headers', headers)
}

function addHeader(): void {
  if (props.modelValue.headers.length >= 32) return
  update('headers', [...props.modelValue.headers.map((header) => ({ ...header })), { name: '', value: '' }])
}

function removeHeader(index: number): void {
  update('headers', props.modelValue.headers.filter((_, candidate) => candidate !== index))
}

function updateAssertionText(
  key: 'contains_text' | 'forbidden_text',
  event: Event,
): void {
  updateAssertions(key, (event.currentTarget as HTMLInputElement).value)
}

function updateAssertionNumber(key: 'status_code', event: Event): void {
  updateAssertions(key, Number((event.currentTarget as HTMLInputElement).value))
}

function updateAssertions<Key extends keyof RouteAssertionsInput>(
  key: Key,
  value: RouteAssertionsInput[Key],
): void {
  update('assertions', { ...props.modelValue.assertions, [key]: value })
}

function emitAnalyze(): void {
  emit('analyze', cloneRequest())
}

function emitRun(event: MouseEvent): void {
  emit('run', cloneRequest(), event.currentTarget as HTMLElement | null)
}

function isSensitiveHeader(name: string): boolean {
  const lower = name.trim().toLowerCase()
  return (
    lower === 'authorization' ||
    lower === 'proxy-authorization' ||
    lower === 'cookie' ||
    lower === 'set-cookie' ||
    lower.includes('token') ||
    lower.includes('secret') ||
    lower.includes('api-key')
  )
}
</script>

<style scoped>
.route-request-form,
.route-request-form fieldset,
.route-request-form label,
.route-request-form__headers {
  display: grid;
  min-width: 0;
}

.route-request-form {
  align-content: start;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
  gap: var(--spacing-lg);
}

.route-request-form header,
.route-request-form__actions,
.route-request-form__header-row {
  display: flex;
  min-width: 0;
}

.route-request-form header {
  align-items: start;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.route-request-form h2,
.route-request-form p {
  margin: 0;
}

.route-request-form__eyebrow,
.route-request-form header > span,
.route-request-form small,
.route-request-form__headers > p {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.route-request-form fieldset {
  margin: 0;
  padding: 0;
  border: 0;
  gap: var(--spacing-sm);
}

.route-request-form legend {
  margin-block-end: var(--spacing-sm);
  font-weight: var(--font-weight-semibold);
}

.route-request-form label {
  gap: var(--spacing-xxs);
}

.route-request-form label > span {
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
}

.route-request-form input,
.route-request-form select,
.route-request-form textarea,
.route-request-form button {
  min-height: var(--component-control-min-size);
  font: inherit;
}

.route-request-form input,
.route-request-form select,
.route-request-form textarea {
  width: 100%;
  min-width: 0;
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  color: var(--color-ink);
}

.route-request-form textarea {
  min-height: var(--component-route-body-min-height);
  resize: vertical;
}

.route-request-form__grid {
  display: grid;
  min-width: 0;
  gap: var(--spacing-sm);
}

.route-request-form__grid--connection {
  grid-template-columns: minmax(0, 1fr) 112px;
}

.route-request-form__grid--request-line {
  grid-template-columns: 112px minmax(0, 1fr);
}

.route-request-form__headers {
  padding: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.route-request-form__headers > p {
  margin-block-end: var(--spacing-sm);
}

.route-request-form__header-row {
  align-items: end;
  gap: var(--spacing-xs);
  margin-block-end: var(--spacing-sm);
}

.route-request-form__header-row label {
  flex: 1 1 0;
}

.route-request-form__actions {
  flex-wrap: wrap;
  gap: var(--spacing-sm);
}

.route-request-form button {
  padding: var(--spacing-xs) var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.route-request-form__actions button:last-child {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.route-request-form button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.route-request-form button:active:not(:disabled) {
  transform: scale(0.95);
}

.route-request-form__warning {
  padding: var(--spacing-sm);
  border: 1px solid var(--color-state-warning-foreground);
  border-radius: var(--rounded-sm);
  background: var(--color-state-warning);
  color: var(--color-state-warning-foreground);
}

@media (max-width: 480px) {
  .route-request-form__grid,
  .route-request-form__header-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

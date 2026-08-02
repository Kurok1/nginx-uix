<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.3.0
-->
<template>
  <section
    class="structured-change-review"
    :aria-label="t('structured.reviewPanel.label')"
  >
    <header>
      <div>
        <h2>{{ t('structured.reviewPanel.title') }}</h2>
        <p>{{ t('structured.reviewPanel.description') }}</p>
      </div>
      <button
        v-if="closable"
        type="button"
        :aria-label="t('structured.reviewPanel.close')"
        @click="emit('close')"
      >
        {{ t('common.close') }}
      </button>
    </header>

    <p
      v-if="preview === null"
      class="structured-change-review__empty"
      role="status"
    >
      {{ t('structured.reviewPanel.empty') }}
    </p>

    <template v-else>
      <p
        v-if="!preview.complete"
        class="structured-change-review__blocking"
        role="alert"
      >
        <span aria-hidden="true">◇!</span>
        {{ t('structured.reviewPanel.incomplete') }}
      </p>
      <p
        v-if="errorMessage !== ''"
        class="structured-change-review__blocking"
        role="alert"
      >
        <span aria-hidden="true">◇!</span>
        {{ errorMessage }}
      </p>

      <ul class="structured-change-review__files">
        <li
          v-for="file in preview.changed_files"
          :key="file.path"
        >
          <strong>{{ file.path }}</strong>
          <span>{{ t('structured.reviewPanel.addedCount', { count: file.added_lines }) }}</span>
          <span>{{ t('structured.reviewPanel.removedCount', { count: file.removed_lines }) }}</span>
          <span>{{ abbreviate(file.before_digest) }} → {{ abbreviate(file.after_digest) }}</span>
        </li>
      </ul>

      <div
        v-if="preview.complete"
        class="structured-change-review__diff"
        role="region"
        :aria-label="t('structured.reviewPanel.diffLabel')"
        tabindex="0"
      >
        <div
          v-for="(line, index) in diffLines"
          :key="String(index) + line.content"
          class="structured-change-review__line"
          :class="'structured-change-review__line--' + line.kind"
        >
          <span
            class="structured-change-review__marker"
            aria-hidden="true"
          >{{ line.marker }}</span>
          <span class="structured-change-review__line-label">{{ line.label }}</span>
          <code>{{ line.content }}</code>
        </div>
      </div>

      <label
        v-if="confirmationTarget !== ''"
        class="structured-change-review__confirmation"
      >
        <span>{{ t('structured.reviewPanel.confirm', { target: confirmationTarget }) }}</span>
        <input
          type="text"
          autocomplete="off"
          :value="confirmation"
          :aria-label="t('structured.reviewPanel.confirmAria', { target: confirmationTarget })"
          @input="updateConfirmation"
        >
      </label>

      <div class="structured-change-review__actions">
        <button
          type="button"
          data-action="apply"
          :disabled="applyDisabled"
          :aria-describedby="applyDisabled ? applyReasonId : undefined"
          @click="emit('apply')"
        >
          {{ pending ? t('structured.reviewPanel.applying') : t('structured.reviewPanel.apply') }}
        </button>
      </div>
      <p
        :id="applyReasonId"
        class="structured-change-review__reason"
      >
        {{ applyReason }}
      </p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { StructuredChangePreview } from '../api/structured'

const { t } = useI18n()

type DiffLineKind = 'added' | 'removed' | 'context' | 'meta'

interface DiffLine {
  kind: DiffLineKind
  marker: string
  label: string
  content: string
}

const props = withDefaults(
  defineProps<{
    preview: StructuredChangePreview | null
    pending: boolean
    confirmation: string
    confirmationTarget: string
    errorMessage: string
    closable?: boolean
  }>(),
  { closable: false },
)
const emit = defineEmits<{
  apply: []
  close: []
  'update:confirmation': [value: string]
}>()
const applyReasonId = useId()
const diffLines = computed(() =>
  props.preview?.changed_files.flatMap((file) => parsePatch(file.patch)) ?? [],
)
const applyDisabled = computed(
  () =>
    props.preview === null ||
    !props.preview.complete ||
    props.pending ||
    props.errorMessage !== '' ||
    (props.confirmationTarget !== '' && props.confirmation !== props.confirmationTarget),
)
const applyReason = computed(() => {
  if (props.preview === null) return t('structured.reviewPanel.reasons.generate')
  if (!props.preview.complete) return t('structured.reviewPanel.reasons.incomplete')
  if (props.errorMessage !== '') return t('structured.reviewPanel.reasons.error')
  if (props.pending) return t('structured.reviewPanel.reasons.pending')
  if (props.confirmationTarget !== '' && props.confirmation !== props.confirmationTarget) {
    return t('structured.reviewPanel.reasons.confirmation')
  }
  return t('structured.reviewPanel.reasons.ready')
})

function updateConfirmation(event: Event): void {
  if (event.target instanceof HTMLInputElement) {
    emit('update:confirmation', event.target.value)
  }
}

function abbreviate(digest: string): string {
  return digest.slice(0, 8)
}

function parsePatch(patch: string): DiffLine[] {
  return patch
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => {
      if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('@@')) {
        return { kind: 'meta', marker: '·', label: t('structured.reviewPanel.diff.metadata'), content: line }
      }
      if (line.startsWith('+')) {
        return { kind: 'added', marker: '+', label: t('structured.reviewPanel.diff.added'), content: line.slice(1) }
      }
      if (line.startsWith('-')) {
        return { kind: 'removed', marker: '−', label: t('structured.reviewPanel.diff.removed'), content: line.slice(1) }
      }
      return {
        kind: 'context',
        marker: '·',
        label: t('structured.reviewPanel.diff.context'),
        content: line.startsWith(' ') ? line.slice(1) : line,
      }
    })
}
</script>

<style scoped>
.structured-change-review {
  display: grid;
  min-width: 0;
  gap: var(--spacing-md);
}

.structured-change-review header {
  display: flex;
  min-width: 0;
  align-items: start;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.structured-change-review h2,
.structured-change-review p {
  margin: 0;
}

.structured-change-review header p,
.structured-change-review__reason {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
}

.structured-change-review button,
.structured-change-review input {
  min-height: var(--component-control-min-size);
}

.structured-change-review button {
  padding-inline: var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.structured-change-review button[data-action='apply'] {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.structured-change-review button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.structured-change-review__blocking {
  display: flex;
  gap: var(--spacing-xs);
  padding: var(--spacing-sm);
  border: 1px solid var(--color-state-danger-foreground);
  border-radius: var(--rounded-sm);
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}

.structured-change-review__files {
  display: grid;
  margin: 0;
  padding: 0;
  gap: var(--spacing-xs);
  list-style: none;
}

.structured-change-review__files li {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: var(--spacing-xs) var(--spacing-sm);
  padding-block-end: var(--spacing-xs);
  border-bottom: 1px solid var(--color-hairline);
  font-size: var(--font-size-caption);
}

.structured-change-review__files strong {
  width: 100%;
  overflow-wrap: anywhere;
}

.structured-change-review__diff {
  max-height: var(--component-structured-diagnostic-max-height);
  overflow: auto;
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-diff-context);
  font-family: var(--font-code);
  font-size: var(--font-size-caption);
}

.structured-change-review__line {
  display: grid;
  min-width: max-content;
  grid-template-columns: 24px 72px minmax(0, 1fr);
  padding: var(--spacing-xxs) var(--spacing-xs);
}

.structured-change-review__line--added {
  background: var(--color-diff-added);
  color: var(--color-diff-added-foreground);
}

.structured-change-review__line--removed {
  background: var(--color-diff-removed);
  color: var(--color-diff-removed-foreground);
}

.structured-change-review__line-label {
  font-family: var(--font-ui);
  font-size: var(--font-size-nav);
}

.structured-change-review__confirmation {
  display: grid;
  gap: var(--spacing-xs);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
}

.structured-change-review__confirmation input {
  width: 100%;
  min-width: 0;
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
}

.structured-change-review__actions {
  display: flex;
  justify-content: flex-end;
}
</style>

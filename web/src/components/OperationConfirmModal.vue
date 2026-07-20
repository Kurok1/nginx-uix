<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.3
-->
<template>
  <div
    v-if="open"
    class="operation-modal__backdrop"
    @click.self="requestCancel"
  >
    <section
      ref="dialog"
      class="operation-modal"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="titleId"
      :aria-describedby="consequenceId"
      @keydown="handleKeydown"
    >
      <header>
        <h2 :id="titleId">
          {{ title }}
        </h2>
        <p :id="consequenceId">
          {{ consequence }}
        </p>
      </header>
      <div
        v-if="$slots.default"
        class="operation-modal__details"
      >
        <slot />
      </div>
      <form @submit.prevent="requestConfirm">
        <label
          v-if="requiresReason"
          :for="reasonId"
        >
          Reason
          <textarea
            :id="reasonId"
            v-model="reason"
            rows="3"
            maxlength="256"
            autocomplete="off"
          />
          <span>1–256 characters; leading or trailing spaces are not accepted.</span>
        </label>
        <label
          v-if="confirmationText !== ''"
          :for="confirmationId"
        >
          Type “{{ confirmationText }}” to confirm
          <input
            :id="confirmationId"
            v-model="confirmation"
            data-confirmation
            type="text"
            autocomplete="off"
            :aria-label="`Type ${confirmationText} exactly to confirm`"
          >
        </label>
        <div class="operation-modal__actions">
          <button
            ref="cancelButton"
            type="button"
            data-action="cancel"
            :disabled="pending"
            @click="requestCancel"
          >
            Cancel
          </button>
          <button
            type="submit"
            data-action="confirm"
            :disabled="!canConfirm || pending"
          >
            {{ pending ? 'Submitting…' : confirmLabel }}
          </button>
        </div>
      </form>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, toRef, useId, watch } from 'vue'

import { useFocusTrap } from '../composables/useFocusTrap'

interface Props {
  confirmationText: string
  confirmLabel: string
  consequence: string
  open: boolean
  pending?: boolean
  requiresReason?: boolean
  title: string
  trigger: HTMLElement | null
}

const props = withDefaults(defineProps<Props>(), {
  pending: false,
  requiresReason: true,
})
const emit = defineEmits<{
  cancel: []
  confirm: [reason: string, confirmation: string]
}>()

const reason = ref('')
const confirmation = ref('')
const dialog = ref<HTMLElement | null>(null)
const cancelButton = ref<HTMLButtonElement | null>(null)
const titleId = useId()
const consequenceId = useId()
const reasonId = useId()
const confirmationId = useId()
const trap = useFocusTrap(dialog, toRef(props, 'trigger'))
const reasonValid = computed(() =>
  !props.requiresReason || (
    reason.value.length >= 1 &&
    reason.value.length <= 256 &&
    reason.value.trim() === reason.value
  ),
)
const confirmationValid = computed(() =>
  props.confirmationText === '' || confirmation.value === props.confirmationText,
)
const canConfirm = computed(() => reasonValid.value && confirmationValid.value)

onMounted(() => {
  if (props.open) activate()
})

watch(
  () => props.open,
  (open) => {
    if (open) activate()
    else trap.deactivate()
  },
  { flush: 'post' },
)

function activate(): void {
  reason.value = ''
  confirmation.value = ''
  trap.activate()
  cancelButton.value?.focus()
}

function requestCancel(): void {
  emit('cancel')
}

function requestConfirm(): void {
  if (canConfirm.value) emit('confirm', reason.value, confirmation.value)
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    requestCancel()
    return
  }
  trap.onKeydown(event)
}
</script>

<style scoped>
.operation-modal__backdrop {
  position: fixed;
  z-index: var(--z-index-workspace-overlay);
  display: grid;
  overflow: auto;
  padding: var(--spacing-md);
  background: var(--color-workspace-backdrop);
  inset: 0;
  place-items: center;
}

.operation-modal {
  display: grid;
  width: var(--component-modal-width);
  max-width: 100%;
  min-width: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
  gap: var(--spacing-md);
}

.operation-modal h2,
.operation-modal p {
  margin-block-end: var(--spacing-xs);
  overflow-wrap: anywhere;
}

.operation-modal__details {
  min-width: 0;
  padding: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas-parchment);
  overflow-wrap: anywhere;
}

.operation-modal form,
.operation-modal label {
  display: grid;
  gap: var(--spacing-xs);
}

.operation-modal form {
  gap: var(--spacing-md);
}

.operation-modal label {
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
  letter-spacing: var(--letter-spacing-caption);
}

.operation-modal label span {
  color: var(--color-ink-muted-80);
  font-weight: var(--font-weight-regular);
}

.operation-modal input,
.operation-modal textarea,
.operation-modal button {
  min-height: var(--component-control-min-size);
}

.operation-modal input,
.operation-modal textarea {
  width: 100%;
  min-width: 0;
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.operation-modal textarea {
  resize: vertical;
}

.operation-modal__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--spacing-sm);
}

.operation-modal button {
  min-width: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.operation-modal button[data-action='confirm'] {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.operation-modal button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.operation-modal button:active:not(:disabled) {
  transform: scale(0.95);
}
</style>

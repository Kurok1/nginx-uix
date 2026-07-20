<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <div
    v-if="open"
    class="confirm-modal__backdrop"
    @click.self="requestCancel"
  >
    <section
      ref="dialog"
      class="confirm-modal"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="titleId"
      :aria-describedby="consequenceId"
      @keydown="handleKeydown"
    >
      <h2 :id="titleId">
        {{ title }}
      </h2>
      <p :id="consequenceId">
        {{ consequence }}
      </p>
      <form @submit.prevent="requestConfirm">
        <label :for="inputId">
          Type “{{ objectName }}” to confirm
        </label>
        <input
          :id="inputId"
          v-model="confirmation"
          type="text"
          autocomplete="off"
          :aria-label="`Type ${objectName} exactly to confirm`"
        >
        <div class="confirm-modal__actions">
          <button
            ref="cancelButton"
            type="button"
            data-action="cancel"
            @click="requestCancel"
          >
            Cancel
          </button>
          <button
            type="submit"
            data-action="confirm"
            :disabled="!canConfirm"
          >
            {{ confirmLabel }}
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
  confirmLabel?: string
  consequence: string
  objectName: string
  open: boolean
  title: string
  trigger: HTMLElement | null
}

const props = withDefaults(defineProps<Props>(), {
  confirmLabel: 'Delete',
})
const emit = defineEmits<{
  cancel: []
  confirm: [objectName: string]
}>()

const confirmation = ref('')
const dialog = ref<HTMLElement | null>(null)
const cancelButton = ref<HTMLButtonElement | null>(null)
const titleId = useId()
const consequenceId = useId()
const inputId = useId()
const canConfirm = computed(() => confirmation.value === props.objectName)
const trap = useFocusTrap(dialog, toRef(props, 'trigger'))

onMounted(() => {
  if (props.open) {
    trap.activate()
    cancelButton.value?.focus()
  }
})

watch(
  () => props.open,
  (open) => {
    if (open) {
      confirmation.value = ''
      trap.activate()
      cancelButton.value?.focus()
    } else {
      trap.deactivate()
    }
  },
  { flush: 'post' },
)

function requestCancel(): void {
  emit('cancel')
}

function requestConfirm(): void {
  if (canConfirm.value) {
    emit('confirm', props.objectName)
  }
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
.confirm-modal__backdrop {
  position: fixed;
  z-index: var(--z-index-workspace-overlay);
  display: grid;
  overflow: auto;
  padding: var(--spacing-md);
  background: var(--color-workspace-backdrop);
  inset: 0;
  place-items: center;
}

.confirm-modal {
  width: var(--component-modal-width);
  max-width: 100%;
  min-width: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.confirm-modal h2 {
  margin-block-end: var(--spacing-sm);
  overflow-wrap: anywhere;
}

.confirm-modal p {
  margin-block-end: var(--spacing-lg);
}

.confirm-modal form,
.confirm-modal label {
  display: grid;
  gap: var(--spacing-xs);
}

.confirm-modal label {
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
  letter-spacing: var(--letter-spacing-caption);
}

.confirm-modal input,
.confirm-modal button {
  min-height: var(--component-control-min-size);
}

.confirm-modal input {
  width: 100%;
  min-width: 0;
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.confirm-modal__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--spacing-sm);
  margin-block-start: var(--spacing-lg);
}

.confirm-modal button {
  min-width: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.confirm-modal button[data-action='confirm'] {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.confirm-modal button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.confirm-modal button:active:not(:disabled) {
  transform: scale(0.95);
}
</style>

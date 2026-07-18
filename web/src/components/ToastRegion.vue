<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <section
    class="toast-region"
    aria-live="polite"
    aria-atomic="false"
    aria-label="Success notifications"
  >
    <div
      v-for="toast in visibleToasts"
      :key="toast.id"
      class="toast-region__item"
    >
      <span
        aria-hidden="true"
        data-icon="success"
      >✓</span>
      <span>{{ toast.message }}</span>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

export interface ToastMessage {
  id: string
  message: string
}

const props = defineProps<{
  toasts: readonly ToastMessage[]
}>()
const emit = defineEmits<{
  dismiss: [id: string]
}>()

const dismissed = ref<ReadonlySet<string>>(new Set())
const timers = new Map<string, number>()
const visibleToasts = computed(() =>
  props.toasts.filter(({ id }) => !dismissed.value.has(id)).slice(-3),
)

watch(
  () => props.toasts.map(({ id }) => id),
  (ids) => {
    const currentIDs = new Set(ids)
    const nextDismissed = new Set(
      [...dismissed.value].filter((id) => currentIDs.has(id)),
    )
    for (const id of ids.slice(0, -3)) {
      if (!nextDismissed.has(id)) {
        nextDismissed.add(id)
        emit('dismiss', id)
      }
    }
    dismissed.value = nextDismissed
  },
  { immediate: true },
)

watch(
  visibleToasts,
  (toasts) => {
    const visibleIDs = new Set(toasts.map(({ id }) => id))
    for (const [id, timer] of timers) {
      if (!visibleIDs.has(id)) {
        window.clearTimeout(timer)
        timers.delete(id)
      }
    }
    for (const toast of toasts) {
      if (timers.has(toast.id)) {
        continue
      }
      timers.set(
        toast.id,
        window.setTimeout(() => {
          timers.delete(toast.id)
          dismissed.value = new Set([...dismissed.value, toast.id])
          emit('dismiss', toast.id)
        }, 5_000),
      )
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  for (const timer of timers.values()) {
    window.clearTimeout(timer)
  }
  timers.clear()
})
</script>

<style scoped>
.toast-region {
  position: fixed;
  z-index: var(--z-index-workspace-toast);
  display: grid;
  width: min(var(--component-drawer-width), calc(100vw - var(--spacing-xl)));
  gap: var(--spacing-xs);
  inset-block-end: var(--spacing-md);
  inset-inline-end: var(--spacing-md);
  pointer-events: none;
}

.toast-region__item {
  display: grid;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: var(--spacing-xs);
  padding: var(--spacing-sm) var(--spacing-md);
  border: 1px solid var(--color-state-success-foreground);
  border-radius: var(--rounded-sm);
  background: var(--color-state-success);
  color: var(--color-state-success-foreground);
  overflow-wrap: anywhere;
}
</style>

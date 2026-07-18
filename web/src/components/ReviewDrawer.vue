<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <div
    v-if="open"
    class="review-drawer__backdrop"
    @click.self="requestClose"
  >
    <aside
      ref="dialog"
      class="review-drawer"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="titleId"
      @keydown="handleKeydown"
    >
      <header class="review-drawer__header">
        <h2 :id="titleId">
          {{ title }}
        </h2>
        <button
          type="button"
          aria-label="Close review"
          @click="requestClose"
        >
          <span aria-hidden="true">×</span>
        </button>
      </header>
      <div class="review-drawer__content">
        <slot />
      </div>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, toRef, useId, watch } from 'vue'

import { useFocusTrap } from '../composables/useFocusTrap'

interface Props {
  open: boolean
  title: string
  trigger: HTMLElement | null
}

const props = defineProps<Props>()
const emit = defineEmits<{
  close: []
}>()

const dialog = ref<HTMLElement | null>(null)
const titleId = useId()
const trap = useFocusTrap(dialog, toRef(props, 'trigger'))

onMounted(() => {
  if (props.open) {
    trap.activate()
  }
})

watch(
  () => props.open,
  (open) => {
    if (open) {
      trap.activate()
    } else {
      trap.deactivate()
    }
  },
  { flush: 'post' },
)

function requestClose(): void {
  emit('close')
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    requestClose()
    return
  }
  trap.onKeydown(event)
}
</script>

<style scoped>
.review-drawer__backdrop {
  position: fixed;
  z-index: var(--z-index-workspace-overlay);
  display: flex;
  align-items: stretch;
  justify-content: flex-end;
  background: var(--color-workspace-backdrop);
  inset: 0;
}

.review-drawer {
  display: grid;
  width: var(--component-drawer-width);
  max-width: 100%;
  min-width: 0;
  grid-template-rows: auto minmax(0, 1fr);
  border-left: 1px solid var(--color-hairline);
  background: var(--color-canvas);
}

.review-drawer__header {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--spacing-sm);
  padding: var(--spacing-md) var(--spacing-lg);
  border-bottom: 1px solid var(--color-hairline);
}

.review-drawer__header h2 {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.review-drawer__header button {
  display: inline-grid;
  min-width: var(--component-control-min-size);
  min-height: var(--component-control-min-size);
  flex: none;
  place-items: center;
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.review-drawer__header button:active {
  transform: scale(0.95);
}

.review-drawer__content {
  min-width: 0;
  overflow: auto;
  padding: var(--spacing-lg);
}
</style>

<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.3
-->
<template>
  <div
    ref="tablist"
    class="operations-tabs"
    role="tablist"
    :aria-label="t('operations.tabs.label')"
  >
    <button
      v-for="tab in tabs"
      :id="`operations-tab-${tab.id}`"
      :key="tab.id"
      type="button"
      role="tab"
      :aria-controls="`operations-panel-${tab.id}`"
      :aria-selected="active === tab.id"
      :tabindex="active === tab.id ? 0 : -1"
      @click="$emit('select', tab.id)"
      @keydown="handleKeydown($event, tab.id)"
    >
      {{ tab.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

export type OperationsTab = 'overview' | 'backups' | 'history' | 'audit'

defineProps<{
  active: OperationsTab
}>()

const emit = defineEmits<{
  select: [tab: OperationsTab]
}>()

const { t } = useI18n()

const tabs = computed<ReadonlyArray<{ id: OperationsTab; label: string }>>(() => [
  { id: 'overview', label: t('operations.tabs.overview') },
  { id: 'backups', label: t('operations.tabs.backups') },
  { id: 'history', label: t('operations.tabs.history') },
  { id: 'audit', label: t('operations.tabs.audit') },
])
const tablist = ref<HTMLElement | null>(null)

function handleKeydown(event: KeyboardEvent, current: OperationsTab): void {
  const index = tabs.value.findIndex(({ id }) => id === current)
  let next: number
  switch (event.key) {
    case 'ArrowRight':
    case 'ArrowDown':
      next = (index + 1) % tabs.value.length
      break
    case 'ArrowLeft':
    case 'ArrowUp':
      next = (index - 1 + tabs.value.length) % tabs.value.length
      break
    case 'Home':
      next = 0
      break
    case 'End':
      next = tabs.value.length - 1
      break
    default:
      return
  }
  event.preventDefault()
  const selected = tabs.value[next]
  if (selected === undefined) return
  emit('select', selected.id)
  const buttons = tablist.value?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
  buttons?.item(next).focus()
}
</script>

<style scoped>
.operations-tabs {
  display: flex;
  min-width: 0;
  overflow-x: auto;
  border-bottom: 1px solid var(--color-hairline);
  gap: var(--spacing-xxs);
  scrollbar-gutter: stable;
}

.operations-tabs button {
  position: relative;
  min-width: max-content;
  min-height: var(--component-control-min-size);
  padding-inline: var(--spacing-md);
  border: 0;
  background: transparent;
  color: var(--color-ink-muted-80);
  cursor: pointer;
  font-size: var(--font-size-caption);
}

.operations-tabs button[aria-selected='true'] {
  color: var(--color-primary);
  font-weight: var(--font-weight-semibold);
}

.operations-tabs button[aria-selected='true']::after {
  position: absolute;
  height: 2px;
  background: var(--color-primary);
  content: '';
  inset-inline: var(--spacing-sm);
  inset-block-end: 0;
}
</style>

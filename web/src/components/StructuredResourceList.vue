<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.3.0
-->
<template>
  <section class="structured-resource-list">
    <h2>{{ label }}</h2>
    <p
      v-if="resources.length === 0"
      role="status"
    >
      {{ t('structured.resources.empty') }}
    </p>
    <ul
      v-else
      ref="list"
      role="listbox"
      :aria-label="label"
      :aria-activedescendant="selectedId === null ? undefined : optionId(selectedId)"
    >
      <li
        v-for="(resource, index) in resources"
        :key="resource.id"
        role="none"
      >
        <button
          :id="optionId(resource.id)"
          type="button"
          role="option"
          :aria-selected="resource.id === selectedId"
          :tabindex="resource.id === selectedId || (selectedId === null && index === 0) ? 0 : -1"
          @click="emit('select', resource.id)"
          @keydown="handleKeydown($event, index)"
        >
          <span class="structured-resource-list__identity">
            <strong>{{ resource.label }}</strong>
            <span>{{ resource.meta }}</span>
          </span>
          <span
            class="structured-resource-list__state"
            :class="{ 'structured-resource-list__state--problem': resource.problem }"
          >
            <span aria-hidden="true">{{ resource.problem ? '◇!' : resource.editable ? '●' : '◇' }}</span>
            {{ resource.problem ? t('structured.resources.attention') : resource.editable ? t('structured.resources.editable') : t('structured.resources.readOnly') }}
          </span>
        </button>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

export interface StructuredResourceItem {
  id: string
  label: string
  meta: string
  editable: boolean
  problem: boolean
}

const props = defineProps<{
  label: string
  resources: readonly StructuredResourceItem[]
  selectedId: string | null
}>()
const emit = defineEmits<{
  select: [id: string]
}>()
const list = ref<HTMLElement | null>(null)

function optionId(id: string): string {
  return 'structured-resource-' + id
}

function handleKeydown(event: KeyboardEvent, index: number): void {
  let nextIndex = index
  switch (event.key) {
    case 'ArrowDown':
      nextIndex = Math.min(props.resources.length - 1, index + 1)
      break
    case 'ArrowUp':
      nextIndex = Math.max(0, index - 1)
      break
    case 'Home':
      nextIndex = 0
      break
    case 'End':
      nextIndex = props.resources.length - 1
      break
    default:
      return
  }
  event.preventDefault()
  const resource = props.resources[nextIndex]
  if (resource === undefined) return
  emit('select', resource.id)
  void nextTick(() => {
    list.value
      ?.querySelectorAll<HTMLElement>('[role="option"]')
      .item(nextIndex)
      .focus()
  })
}
</script>

<style scoped>
.structured-resource-list {
  min-width: 0;
}

.structured-resource-list h2 {
  margin-block-end: var(--spacing-sm);
  font-size: var(--font-size-tagline);
}

.structured-resource-list ul {
  display: grid;
  margin: 0;
  padding: 0;
  gap: var(--spacing-xxs);
  list-style: none;
}

.structured-resource-list button {
  display: flex;
  width: 100%;
  min-width: 0;
  min-height: var(--component-control-min-size);
  padding: var(--spacing-sm);
  align-items: center;
  justify-content: space-between;
  gap: var(--spacing-sm);
  border: 1px solid transparent;
  border-radius: var(--rounded-sm);
  background: transparent;
  color: var(--color-ink);
  text-align: start;
  cursor: pointer;
}

.structured-resource-list button[aria-selected='true'] {
  border-color: var(--color-primary);
  background: var(--color-state-info);
}

.structured-resource-list__identity {
  display: grid;
  min-width: 0;
  gap: var(--spacing-xxs);
}

.structured-resource-list__identity strong,
.structured-resource-list__identity span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.structured-resource-list__identity span,
.structured-resource-list__state {
  font-size: var(--font-size-caption);
}

.structured-resource-list__state {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: var(--spacing-xxs);
  color: var(--color-state-info-foreground);
}

.structured-resource-list__state--problem {
  color: var(--color-state-danger-foreground);
}
</style>

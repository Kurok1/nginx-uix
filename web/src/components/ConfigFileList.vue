<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <div class="config-file-list">
    <nav
      class="config-file-list__desktop"
      aria-label="生效配置加载顺序"
    >
      <ol>
        <li
          v-for="occurrence in occurrences"
          :key="occurrence.id"
        >
          <button
            type="button"
            :data-id="occurrence.id"
            :aria-current="occurrence.id === selectedId ? 'true' : undefined"
            @click="emit('select', occurrence.id)"
            @keydown="selectWithKeyboard($event, occurrence.id)"
          >
            <span class="config-file-list__order">第 {{ occurrence.load_order }} 项</span>
            <span class="config-file-list__path">{{ occurrence.path }}</span>
          </button>
        </li>
      </ol>
    </nav>

    <div class="config-file-list__selector">
      <label :for="selectorId">配置加载项</label>
      <select
        :id="selectorId"
        :value="selectedId"
        @change="selectFromPicker"
      >
        <option
          v-for="occurrence in occurrences"
          :key="occurrence.id"
          :value="occurrence.id"
        >
          第 {{ occurrence.load_order }} 项 · {{ occurrence.path }}
        </option>
      </select>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useId } from 'vue'

import type { EffectiveConfigOccurrence } from '../api/types'

defineProps<{
  occurrences: readonly EffectiveConfigOccurrence[]
  selectedId: string
}>()

const emit = defineEmits<{
  select: [id: string]
}>()

const selectorId = useId()

function selectWithKeyboard(event: KeyboardEvent, id: string): void {
  if (event.key !== 'Enter' && event.key !== ' ') {
    return
  }
  event.preventDefault()
  emit('select', id)
}

function selectFromPicker(event: Event): void {
  if (!(event.target instanceof HTMLSelectElement)) {
    return
  }
  emit('select', event.target.value)
}
</script>

<style scoped>
.config-file-list,
.config-file-list__desktop,
.config-file-list__selector,
.config-file-list li,
.config-file-list button,
.config-file-list__path {
  min-width: 0;
}

.config-file-list__desktop {
  padding: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.config-file-list ol {
  display: grid;
  gap: var(--spacing-xs);
  margin: 0;
  padding: 0;
  list-style: none;
}

.config-file-list button {
  width: 100%;
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid transparent;
  border-radius: var(--rounded-sm);
  background: transparent;
  color: var(--color-ink);
  cursor: pointer;
  text-align: start;
}

.config-file-list button:active {
  transform: scale(0.95);
}

.config-file-list button[aria-current='true'] {
  border-color: var(--color-primary);
  background: var(--color-canvas-parchment);
}

.config-file-list__order,
.config-file-list__path {
  display: block;
}

.config-file-list__order {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
}

.config-file-list__path {
  margin-block-start: var(--spacing-xxs);
  overflow-wrap: anywhere;
}

.config-file-list__selector {
  display: none;
  gap: var(--spacing-xs);
}

.config-file-list__selector label {
  font-weight: var(--font-weight-semibold);
}

.config-file-list__selector select {
  width: 100%;
  min-height: var(--component-control-min-size);
  padding-inline: var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

@media (max-width: 833px) {
  .config-file-list__desktop {
    display: none;
  }

  .config-file-list__selector {
    display: grid;
  }
}
</style>

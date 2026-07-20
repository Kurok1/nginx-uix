<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.3.0
-->
<template>
  <section class="location-tree">
    <h2>Locations</h2>
    <p
      v-if="locations.length === 0"
      role="status"
    >
      This server has no location blocks.
    </p>
    <ul
      v-else
      ref="tree"
      role="tree"
      aria-label="Location rules"
    >
      <li
        v-for="(row, index) in visibleRows"
        :key="row.location.id"
        role="none"
      >
        <button
          type="button"
          role="treeitem"
          :aria-label="accessibleName(row.location)"
          :aria-level="row.level"
          :aria-expanded="row.location.children.length === 0 ? undefined : expanded.has(row.location.id)"
          :aria-selected="row.location.id === selectedId"
          :tabindex="row.location.id === selectedId || (selectedId === null && index === 0) ? 0 : -1"
          :style="levelStyle(row.level)"
          @click="emit('select', row.location.id)"
          @keydown="handleKeydown($event, index)"
        >
          <span
            class="location-tree__disclosure"
            aria-hidden="true"
          >{{ disclosure(row.location) }}</span>
          <span class="location-tree__matcher">
            <strong>{{ matcherLabel(row.location.type) }}</strong>
            <span>{{ row.location.matcher }}</span>
          </span>
          <span class="location-tree__state">
            {{ row.location.editable ? 'Editable' : 'Read only' }}
          </span>
        </button>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'

import type { StructuredLocation, StructuredMatcherType } from '../api/structured'

interface LocationRow {
  location: StructuredLocation
  level: number
  parentId: string | null
}

const props = defineProps<{
  locations: readonly StructuredLocation[]
  selectedId: string | null
}>()
const emit = defineEmits<{
  select: [id: string]
}>()
const tree = ref<HTMLElement | null>(null)
const expanded = ref(new Set(collectParentIDs(props.locations)))
const visibleRows = computed(() => flatten(props.locations, expanded.value))

function collectParentIDs(locations: readonly StructuredLocation[]): string[] {
  return locations.flatMap((location) => [
    ...(location.children.length === 0 ? [] : [location.id]),
    ...collectParentIDs(location.children),
  ])
}

function flatten(
  locations: readonly StructuredLocation[],
  opened: ReadonlySet<string>,
  level = 1,
  parentId: string | null = null,
): LocationRow[] {
  const rows: LocationRow[] = []
  for (const location of locations) {
    rows.push({ location, level, parentId })
    if (location.children.length > 0 && opened.has(location.id)) {
      rows.push(...flatten(location.children, opened, level + 1, location.id))
    }
  }
  return rows
}

function matcherLabel(type: StructuredMatcherType): string {
  const labels: Record<StructuredMatcherType, string> = {
    unknown: 'Raw-only',
    exact: 'Exact',
    prefix: 'Prefix',
    prefix_priority: 'Priority prefix',
    regex: 'Regex',
    regex_insensitive: 'Case-insensitive regex',
    named: 'Named',
  }
  return labels[type]
}

function accessibleName(location: StructuredLocation): string {
  return matcherLabel(location.type) + ' location ' + location.matcher
}

function disclosure(location: StructuredLocation): string {
  if (location.children.length === 0) return '·'
  return expanded.value.has(location.id) ? '▾' : '▸'
}

function levelStyle(level: number): Record<string, string> {
  return { '--location-tree-level': String(level - 1) }
}

function setExpanded(id: string, open: boolean): void {
  const next = new Set(expanded.value)
  if (open) next.add(id)
  else next.delete(id)
  expanded.value = next
}

function handleKeydown(event: KeyboardEvent, index: number): void {
  const row = visibleRows.value[index]
  if (row === undefined) return
  let nextIndex: number | null = null
  switch (event.key) {
    case 'ArrowDown':
      nextIndex = Math.min(visibleRows.value.length - 1, index + 1)
      break
    case 'ArrowUp':
      nextIndex = Math.max(0, index - 1)
      break
    case 'Home':
      nextIndex = 0
      break
    case 'End':
      nextIndex = visibleRows.value.length - 1
      break
    case 'ArrowRight':
      if (row.location.children.length > 0 && !expanded.value.has(row.location.id)) {
        setExpanded(row.location.id, true)
      } else if (row.location.children.length > 0) {
        nextIndex = index + 1
      }
      break
    case 'ArrowLeft':
      if (expanded.value.has(row.location.id) && row.location.children.length > 0) {
        setExpanded(row.location.id, false)
      } else if (row.parentId !== null) {
        nextIndex = visibleRows.value.findIndex((candidate) => candidate.location.id === row.parentId)
      }
      break
    default:
      return
  }
  event.preventDefault()
  if (nextIndex === null || nextIndex < 0) return
  const target = visibleRows.value[nextIndex]
  if (target === undefined) return
  emit('select', target.location.id)
  void nextTick(() => {
    tree.value
      ?.querySelectorAll<HTMLElement>('[role="treeitem"]')
      .item(nextIndex)
      .focus()
  })
}
</script>

<style scoped>
.location-tree {
  min-width: 0;
}

.location-tree h2 {
  margin-block-end: var(--spacing-sm);
  font-size: var(--font-size-tagline);
}

.location-tree ul {
  display: grid;
  margin: 0;
  padding: 0;
  gap: var(--spacing-xxs);
  list-style: none;
}

.location-tree button {
  display: grid;
  width: 100%;
  min-width: 0;
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-sm);
  padding-inline-start: calc(
    var(--spacing-sm) + var(--location-tree-level) * var(--component-structured-tree-indent)
  );
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--spacing-xs);
  border: 1px solid transparent;
  border-radius: var(--rounded-sm);
  background: transparent;
  color: var(--color-ink);
  text-align: start;
  cursor: pointer;
}

.location-tree button[aria-selected='true'] {
  border-color: var(--color-primary);
  background: var(--color-state-info);
}

.location-tree__disclosure {
  width: var(--spacing-md);
}

.location-tree__matcher {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
}

.location-tree__matcher span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.location-tree__state {
  color: var(--color-state-info-foreground);
  font-size: var(--font-size-caption);
}
</style>

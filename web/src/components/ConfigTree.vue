<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <section
    class="config-tree"
    :aria-label="t('workspace.tree.label')"
  >
    <div
      class="config-tree__mode"
      role="group"
      :aria-label="t('workspace.tree.view')"
    >
      <button
        type="button"
        :aria-label="t('workspace.tree.showFiles')"
        :aria-pressed="mode === 'physical'"
        @click="setMode('physical')"
      >
        {{ t('workspace.tree.files') }}
      </button>
      <button
        type="button"
        :aria-label="t('workspace.tree.showGroups')"
        :aria-pressed="mode === 'groups'"
        @click="setMode('groups')"
      >
        {{ t('workspace.tree.groups') }}
      </button>
    </div>

    <div class="config-tree__actions">
      <template v-if="mode === 'physical'">
        <button
          type="button"
          :aria-label="t('workspace.tree.createFile')"
          :disabled="readOnly"
          @click="emit('create')"
        >
          {{ t('workspace.tree.newFile') }}
        </button>
        <template v-if="selectedWritableNode !== null">
          <button
            type="button"
            :aria-label="t('workspace.tree.copySelected')"
            @click="emit('copy', selectedWritableNode.path)"
          >
            {{ t('common.copy') }}
          </button>
          <button
            type="button"
            :aria-label="t('workspace.tree.renameSelected')"
            @click="emit('rename', selectedWritableNode.path)"
          >
            {{ t('common.rename') }}
          </button>
          <button
            type="button"
            :aria-label="t('workspace.tree.deleteSelected')"
            @click="emit('delete', selectedWritableNode.path)"
          >
            {{ t('common.delete') }}
          </button>
        </template>
      </template>
      <template v-else>
        <button
          type="button"
          :aria-label="t('workspace.tree.createGroup')"
          :disabled="readOnly"
          @click="emit('create-group')"
        >
          {{ t('workspace.tree.newGroup') }}
        </button>
        <template v-if="selectedGroup !== null && !readOnly">
          <button
            type="button"
            :aria-label="t('workspace.tree.editSelectedGroup')"
            @click="emit('replace-group', selectedGroup)"
          >
            {{ t('workspace.tree.editGroup') }}
          </button>
          <button
            type="button"
            :aria-label="t('workspace.tree.deleteSelectedGroup')"
            @click="emit('delete-group', selectedGroup)"
          >
            {{ t('workspace.tree.deleteGroup') }}
          </button>
        </template>
      </template>
    </div>

    <ul
      class="config-tree__items"
      role="tree"
      :aria-label="mode === 'physical' ? t('workspace.tree.physicalFiles') : t('workspace.tree.logicalGroups')"
      @keydown="handleKeydown"
    >
      <li
        v-for="row in visibleRows"
        :key="row.key"
        :ref="(element) => setRowElement(row.key, element)"
        role="treeitem"
        :tabindex="activeKey === row.key ? 0 : -1"
        :aria-level="row.level"
        :aria-expanded="row.container ? isExpanded(row.key) : undefined"
        :aria-selected="rowSelectable(row) ? row.path === selectedPath : undefined"
        :aria-label="rowAccessibleName(row)"
        :data-key="row.key"
        :data-path="row.path"
        :data-group-id="row.group?.id"
        :style="{ '--tree-level': row.level }"
        @focus="activeKey = row.key"
        @click="activateRow(row)"
      >
        <span
          class="config-tree__disclosure"
          aria-hidden="true"
        >{{ row.container ? (isExpanded(row.key) ? '▾' : '▸') : '·' }}</span>
        <span
          class="config-tree__type-icon"
          data-state-icon
          aria-hidden="true"
        >{{ rowIcon(row) }}</span>
        <span class="config-tree__label">{{ row.label }}</span>
        <span
          v-if="rowStatus(row) !== ''"
          class="config-tree__state"
        >{{ rowStatus(row) }}</span>
        <span
          v-if="row.diffStatus !== undefined"
          class="config-tree__diff"
          :class="`config-tree__diff--${row.diffStatus}`"
        >
          <span
            data-diff-icon
            aria-hidden="true"
          >{{ diffIcon(row.diffStatus) }}</span>
          {{ diffLabel(row.diffStatus) }}
        </span>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ConfigGroup, ConfigTreeNode, DiffStatus } from '../api/types'

type TreeMode = 'groups' | 'physical'

interface TreeRow {
  key: string
  path: string
  label: string
  level: number
  parentKey?: string
  container: boolean
  node?: ConfigTreeNode
  group?: ConfigGroup
  groupMember?: boolean
  missing?: boolean
  diffStatus?: DiffStatus
}

const props = withDefaults(
  defineProps<{
    groups?: readonly ConfigGroup[]
    nodes: readonly ConfigTreeNode[]
    readOnly?: boolean
    selectedPath: string | null
  }>(),
  {
    groups: () => [],
    readOnly: false,
  },
)
const emit = defineEmits<{
  copy: [path: string]
  create: []
  'create-group': []
  delete: [path: string]
  'delete-group': [group: ConfigGroup]
  rename: [path: string]
  'replace-group': [group: ConfigGroup]
  select: [path: string]
}>()

const mode = ref<TreeMode>('physical')
const { t } = useI18n()
const expanded = reactive(new Set<string>())
const rowElements = new Map<string, HTMLElement>()

const physicalRows = computed(() => buildPhysicalRows(props.nodes, expanded))
const groupRows = computed(() => buildGroupRows(props.groups, expanded))
const visibleRows = computed(() =>
  mode.value === 'physical' ? physicalRows.value : groupRows.value,
)
const activeKey = ref(props.selectedPath ?? physicalRows.value[0]?.key ?? '')

const selectedWritableNode = computed(() => {
  if (props.readOnly || props.selectedPath === null) {
    return null
  }
  const node = props.nodes.find(({ path }) => path === props.selectedPath)
  return node?.entry_type === 'regular' && node.managed && !node.read_only ? node : null
})
const selectedGroup = computed(() => {
  if (mode.value !== 'groups') {
    return null
  }
  return visibleRows.value.find(({ key }) => key === activeKey.value)?.group ?? null
})

watch(
  () => props.selectedPath,
  (path) => {
    if (path !== null && visibleRows.value.some((row) => row.key === path)) {
      activeKey.value = path
    }
  },
)

watch(visibleRows, (rows) => {
  if (!rows.some(({ key }) => key === activeKey.value)) {
    activeKey.value = rows[0]?.key ?? ''
  }
})

function setMode(nextMode: TreeMode): void {
  mode.value = nextMode
  activeKey.value = nextMode === 'physical' ? props.selectedPath ?? '' : ''
  void nextTick(() => {
    if (!visibleRows.value.some(({ key }) => key === activeKey.value)) {
      activeKey.value = visibleRows.value[0]?.key ?? ''
    }
  })
}

function setRowElement(key: string, element: unknown): void {
  if (element instanceof HTMLElement) {
    rowElements.set(key, element)
  } else {
    rowElements.delete(key)
  }
}

function isExpanded(key: string): boolean {
  return expanded.has(key)
}

function toggle(row: TreeRow, force?: boolean): void {
  if (!row.container) {
    return
  }
  const shouldExpand = force ?? !expanded.has(row.key)
  if (shouldExpand) {
    expanded.add(row.key)
  } else {
    expanded.delete(row.key)
  }
}

function activateRow(row: TreeRow): void {
  activeKey.value = row.key
  rowElements.get(row.key)?.focus()
  if (row.container) {
    toggle(row)
  } else if (rowSelectable(row)) {
    emit('select', row.path)
  }
}

function rowSelectable(row: TreeRow): boolean {
  if (row.missing) {
    return false
  }
  if (row.groupMember) {
    return true
  }
  return row.node?.entry_type === 'regular'
}

function handleKeydown(event: KeyboardEvent): void {
  const target = event.target instanceof HTMLElement
    ? event.target.closest<HTMLElement>('[role="treeitem"]')
    : null
  const currentKey = target?.dataset.key ?? activeKey.value
  const currentIndex = visibleRows.value.findIndex(({ key }) => key === currentKey)
  const current = visibleRows.value[currentIndex]

  switch (event.key) {
    case 'Home':
      event.preventDefault()
      focusRow(0)
      break
    case 'End':
      event.preventDefault()
      focusRow(visibleRows.value.length - 1)
      break
    case 'ArrowDown':
      event.preventDefault()
      focusRow(Math.min(currentIndex + 1, visibleRows.value.length - 1))
      break
    case 'ArrowUp':
      event.preventDefault()
      focusRow(Math.max(currentIndex - 1, 0))
      break
    case 'ArrowRight':
      if (current === undefined) return
      event.preventDefault()
      if (current.container && !expanded.has(current.key)) {
        toggle(current, true)
      } else {
        const childIndex = visibleRows.value.findIndex(
          ({ parentKey }) => parentKey === current.key,
        )
        if (childIndex >= 0) focusRow(childIndex)
      }
      break
    case 'ArrowLeft':
      if (current === undefined) return
      event.preventDefault()
      if (current.container && expanded.has(current.key)) {
        toggle(current, false)
      } else if (current.parentKey !== undefined) {
        const parentIndex = visibleRows.value.findIndex(
          ({ key }) => key === current.parentKey,
        )
        if (parentIndex >= 0) focusRow(parentIndex)
      }
      break
    case 'Enter':
    case ' ':
      if (current === undefined) return
      event.preventDefault()
      activateRow(current)
      break
  }
}

function focusRow(index: number): void {
  const row = visibleRows.value[index]
  if (row === undefined) {
    return
  }
  activeKey.value = row.key
  void nextTick(() => rowElements.get(row.key)?.focus())
}

function rowAccessibleName(row: TreeRow): string {
  return [row.label, rowStatus(row), row.diffStatus === undefined ? '' : diffLabel(row.diffStatus)]
    .filter((part) => part !== '')
    .join(', ')
}

function rowStatus(row: TreeRow): string {
  if (row.missing) return t('workspace.tree.missing')
  if (row.group !== undefined) {
    return t(
      'workspace.tree.members',
      { count: row.group.members.length },
      row.group.members.length,
    )
  }
  const reason = row.node?.status_reason_code
  const labels: Partial<Record<ConfigTreeNode['status_reason_code'], string>> = {
    directory: t('workspace.tree.directory'),
    sensitive_material: t('workspace.tree.sensitive'),
    not_candidate: t('workspace.tree.notCandidate'),
    invalid_text: t('workspace.tree.invalidText'),
    file_limit: t('workspace.tree.fileLimit'),
    symlink_external: t('workspace.tree.externalSymlink'),
    symlink_internal: t('workspace.tree.internalSymlink'),
    symlink_unavailable: t('workspace.tree.unavailableSymlink'),
    special: t('workspace.tree.special'),
  }
  if (reason !== undefined && labels[reason] !== undefined) return labels[reason] ?? ''
  return row.node?.read_only ? t('workspace.tree.readOnly') : ''
}

function rowIcon(row: TreeRow): string {
  if (row.missing) return '◇!'
  if (row.group !== undefined) return '▦'
  switch (row.node?.entry_type) {
    case 'directory': return '▣'
    case 'symlink': return '↗'
    case 'special': return '◇'
    default: return '▤'
  }
}

function diffIcon(status: DiffStatus): string {
  return status === 'created' ? '+' : status === 'deleted' ? '−' : status === 'modified' ? '±' : '·'
}

function diffLabel(status: DiffStatus): string {
  const labels: Record<DiffStatus, string> = {
    created: t('workspace.tree.created'),
    modified: t('workspace.tree.modified'),
    deleted: t('workspace.tree.deleted'),
    unchanged: t('workspace.tree.unchanged'),
  }
  return `${diffIcon(status)} ${labels[status]}`
}

function buildPhysicalRows(
  nodes: readonly ConfigTreeNode[],
  expandedKeys: ReadonlySet<string>,
): TreeRow[] {
  const byPath = new Map(nodes.map((node) => [node.path, node]))
  const children = new Map<string | null, ConfigTreeNode[]>()
  for (const node of nodes) {
    const separator = node.path.lastIndexOf('/')
    const candidateParent = separator < 0 ? null : node.path.slice(0, separator)
    const parent = candidateParent !== null && byPath.has(candidateParent)
      ? candidateParent
      : null
    const siblings = children.get(parent) ?? []
    siblings.push(node)
    children.set(parent, siblings)
  }
  for (const siblings of children.values()) {
    siblings.sort((left, right) => left.path.localeCompare(right.path))
  }

  const rows: TreeRow[] = []
  const walk = (parent: string | null, level: number): void => {
    for (const node of children.get(parent) ?? []) {
      const hasChildren = (children.get(node.path)?.length ?? 0) > 0
      rows.push({
        key: node.path,
        path: node.path,
        label: node.name,
        level,
        ...(parent === null ? {} : { parentKey: parent }),
        container: node.entry_type === 'directory' && hasChildren,
        node,
        ...(node.diff_status === undefined ? {} : { diffStatus: node.diff_status }),
      })
      if (node.entry_type === 'directory' && expandedKeys.has(node.path)) {
        walk(node.path, level + 1)
      }
    }
  }
  walk(null, 1)
  return rows
}

function buildGroupRows(
  groups: readonly ConfigGroup[],
  expandedKeys: ReadonlySet<string>,
): TreeRow[] {
  const rows: TreeRow[] = []
  const ordered = [...groups].sort(
    (left, right) => left.sort_order - right.sort_order || left.name.localeCompare(right.name),
  )
  for (const group of ordered) {
    const key = `group:${group.id}`
    rows.push({
      key,
      path: key,
      label: group.name,
      level: 1,
      container: group.members.length > 0,
      group,
    })
    if (!expandedKeys.has(key)) continue
    const missing = new Set(group.missing)
    for (const path of group.members) {
      rows.push({
        key: `${key}:${path}`,
        path,
        label: path,
        level: 2,
        parentKey: key,
        container: false,
        groupMember: true,
        missing: missing.has(path),
      })
    }
  }
  return rows
}
</script>

<style scoped>
.config-tree {
  display: grid;
  min-width: 0;
  align-content: start;
  gap: var(--spacing-sm);
}

.config-tree__mode,
.config-tree__actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
}

.config-tree button {
  min-width: var(--component-control-min-size);
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.config-tree button[aria-pressed='true'] {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.config-tree button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.config-tree button:active:not(:disabled) {
  transform: scale(0.95);
}

.config-tree__items {
  display: grid;
  min-width: 0;
  margin: 0;
  padding: 0;
  list-style: none;
}

.config-tree__items [role='treeitem'] {
  display: grid;
  min-width: var(--component-control-min-size);
  min-height: var(--component-control-min-size);
  grid-template-columns: auto auto minmax(0, 1fr);
  align-items: center;
  gap: var(--spacing-xxs);
  padding-block: var(--spacing-xxs);
  padding-inline: calc((var(--tree-level) - 1) * var(--spacing-md)) var(--spacing-xs);
  border-radius: var(--rounded-sm);
  cursor: default;
}

.config-tree__items [role='treeitem'][aria-selected='true'] {
  color: var(--color-primary);
  font-weight: var(--font-weight-semibold);
}

.config-tree__disclosure,
.config-tree__type-icon {
  width: var(--spacing-md);
  text-align: center;
}

.config-tree__label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.config-tree__state,
.config-tree__diff {
  grid-column: 3;
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
  overflow-wrap: anywhere;
}

.config-tree__diff--created {
  background: var(--color-diff-added);
  color: var(--color-diff-added-foreground);
}

.config-tree__diff--deleted {
  background: var(--color-diff-removed);
  color: var(--color-diff-removed-foreground);
}

.config-tree__diff--modified,
.config-tree__diff--unchanged {
  background: var(--color-diff-context);
  color: var(--color-diff-context-foreground);
}
</style>

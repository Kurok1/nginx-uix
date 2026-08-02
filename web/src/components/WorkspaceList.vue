<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <section
    class="workspace-list"
    aria-labelledby="workspace-list-title"
  >
    <header class="workspace-list__header">
      <h2 id="workspace-list-title">
        {{ t('workspace.listTitle') }}
      </h2>
      <button
        type="button"
        :aria-label="t('workspace.createLabel')"
        :disabled="pendingAction !== null"
        @click="showCreateForm = true"
      >
        {{ t('workspace.newWorkspace') }}
      </button>
    </header>

    <p
      v-if="pendingAction?.kind === 'create_workspace'"
      class="workspace-list__pending"
      role="status"
    >
      <span aria-hidden="true">◌</span>
      {{ t('workspace.creating') }}
    </p>

    <form
      v-if="showCreateForm"
      class="workspace-list__create"
      @submit.prevent="submitCreate"
    >
      <label for="workspace-list-name">{{ t('workspace.name') }}</label>
      <input
        id="workspace-list-name"
        v-model="workspaceName"
        name="workspace-name"
        :aria-label="t('workspace.name')"
        autocomplete="off"
      >
      <div>
        <button
          type="button"
          :aria-label="t('workspace.cancelCreation')"
          @click="cancelCreate"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          :disabled="workspaceName === '' || pendingAction !== null"
        >
          {{ t('common.create') }}
        </button>
      </div>
    </form>

    <p
      v-if="workspaces.length === 0"
      class="workspace-list__empty"
    >
      {{ t('workspace.noWorkspaces') }}
    </p>
    <ul v-else>
      <li
        v-for="workspace in workspaces"
        :key="workspace.id"
        :data-workspace-id="workspace.id"
      >
        <a
          href=""
          :aria-current="workspace.id === selectedId ? 'page' : undefined"
          @click.prevent="emit('select', workspace.id)"
        >
          <span>{{ workspace.name }}</span>
          <span
            v-if="workspace.state === 'preparing'"
            class="workspace-list__state"
          >
            <span
              data-state-icon
              aria-hidden="true"
            >◌</span>
            {{ t('workspace.states.preparing') }}
          </span>
          <span
            v-else
            class="workspace-list__state"
          >
            <span
              data-state-icon
              aria-hidden="true"
            >{{ stateIcon(workspace.state) }}</span>
            {{ stateLabel(workspace.state) }}
          </span>
        </a>
        <button
          type="button"
          :aria-label="t('workspace.deleteWorkspace', { name: workspace.name })"
          @click="emit('request-delete', workspace)"
        >
          {{ t('common.delete') }}
        </button>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { WorkspaceState, WorkspaceSummary } from '../api/types'
import type { WorkspacePendingAction } from '../workspace'

defineProps<{
  pendingAction: WorkspacePendingAction | null
  selectedId: string | null
  workspaces: readonly WorkspaceSummary[]
}>()
const emit = defineEmits<{
  create: [name: string]
  'request-delete': [workspace: WorkspaceSummary]
  select: [id: string]
}>()

const showCreateForm = ref(false)
const workspaceName = ref('')
const { t } = useI18n()

function submitCreate(): void {
  if (workspaceName.value !== '') {
    emit('create', workspaceName.value)
  }
}

function cancelCreate(): void {
  workspaceName.value = ''
  showCreateForm.value = false
}

function stateIcon(state: WorkspaceState): string {
  return state === 'ready' ? '✓' : state === 'stale' ? '△!' : '◇!'
}

function stateLabel(state: WorkspaceState): string {
  switch (state) {
    case 'preparing': return t('workspace.states.preparing')
    case 'ready': return t('workspace.states.ready')
    case 'stale': return t('workspace.states.stale')
    case 'published': return t('workspace.states.published')
    case 'needs_attention': return t('workspace.states.needsAttention')
  }
}
</script>

<style scoped>
.workspace-list {
  display: grid;
  min-width: 0;
  gap: var(--spacing-md);
}

.workspace-list__header,
.workspace-list__create div,
.workspace-list li,
.workspace-list li a {
  display: flex;
  min-width: 0;
  align-items: center;
}

.workspace-list__header,
.workspace-list li {
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.workspace-list h2,
.workspace-list p {
  margin: 0;
}

.workspace-list button,
.workspace-list li a {
  min-width: var(--component-control-min-size);
  min-height: var(--component-control-min-size);
}

.workspace-list button {
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.workspace-list button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.workspace-list button:active:not(:disabled) {
  transform: scale(0.95);
}

.workspace-list__create {
  display: grid;
  gap: var(--spacing-xs);
}

.workspace-list__create input {
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
}

.workspace-list__create div {
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--spacing-xs);
}

.workspace-list ul {
  display: grid;
  margin: 0;
  padding: 0;
  gap: var(--spacing-xs);
  list-style: none;
}

.workspace-list li {
  padding: var(--spacing-xs);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.workspace-list li a {
  flex: 1;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
  color: var(--color-ink);
  overflow-wrap: anywhere;
  text-decoration: none;
}

.workspace-list li a[aria-current='page'] {
  color: var(--color-primary);
  font-weight: var(--font-weight-semibold);
}

.workspace-list__state,
.workspace-list__pending,
.workspace-list__empty {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
}
</style>

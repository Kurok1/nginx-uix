<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <section
    class="config-editor"
    aria-label="Workspace editor"
  >
    <p
      v-if="documents.length === 0"
      class="config-editor__empty"
      role="status"
    >
      Select a managed file to edit.
    </p>
    <template v-else>
      <div
        class="config-editor__tabs"
        role="tablist"
        aria-label="Open configuration files"
      >
        <div
          v-for="document in documents"
          :key="document.path"
          class="config-editor__tab"
        >
          <button
            :id="tabId(document.path)"
            type="button"
            role="tab"
            :aria-controls="panelId(document.path)"
            :aria-selected="document.path === selectedPath"
            :tabindex="document.path === selectedPath ? 0 : -1"
            :aria-label="`Select ${document.path}`"
            @click="emit('select', document.path)"
          >
            {{ basename(document.path) }}
            <span v-if="document.dirty">— Unsaved changes</span>
          </button>
          <button
            type="button"
            :aria-label="`Close ${document.path}`"
            @click="emit('close', document.path)"
          >
            <span aria-hidden="true">×</span>
          </button>
        </div>
      </div>

      <div
        v-for="document in documents"
        v-show="document.path === selectedPath"
        :id="panelId(document.path)"
        :key="document.path"
        class="config-editor__panel"
        role="tabpanel"
        :aria-labelledby="tabId(document.path)"
      >
        <header>
          <div>
            <h2>{{ document.path }}</h2>
            <p v-if="document.dirty">
              Unsaved changes
            </p>
          </div>
          <div class="config-editor__actions">
            <button
              type="button"
              :aria-label="`Find in ${document.path}`"
              @click="openFind(document.path)"
            >
              Find
            </button>
            <button
              type="button"
              :aria-label="`Save ${document.path}`"
              :disabled="!canSave || document.path !== selectedPath"
              :aria-describedby="saveReason === '' ? undefined : saveReasonId"
              @click="emit('save', document.path)"
            >
              Save
            </button>
          </div>
        </header>
        <p
          v-if="document.path === selectedPath && saveReason !== ''"
          :id="saveReasonId"
          class="config-editor__save-reason"
        >
          {{ saveReason }}
        </p>
        <CodeEditor
          :ref="(component) => setEditor(document.path, component)"
          v-bind="{ ariaLabel: `${document.path} editor` }"
          :model-value="document.content"
          :read-only="readOnly"
          @update:model-value="emitDocumentUpdate(document, $event)"
        />
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import type { EditorView } from '@codemirror/view'
import { openSearchPanel } from '@codemirror/search'
import { computed, useId } from 'vue'

import type { OpenDocument } from '../workspace'
import CodeEditor from './CodeEditor.vue'

interface CodeEditorExpose {
  editorViewForTest(): EditorView
}

const props = defineProps<{
  canSave: boolean
  documents: readonly OpenDocument[]
  pending: boolean
  readOnly: boolean
  selectedPath: string | null
}>()
const emit = defineEmits<{
  close: [path: string]
  save: [path: string]
  select: [path: string]
  update: [path: string, content: string]
}>()

const instanceId = useId()
const saveReasonId = useId()
const editors = new Map<string, CodeEditorExpose>()
const selectedDocument = computed(() =>
  props.documents.find(({ path }) => path === props.selectedPath),
)
const saveReason = computed(() => {
  if (props.readOnly) return 'This workspace is read-only.'
  if (props.pending) return 'A workspace change is in progress.'
  if (selectedDocument.value?.requiresRefresh) return 'Read the server version before saving.'
  if (!selectedDocument.value?.dirty) return 'No unsaved changes.'
  if (!props.canSave) return 'Saving is unavailable.'
  return ''
})

function setEditor(path: string, component: unknown): void {
  if (
    typeof component === 'object' &&
    component !== null &&
    'editorViewForTest' in component &&
    typeof component.editorViewForTest === 'function'
  ) {
    editors.set(path, component as CodeEditorExpose)
  } else {
    editors.delete(path)
  }
}

function openFind(path: string): void {
  const editor = editors.get(path)
  if (editor !== undefined) {
    openSearchPanel(editor.editorViewForTest())
  }
}

function emitDocumentUpdate(document: OpenDocument, content: string): void {
  const normalized =
    document.lineEnding === 'crlf'
      ? content.replaceAll(/\r\n|\r|\n/g, '\r\n')
      : document.lineEnding === 'lf'
        ? content.replaceAll(/\r\n|\r/g, '\n')
        : content
  emit('update', document.path, normalized)
}

function basename(path: string): string {
  return path.split('/').at(-1) ?? path
}

function idPath(path: string): string {
  return path.replaceAll(/[^a-zA-Z0-9_-]/g, '-')
}

function tabId(path: string): string {
  return `${instanceId}-tab-${idPath(path)}`
}

function panelId(path: string): string {
  return `${instanceId}-panel-${idPath(path)}`
}
</script>

<style scoped>
.config-editor,
.config-editor__panel {
  display: grid;
  min-width: 0;
  gap: var(--spacing-sm);
}

.config-editor__empty {
  margin: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.config-editor__tabs,
.config-editor__tab,
.config-editor__panel header,
.config-editor__actions {
  display: flex;
  min-width: 0;
  align-items: center;
}

.config-editor__tabs {
  overflow-x: auto;
  gap: var(--spacing-xs);
}

.config-editor__tab {
  flex: none;
}

.config-editor button {
  min-width: var(--component-control-min-size);
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-primary);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.config-editor__tab button:first-child {
  border-radius: var(--rounded-pill) var(--rounded-none) var(--rounded-none) var(--rounded-pill);
}

.config-editor__tab button:last-child {
  border-radius: var(--rounded-none) var(--rounded-pill) var(--rounded-pill) var(--rounded-none);
}

.config-editor button[aria-selected='true'] {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.config-editor button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.config-editor button:active:not(:disabled) {
  transform: scale(0.95);
}

.config-editor__panel header {
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.config-editor__panel h2,
.config-editor__panel p {
  margin: 0;
  overflow-wrap: anywhere;
}

.config-editor__actions {
  flex-wrap: wrap;
  gap: var(--spacing-xs);
}

.config-editor__actions button {
  border-radius: var(--rounded-pill);
}

.config-editor__save-reason {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
}
</style>

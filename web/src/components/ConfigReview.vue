<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <section
    class="config-review"
    aria-label="Workspace review"
  >
    <div
      class="config-review__tabs"
      role="group"
      aria-label="Review mode"
    >
      <button
        type="button"
        aria-label="Review current file diff"
        :aria-pressed="activeTab === 'current'"
        :disabled="selectedPath === null || pending"
        @click="requestDiff('current')"
      >
        Current file
      </button>
      <button
        type="button"
        aria-label="Review all file diffs"
        :aria-pressed="activeTab === 'all'"
        :disabled="pending"
        @click="requestDiff('all')"
      >
        All changes
      </button>
      <button
        type="button"
        aria-label="Review include dependencies"
        :aria-pressed="activeTab === 'dependencies'"
        @click="activeTab = 'dependencies'"
      >
        Includes
      </button>
      <button
        type="button"
        aria-label="Search workspace files"
        :aria-pressed="activeTab === 'search'"
        @click="activeTab = 'search'"
      >
        Search
      </button>
    </div>

    <div v-if="activeTab === 'current' || activeTab === 'all'">
      <InlineBanner
        v-if="diff?.reason === 'response_limit'"
        kind="info"
        message="Diff incomplete: response limit reached"
      />
      <p v-if="diff === null">
        Choose a diff scope to review workspace changes.
      </p>
      <template v-else>
        <ul class="config-review__summaries">
          <li
            v-for="file in diff.files"
            :key="file.path"
          >
            <strong>{{ file.path }}</strong>
            <span>{{ file.status }}</span>
            <span>+{{ file.added_lines }}</span>
            <span>−{{ file.removed_lines }}</span>
          </li>
        </ul>
        <div
          v-if="diff.patch !== ''"
          class="config-review__patch"
          role="region"
          aria-label="Unified configuration diff"
          tabindex="0"
        >
          <div
            v-for="(line, index) in patchLines"
            :key="`${index}-${line.content}`"
            class="config-review__line"
            :class="`config-review__line--${line.kind}`"
            :data-diff-line="line.kind === 'meta' ? undefined : line.kind"
          >
            <span class="config-review__numbers">{{ lineNumbers(line) }}</span>
            <span
              class="config-review__marker"
              aria-hidden="true"
            >{{ line.marker }}</span>
            <span class="config-review__line-label">{{ line.label }}</span>
            <code>{{ line.content }}</code>
          </div>
        </div>
      </template>
    </div>

    <div v-else-if="activeTab === 'dependencies'">
      <p v-if="dependencies.length === 0">
        No include dependencies were found.
      </p>
      <ul v-else>
        <li
          v-for="dependency in dependencies"
          :key="`${dependency.source}:${dependency.line}:${dependency.column}:${dependency.display_value}`"
        >
          <strong>{{ dependency.source }}:{{ dependency.line }}:{{ dependency.column }}</strong>
          <code>{{ dependency.display_value }}</code>
          <span>{{ dependencyStatus(dependency.status) }}</span>
          <span v-if="dependency.target !== undefined">→ {{ dependency.target }}</span>
        </li>
      </ul>
    </div>

    <div v-else>
      <form @submit.prevent="submitSearch">
        <label for="config-review-search">Search workspace text</label>
        <input
          id="config-review-search"
          v-model="query"
          name="workspace-search"
          type="search"
        >
        <button
          type="submit"
          :disabled="query === '' || pending"
        >
          Search
        </button>
      </form>
      <p
        v-if="search !== null && !search.complete"
        role="status"
      >
        Search incomplete: response limit reached
      </p>
      <ul v-if="search !== null">
        <li
          v-for="match in search.matches"
          :key="`${match.path}:${match.line}:${match.column}`"
        >
          <button
            type="button"
            :aria-label="`Open search match ${match.path} line ${match.line}`"
            @click="emit('select', match.path)"
          >
            {{ match.path }}:{{ match.line }}:{{ match.column }}
          </button>
          <code>{{ match.snippet }}</code>
        </li>
      </ul>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

import type { ConfigDependency, DependencyStatus, DiffResponse, SearchResponse } from '../api/types'
import InlineBanner from './InlineBanner.vue'

type ReviewTab = 'all' | 'current' | 'dependencies' | 'search'
type PatchKind = 'added' | 'context' | 'meta' | 'removed'

interface PatchLine {
  kind: PatchKind
  marker: string
  label: string
  content: string
  oldLine?: number
  newLine?: number
}

const props = defineProps<{
  dependencies: readonly ConfigDependency[]
  diff: DiffResponse | null
  pending: boolean
  search: SearchResponse | null
  selectedPath: string | null
}>()
const emit = defineEmits<{
  'request-diff': [path?: string]
  search: [query: string]
  select: [path: string]
}>()

const activeTab = ref<ReviewTab>('current')
const query = ref('')
const patchLines = computed(() => parsePatch(props.diff?.patch ?? ''))

function requestDiff(scope: 'all' | 'current'): void {
  activeTab.value = scope
  if (scope === 'current' && props.selectedPath !== null) {
    emit('request-diff', props.selectedPath)
  } else if (scope === 'all') {
    emit('request-diff')
  }
}

function submitSearch(): void {
  if (query.value !== '') {
    emit('search', query.value)
  }
}

function dependencyStatus(status: DependencyStatus): string {
  return `${status.charAt(0).toUpperCase()}${status.slice(1)}`
}

function lineNumbers(line: PatchLine): string {
  if (line.oldLine === undefined && line.newLine === undefined) return ''
  return `${line.oldLine ?? '–'} / ${line.newLine ?? '–'}`
}

function parsePatch(patch: string): PatchLine[] {
  let oldLine = 0
  let newLine = 0
  const lines: PatchLine[] = []
  for (const content of patch.split('\n')) {
    if (content === '') continue
    const range = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(content)
    if (range !== null) {
      oldLine = Number(range[1])
      newLine = Number(range[2])
      lines.push({ kind: 'meta', marker: '@', label: 'Range', content })
    } else if (content.startsWith('---') || content.startsWith('+++')) {
      lines.push({ kind: 'meta', marker: '·', label: 'File', content })
    } else if (content.startsWith('-')) {
      lines.push({
        kind: 'removed',
        marker: '−',
        label: 'Removed',
        content: content.slice(1),
        oldLine,
      })
      oldLine += 1
    } else if (content.startsWith('+')) {
      lines.push({
        kind: 'added',
        marker: '+',
        label: 'Added',
        content: content.slice(1),
        newLine,
      })
      newLine += 1
    } else {
      lines.push({
        kind: 'context',
        marker: '·',
        label: 'Context',
        content: content.startsWith(' ') ? content.slice(1) : content,
        oldLine,
        newLine,
      })
      oldLine += 1
      newLine += 1
    }
  }
  return lines
}
</script>

<style scoped>
.config-review {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr);
  align-content: start;
  gap: var(--spacing-sm);
}

.config-review__tabs,
.config-review form {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
}

.config-review button,
.config-review input {
  min-width: var(--component-control-min-size);
  min-height: var(--component-control-min-size);
}

.config-review button {
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.config-review button[aria-pressed='true'] {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.config-review button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.config-review button:active:not(:disabled) {
  transform: scale(0.95);
}

.config-review form {
  align-items: end;
}

.config-review form label {
  width: 100%;
}

.config-review input {
  flex: 1;
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
}

.config-review ul {
  display: grid;
  min-width: 0;
  margin: 0;
  padding: 0;
  gap: var(--spacing-xs);
  list-style: none;
}

.config-review li {
  min-width: 0;
  overflow-wrap: anywhere;
}

.config-review__summaries li {
  display: flex;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
  padding: var(--spacing-xs);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
}

.config-review__patch {
  min-width: 0;
  max-width: 100%;
  margin-block-start: var(--spacing-sm);
  overflow-x: auto;
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  font-family: var(--font-code);
  font-size: 13px;
  line-height: 1.54;
}

.config-review__line {
  display: grid;
  width: max-content;
  min-width: 100%;
  grid-template-columns: 8ch 2ch 8ch minmax(max-content, 1fr);
  gap: var(--spacing-xxs);
  padding-inline: var(--spacing-xs);
  white-space: pre;
}

.config-review__line--added {
  background: var(--color-diff-added);
  color: var(--color-diff-added-foreground);
}

.config-review__line--removed {
  background: var(--color-diff-removed);
  color: var(--color-diff-removed-foreground);
}

.config-review__line--context,
.config-review__line--meta {
  background: var(--color-diff-context);
  color: var(--color-diff-context-foreground);
}

.config-review__numbers,
.config-review__line-label {
  font-family: var(--font-ui);
  font-size: var(--font-size-caption);
}
</style>

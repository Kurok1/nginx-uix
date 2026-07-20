<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.3.0
-->
<template>
  <section
    v-if="items.length > 0"
    class="structured-diagnostic-list"
    :role="hasBlocking ? 'alert' : 'status'"
    aria-labelledby="structured-diagnostics-title"
  >
    <h2 id="structured-diagnostics-title">
      Structure diagnostics
    </h2>
    <ul>
      <li
        v-for="item in items"
        :key="item.key"
        :class="'structured-diagnostic-list__item--' + item.severity"
      >
        <span class="structured-diagnostic-list__severity">
          <span aria-hidden="true">{{ item.severity === 'blocking' ? '◇!' : '△!' }}</span>
          {{ item.severity === 'blocking' ? 'Blocking' : 'Warning' }}
        </span>
        <code>{{ item.code }}</code>
        <a :href="sourceHref(item.path, item.line)">
          {{ item.path }}:{{ item.line }}:{{ item.column }}
        </a>
        <span v-if="item.related !== ''">Related: {{ item.related }}</span>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type {
  StructuredDiagnostic,
  StructuredProjectDiagnostic,
} from '../api/structured'

interface DiagnosticItem {
  key: string
  code: string
  severity: 'blocking' | 'warning'
  path: string
  line: number
  column: number
  related: string
}

const props = defineProps<{
  rawEditorPath: string
  projectDiagnostics: readonly StructuredProjectDiagnostic[]
  diagnostics: readonly StructuredDiagnostic[]
}>()
const items = computed<DiagnosticItem[]>(() => [
  ...props.projectDiagnostics.map((diagnostic) => ({
    key:
      'project:' +
      diagnostic.code +
      ':' +
      diagnostic.path +
      ':' +
      String(diagnostic.line) +
      ':' +
      String(diagnostic.column),
    code: diagnostic.code,
    severity: 'blocking' as const,
    path: diagnostic.path,
    line: diagnostic.line,
    column: diagnostic.column,
    related: diagnostic.related_path ?? '',
  })),
  ...props.diagnostics.map((diagnostic) => ({
    key:
      diagnostic.domain +
      ':' +
      diagnostic.code +
      ':' +
      diagnostic.source.path +
      ':' +
      String(diagnostic.source.start_line) +
      ':' +
      (diagnostic.related_id ?? ''),
    code: diagnostic.code,
    severity: diagnostic.severity,
    path: diagnostic.source.path,
    line: diagnostic.source.start_line,
    column: diagnostic.source.start_column,
    related: diagnostic.related_id ?? diagnostic.parent_id ?? '',
  })),
])
const hasBlocking = computed(() => items.value.some((item) => item.severity === 'blocking'))

function sourceHref(path: string, line: number): string {
  return props.rawEditorPath + '?path=' + encodeURIComponent(path) + '#line-' + String(line)
}
</script>

<style scoped>
.structured-diagnostic-list {
  display: grid;
  max-height: var(--component-structured-diagnostic-max-height);
  min-width: 0;
  overflow: auto;
  gap: var(--spacing-sm);
  padding: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.structured-diagnostic-list h2 {
  margin: 0;
  font-size: var(--font-size-tagline);
}

.structured-diagnostic-list ul {
  display: grid;
  margin: 0;
  padding: 0;
  gap: var(--spacing-xs);
  list-style: none;
}

.structured-diagnostic-list li {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
  padding: var(--spacing-xs);
  border-inline-start: 2px solid currentcolor;
  font-size: var(--font-size-caption);
}

.structured-diagnostic-list__item--blocking {
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}

.structured-diagnostic-list__item--warning {
  background: var(--color-state-warning);
  color: var(--color-state-warning-foreground);
}

.structured-diagnostic-list__severity {
  display: inline-flex;
  align-items: center;
  gap: var(--spacing-xxs);
  font-weight: var(--font-weight-semibold);
}

.structured-diagnostic-list a {
  display: inline-flex;
  min-width: var(--component-control-min-size);
  min-height: var(--component-control-min-size);
  padding-inline: var(--spacing-xs);
  align-items: center;
  color: currentcolor;
}
</style>

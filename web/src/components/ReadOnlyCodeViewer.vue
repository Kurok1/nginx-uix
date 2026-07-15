<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <section
    class="read-only-code-viewer"
    :aria-labelledby="headingId"
  >
    <header class="read-only-code-viewer__header">
      <div class="read-only-code-viewer__metadata">
        <h2 :id="headingId">
          配置内容
        </h2>
        <p
          :id="fileId"
          class="read-only-code-viewer__path"
        >
          {{ occurrence.path }}
        </p>
        <p class="read-only-code-viewer__order">
          第 {{ occurrence.load_order }} 项
        </p>
      </div>
      <button
        type="button"
        :aria-pressed="wrapLines"
        @click="wrapLines = !wrapLines"
      >
        {{ wrapLines ? '关闭自动换行' : '自动换行' }}
      </button>
    </header>

    <div
      class="read-only-code-viewer__scroll"
      :class="{ 'read-only-code-viewer__scroll--wrap': wrapLines }"
      role="region"
      tabindex="0"
      :aria-labelledby="`${headingId} ${fileId}`"
    >
      <pre
        class="read-only-code-viewer__line-numbers"
        aria-hidden="true"
      >{{ lineNumbers }}</pre>
      <pre class="read-only-code-viewer__content"><code>{{ occurrence.content }}</code></pre>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, useId } from 'vue'

import type { EffectiveConfigOccurrence } from '../api/types'

const props = defineProps<{
  occurrence: EffectiveConfigOccurrence
}>()

const wrapLines = ref(false)
const headingId = useId()
const fileId = useId()

const lineNumbers = computed(() =>
  Array.from(
    { length: props.occurrence.content.split('\n').length },
    (_line, index) => index + 1,
  ).join('\n'),
)
</script>

<style scoped>
.read-only-code-viewer,
.read-only-code-viewer__header,
.read-only-code-viewer__metadata,
.read-only-code-viewer__scroll,
.read-only-code-viewer__content,
.read-only-code-viewer__content code {
  min-width: 0;
}

.read-only-code-viewer {
  display: grid;
  gap: var(--spacing-lg);
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.read-only-code-viewer__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--spacing-lg);
}

.read-only-code-viewer__header h2,
.read-only-code-viewer__header p {
  margin: 0;
}

.read-only-code-viewer__path {
  margin-block-start: var(--spacing-xs) !important;
  overflow-wrap: anywhere;
}

.read-only-code-viewer__order {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
}

.read-only-code-viewer__header button {
  flex: none;
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.read-only-code-viewer__header button:active {
  transform: scale(0.95);
}

.read-only-code-viewer__header button[aria-pressed='true'] {
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.read-only-code-viewer__scroll {
  display: grid;
  max-width: 100%;
  max-height: 60vh;
  grid-template-columns: max-content max-content;
  align-items: stretch;
  overflow: auto;
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  user-select: text;
}

.read-only-code-viewer pre,
.read-only-code-viewer code {
  font-family: var(--font-code);
  font-size: 13px;
  font-weight: var(--font-weight-regular);
  line-height: 1.54;
  tab-size: 2;
}

.read-only-code-viewer pre {
  min-height: 100%;
  margin: 0;
  padding-block: var(--spacing-sm);
  white-space: pre;
  user-select: text;
}

.read-only-code-viewer__line-numbers {
  padding-inline: var(--spacing-sm);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  text-align: end;
}

.read-only-code-viewer__content {
  padding-inline: var(--spacing-sm) var(--spacing-lg);
  color: var(--color-ink);
}

.read-only-code-viewer__scroll--wrap {
  grid-template-columns: max-content minmax(0, 1fr);
}

.read-only-code-viewer__scroll--wrap .read-only-code-viewer__content {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

@media (max-width: 480px) {
  .read-only-code-viewer__header {
    flex-direction: column;
  }
}
</style>

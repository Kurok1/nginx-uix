<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <div
    class="inline-banner"
    :class="`inline-banner--${kind}`"
    :role="role"
  >
    <span
      class="inline-banner__icon"
      :data-icon="kind"
      aria-hidden="true"
    >{{ icon }}</span>
    <p>{{ message }}</p>
    <div
      v-if="$slots.actions"
      class="inline-banner__actions"
    >
      <slot name="actions" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

export type InlineBannerKind =
  | 'agent'
  | 'conflict'
  | 'info'
  | 'needs_attention'
  | 'stale'

const props = defineProps<{
  kind: InlineBannerKind
  message: string
}>()

const icons: Record<InlineBannerKind, string> = {
  agent: '⬡!',
  conflict: '⇄!',
  info: 'ⓘ',
  needs_attention: '◇!',
  stale: '△!',
}
const role = computed(() =>
  props.kind === 'info' || props.kind === 'stale' ? 'status' : 'alert',
)
const icon = computed(() => icons[props.kind])
</script>

<style scoped>
.inline-banner {
  display: grid;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: start;
  gap: var(--spacing-sm);
  padding: var(--spacing-sm) var(--spacing-md);
  border: 1px solid currentcolor;
  border-radius: var(--rounded-sm);
}

.inline-banner p {
  margin: 0;
  overflow-wrap: anywhere;
}

.inline-banner__icon {
  font-weight: var(--font-weight-semibold);
}

.inline-banner__actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  grid-column: 2;
  gap: var(--spacing-xs);
}

.inline-banner__actions :deep(button),
.inline-banner__actions :deep(a) {
  display: inline-flex;
  min-width: var(--component-control-min-size);
  min-height: var(--component-control-min-size);
  align-items: center;
  justify-content: center;
}

.inline-banner--info {
  background: var(--color-state-info);
  color: var(--color-state-info-foreground);
}

.inline-banner--stale {
  background: var(--color-state-warning);
  color: var(--color-state-warning-foreground);
}

.inline-banner--agent,
.inline-banner--conflict,
.inline-banner--needs_attention {
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}
</style>

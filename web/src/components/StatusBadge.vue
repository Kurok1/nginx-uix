<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <span
    class="status-badge"
    :class="`status-badge--${tone}`"
    :aria-label="label"
  >
    <svg
      v-if="tone === 'success'"
      aria-hidden="true"
      data-shape="circle"
      focusable="false"
      viewBox="0 0 24 24"
    >
      <circle
        cx="12"
        cy="12"
        r="9"
      />
      <path d="m8 12 2.5 2.5L16 9" />
    </svg>
    <svg
      v-else-if="tone === 'warning'"
      aria-hidden="true"
      data-shape="triangle"
      focusable="false"
      viewBox="0 0 24 24"
    >
      <path d="M12 3 22 20H2Z" />
      <path d="M12 9v5" />
      <path d="M12 17.5h.01" />
    </svg>
    <svg
      v-else-if="tone === 'error'"
      aria-hidden="true"
      data-shape="octagon"
      focusable="false"
      viewBox="0 0 24 24"
    >
      <path d="m8 2 8 0 6 6v8l-6 6H8l-6-6V8Z" />
      <path d="M12 7.5v6" />
      <path d="M12 17h.01" />
    </svg>
    <svg
      v-else
      aria-hidden="true"
      data-shape="diamond"
      focusable="false"
      viewBox="0 0 24 24"
    >
      <path d="m12 2 10 10-10 10L2 12Z" />
      <path d="M9.75 9a2.25 2.25 0 1 1 3.4 1.94c-.75.45-1.15.83-1.15 1.56" />
      <path d="M12 16.5h.01" />
    </svg>
    <span class="status-badge__text">{{ label }}</span>
  </span>
</template>

<script setup lang="ts">
export type StatusTone = 'success' | 'warning' | 'error' | 'unknown'

defineProps<{
  tone: StatusTone
  label: string
}>()
</script>

<style scoped>
.status-badge {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  gap: var(--spacing-xxs);
  padding: var(--spacing-xxs) 10px;
  border-radius: var(--rounded-pill);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-semibold);
  letter-spacing: var(--letter-spacing-caption);
  line-height: 20px;
  white-space: nowrap;
}

.status-badge svg {
  width: 16px;
  height: 16px;
  flex: 0 0 auto;
  fill: none;
  stroke: currentcolor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.8;
}

.status-badge__text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.status-badge--success {
  background: var(--color-status-success-surface);
  color: var(--color-status-success-foreground);
}

.status-badge--warning {
  background: var(--color-status-warning-surface);
  color: var(--color-status-warning-foreground);
}

.status-badge--error {
  background: var(--color-status-error-surface);
  color: var(--color-status-error-foreground);
}

.status-badge--unknown {
  background: var(--color-status-unknown-surface);
  color: var(--color-status-unknown-foreground);
}
</style>

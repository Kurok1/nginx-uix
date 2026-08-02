<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <section
    class="unsaved-recovery"
    :aria-labelledby="headingId"
  >
    <h2 :id="headingId">
      {{ t('recoveryDraft.title') }}
    </h2>
    <p>
      {{ t('recoveryDraft.description') }}
    </p>
    <ul>
      <li
        v-for="path in paths"
        :key="path"
      >
        <span>{{ path }}</span>
        <button
          type="button"
          :aria-label="t('recoveryDraft.copyLabel', { path })"
          @click="emit('copy', path)"
        >
          {{ t('recoveryDraft.copy') }}
        </button>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'

defineProps<{
  paths: readonly string[]
}>()
const emit = defineEmits<{
  copy: [path: string]
}>()

const headingId = useId()
const { t } = useI18n()
</script>

<style scoped>
.unsaved-recovery {
  min-width: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-state-warning-foreground);
  border-radius: var(--rounded-lg);
  background: var(--color-state-warning);
  color: var(--color-state-warning-foreground);
}

.unsaved-recovery h2 {
  margin-block-end: var(--spacing-xs);
}

.unsaved-recovery p {
  margin-block-end: var(--spacing-md);
}

.unsaved-recovery ul {
  display: grid;
  gap: var(--spacing-xs);
  margin: 0;
  padding: 0;
  list-style: none;
}

.unsaved-recovery li {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.unsaved-recovery li span {
  min-width: 0;
  overflow-wrap: anywhere;
}

.unsaved-recovery button {
  min-width: var(--component-control-min-size);
  min-height: var(--component-control-min-size);
  flex: none;
  padding: var(--spacing-xs) var(--spacing-md);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.unsaved-recovery button:active {
  transform: scale(0.95);
}
</style>

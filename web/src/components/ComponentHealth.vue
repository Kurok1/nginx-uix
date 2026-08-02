<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <article
    class="component-health"
    :aria-labelledby="headingId"
  >
    <div class="component-health__heading">
      <h3 :id="headingId">
        {{ name }}
      </h3>
      <StatusBadge
        :tone="presentation.tone"
        :label="presentation.label"
      />
    </div>
    <p>{{ presentation.description }}</p>
  </article>
</template>

<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AgentHealthState, NginxRuntimeState } from '../api/types'
import StatusBadge, { type StatusTone } from './StatusBadge.vue'

export type ComponentHealthState = 'healthy' | AgentHealthState | NginxRuntimeState

interface HealthPresentation {
  label: string
  tone: StatusTone
  description: string
}

const props = defineProps<{
  name: string
  state: ComponentHealthState
}>()

const headingId = `component-health-${useId()}`
const { t } = useI18n()

const presentation = computed<HealthPresentation>(() => {
  switch (props.state) {
    case 'healthy':
      return {
        label: t('dashboard.health.healthyLabel'),
        tone: 'success',
        description: t('dashboard.health.healthyDescription'),
      }
    case 'running':
      return {
        label: t('dashboard.health.runningLabel'),
        tone: 'success',
        description: t('dashboard.health.runningDescription'),
      }
    case 'degraded':
      return {
        label: t('dashboard.health.degradedLabel'),
        tone: 'warning',
        description: t('dashboard.health.degradedDescription'),
      }
    case 'stopped':
      return {
        label: t('dashboard.health.stoppedLabel'),
        tone: 'error',
        description: t('dashboard.health.stoppedDescription'),
      }
    case 'unavailable':
      return {
        label: t('dashboard.health.unavailableLabel'),
        tone: 'error',
        description: t('dashboard.health.unavailableDescription'),
      }
    case 'unknown':
      return {
        label: t('dashboard.health.unknownLabel'),
        tone: 'unknown',
        description: t('dashboard.health.unknownDescription'),
      }
  }
  return {
    label: t('dashboard.health.unknownLabel'),
    tone: 'unknown',
    description: t('dashboard.health.unknownDescription'),
  }
})
</script>

<style scoped>
.component-health {
  min-width: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.component-health__heading {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.component-health h3,
.component-health p {
  margin: 0;
}

.component-health h3 {
  min-width: 0;
  font-size: var(--font-size-body);
}

.component-health p {
  margin-block-start: var(--spacing-sm);
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
}
</style>

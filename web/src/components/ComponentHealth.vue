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

const presentation = computed<HealthPresentation>(() => {
  switch (props.state) {
    case 'healthy':
      return { label: '正常', tone: 'success', description: '服务可用。' }
    case 'running':
      return { label: '运行中', tone: 'success', description: '进程状态已验证。' }
    case 'degraded':
      return { label: '降级', tone: 'warning', description: '服务仍在运行，但需要关注。' }
    case 'stopped':
      return { label: '已停止', tone: 'error', description: '未发现运行中的进程。' }
    case 'unavailable':
      return { label: '不可用', tone: 'error', description: '无法连接本地 Agent。' }
    case 'unknown':
      return { label: '未知', tone: 'unknown', description: '当前无法确认运行状态。' }
  }
  return { label: '未知', tone: 'unknown', description: '当前无法确认运行状态。' }
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

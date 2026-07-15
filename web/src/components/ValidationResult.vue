<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <section
    class="validation-result"
    aria-labelledby="validation-result-title"
    :aria-busy="busy ? 'true' : 'false'"
  >
    <h2 id="validation-result-title">
      启动与恢复
    </h2>
    <div class="validation-result__columns">
      <article
        class="validation-result__card"
        aria-labelledby="startup-validation-title"
      >
        <div class="validation-result__card-heading">
          <h3 id="startup-validation-title">
            启动校验
          </h3>
          <StatusBadge
            :tone="validationPresentation.tone"
            :label="validationPresentation.label"
          />
        </div>
        <div class="validation-result__metrics dashboard-grid">
          <MetricCard
            label="检查时间"
            :value="validation?.checked_at ?? null"
            format="timestamp"
            :busy="busy"
          />
          <MetricCard
            label="退出码"
            :value="validation?.exit_code ?? null"
            format="number"
            :busy="busy"
          />
        </div>
        <h4>诊断</h4>
        <pre class="validation-result__diagnostic">{{ diagnostic }}</pre>
      </article>

      <article
        class="validation-result__card"
        aria-labelledby="recovery-status-title"
      >
        <div class="validation-result__card-heading">
          <h3 id="recovery-status-title">
            自动恢复
          </h3>
          <StatusBadge
            :tone="recoveryPresentation.tone"
            :label="recoveryPresentation.label"
          />
        </div>
        <div class="validation-result__metrics dashboard-grid">
          <MetricCard
            label="当前窗口次数"
            :value="recovery?.count ?? null"
            format="number"
            :busy="busy"
          />
          <MetricCard
            label="最近结果"
            :value="recoveryPresentation.value"
            :busy="busy"
          />
          <MetricCard
            label="永久失败"
            :value="recovery === null ? null : recovery.permanent ? '是' : '否'"
            :busy="busy"
          />
        </div>
      </article>
    </div>

    <article
      class="validation-result__issues"
      aria-labelledby="runtime-issues-title"
    >
      <h3 id="runtime-issues-title">
        问题
      </h3>
      <p v-if="issues.length === 0">
        未发现问题。
      </p>
      <ul v-else>
        <li
          v-for="issue in issues"
          :key="issue"
        >
          <StatusBadge
            tone="error"
            :label="issue"
          />
        </li>
      </ul>
    </article>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { RecoveryStatus, StartupValidation } from '../api/types'
import MetricCard from './MetricCard.vue'
import StatusBadge from './StatusBadge.vue'

type StatusTone = 'success' | 'warning' | 'error' | 'unknown'

interface Presentation {
  label: string
  tone: StatusTone
  value: string | null
}

const props = withDefaults(
  defineProps<{
    validation: StartupValidation | null
    recovery: RecoveryStatus | null
    issues: string[]
    busy?: boolean
  }>(),
  { busy: false },
)

const validationPresentation = computed<Presentation>(() => {
  if (props.validation === null) {
    return { label: '无法确认', tone: 'unknown', value: null }
  }
  if (props.validation.valid) {
    return { label: '通过', tone: 'success', value: '通过' }
  }
  return { label: '失败', tone: 'error', value: '失败' }
})

const recoveryPresentation = computed<Presentation>(() => {
  if (props.recovery === null) {
    return { label: '无法确认', tone: 'unknown', value: null }
  }
  switch (props.recovery.last_result) {
    case 'restarting':
      return { label: '正在恢复', tone: 'warning', value: '正在恢复' }
    case 'invalid_config':
      return { label: '配置无效', tone: 'error', value: '配置无效' }
    case 'permanent_failure':
      return { label: '永久失败', tone: 'error', value: '永久失败' }
  }
  return { label: '无法确认', tone: 'unknown', value: null }
})

const diagnostic = computed(() => {
  if (props.validation === null) {
    return '无法确认'
  }
  return props.validation.diagnostic === '' ? '无诊断信息' : props.validation.diagnostic
})
</script>

<style scoped>
.validation-result,
.validation-result__columns,
.validation-result__card,
.validation-result__issues {
  min-width: 0;
}

.validation-result > h2 {
  margin-block-end: var(--spacing-md);
}

.validation-result__columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--spacing-lg);
}

.validation-result__card,
.validation-result__issues {
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.validation-result__card-heading {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--spacing-sm);
}

.validation-result__card h3,
.validation-result__card h4,
.validation-result__issues h3,
.validation-result__issues p,
.validation-result__issues ul {
  margin-block-start: 0;
}

.validation-result__card-heading h3 {
  margin-block-end: 0;
}

.validation-result__metrics {
  margin-block-start: var(--spacing-lg);
}

.validation-result__card h4 {
  margin-block: var(--spacing-lg) var(--spacing-xs);
  font-size: var(--font-size-caption);
}

.validation-result__diagnostic {
  min-width: 0;
  margin: 0;
  padding: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas-parchment);
  font-family: var(--font-code);
  font-size: 13px;
  line-height: 1.54;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.validation-result__issues {
  margin-block-start: var(--spacing-lg);
}

.validation-result__issues h3 {
  margin-block-end: var(--spacing-md);
}

.validation-result__issues p,
.validation-result__issues ul {
  margin-block-end: 0;
}

.validation-result__issues ul {
  display: flex;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
  padding: 0;
  list-style: none;
}

@media (max-width: 734px) {
  .validation-result__columns {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

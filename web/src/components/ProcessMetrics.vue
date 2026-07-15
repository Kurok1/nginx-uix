<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <section
    class="process-metrics"
    aria-labelledby="process-metrics-title"
    :aria-busy="busy ? 'true' : 'false'"
  >
    <h2 id="process-metrics-title">
      进程指标
    </h2>
    <div class="process-metrics__grid dashboard-grid">
      <MetricCard
        label="Master PID"
        :value="master?.pid ?? null"
        format="number"
        :busy="busy"
      />
      <MetricCard
        label="Worker PID"
        :value="workerPIDs"
        :busy="busy"
      />
      <MetricCard
        label="Worker 数量"
        :value="workerCount"
        format="number"
        supporting-text="已验证的 Worker 进程"
        :busy="busy"
      />
      <MetricCard
        label="启动时间"
        :value="master?.started_at ?? null"
        format="timestamp"
        :busy="busy"
      />
    </div>

    <h3>Nginx 构建信息</h3>
    <div class="process-metrics__grid dashboard-grid">
      <MetricCard
        label="Nginx 版本"
        :value="build?.version ?? null"
        :busy="busy"
      />
    </div>

    <div
      class="process-metrics__arguments-card"
      aria-labelledby="process-metrics-arguments-title"
    >
      <h3 id="process-metrics-arguments-title">
        编译参数
      </h3>
      <ol
        v-if="build !== null && build.configure_arguments.length > 0"
        class="process-metrics__arguments"
      >
        <li
          v-for="argument in build.configure_arguments"
          :key="argument"
        >
          <code>{{ argument }}</code>
        </li>
      </ol>
      <p v-else-if="build !== null">
        无
      </p>
      <p v-else>
        无法确认
      </p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { NginxBuild, NginxProcess, NginxRuntimeState } from '../api/types'
import MetricCard from './MetricCard.vue'

const props = withDefaults(
  defineProps<{
    master: NginxProcess | null
    workers: NginxProcess[]
    build: NginxBuild | null
    nginxState: NginxRuntimeState
    busy?: boolean
  }>(),
  { busy: false },
)

const processesConfirmed = computed(() => props.nginxState !== 'unknown')
const workerCount = computed(() => (processesConfirmed.value ? props.workers.length : null))
const workerPIDs = computed(() => {
  if (!processesConfirmed.value) {
    return null
  }
  if (props.workers.length === 0) {
    return '无'
  }
  return props.workers.map((worker) => worker.pid).join('、')
})
</script>

<style scoped>
.process-metrics {
  min-width: 0;
}

.process-metrics h2,
.process-metrics h3,
.process-metrics__arguments-card p {
  margin-block-start: 0;
}

.process-metrics h2 {
  margin-block-end: var(--spacing-md);
}

.process-metrics > h3 {
  margin-block: var(--spacing-xl) var(--spacing-md);
}

.process-metrics__arguments-card {
  min-width: 0;
  margin-block-start: var(--spacing-lg);
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.process-metrics__arguments-card h3 {
  margin-block-end: var(--spacing-md);
}

.process-metrics__arguments {
  min-width: 0;
  margin: 0;
  padding-inline-start: var(--spacing-lg);
}

.process-metrics__arguments li + li {
  margin-block-start: var(--spacing-xs);
}

.process-metrics__arguments code {
  font-family: var(--font-code);
  font-size: 13px;
  overflow-wrap: anywhere;
}
</style>

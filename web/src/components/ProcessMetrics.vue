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
      {{ t('dashboard.processMetrics') }}
    </h2>
    <div class="process-metrics__grid dashboard-grid">
      <MetricCard
        :label="t('dashboard.masterPid')"
        :value="master?.pid ?? null"
        format="number"
        :busy="busy"
      />
      <MetricCard
        :label="t('dashboard.workerPid')"
        :value="workerPIDs"
        :busy="busy"
      />
      <MetricCard
        :label="t('dashboard.workerCount')"
        :value="workerCount"
        format="number"
        :supporting-text="t('dashboard.verifiedWorkers')"
        :busy="busy"
      />
      <MetricCard
        :label="t('dashboard.startedAt')"
        :value="master?.started_at ?? null"
        format="timestamp"
        :busy="busy"
      />
    </div>

    <h3>{{ t('dashboard.buildInformation') }}</h3>
    <div class="process-metrics__grid dashboard-grid">
      <MetricCard
        :label="t('dashboard.nginxVersion')"
        :value="build?.version ?? null"
        :busy="busy"
      />
    </div>

    <div
      class="process-metrics__arguments-card"
      aria-labelledby="process-metrics-arguments-title"
    >
      <h3 id="process-metrics-arguments-title">
        {{ t('dashboard.configureArguments') }}
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
        {{ t('common.none') }}
      </p>
      <p v-else>
        {{ t('common.unableToConfirm') }}
      </p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

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
const { locale, t } = useI18n()

const processesConfirmed = computed(() => props.nginxState !== 'unknown')
const workerCount = computed(() => (processesConfirmed.value ? props.workers.length : null))
const workerPIDs = computed(() => {
  if (!processesConfirmed.value) {
    return null
  }
  if (props.workers.length === 0) {
    return t('common.none')
  }
  return props.workers
    .map((worker) => worker.pid)
    .join(locale.value === 'zh-CN' ? '、' : ', ')
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

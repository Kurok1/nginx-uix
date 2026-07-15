<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <section
    class="effective-config"
    aria-labelledby="effective-config-title"
  >
    <header class="effective-config__header">
      <div>
        <h1 id="effective-config-title">
          生效配置
        </h1>
        <p>按 Nginx 实际加载顺序查看只读配置内容。</p>
      </div>
      <button
        class="effective-config__refresh"
        type="button"
        :disabled="pending"
        aria-describedby="effective-config-refresh-feedback"
        @click="refresh('manual')"
      >
        {{ pending ? '正在刷新…' : '刷新配置' }}
      </button>
    </header>

    <div
      class="effective-config__snapshot"
      :aria-busy="pending"
    >
      <div class="effective-config__state">
        <p v-if="snapshot !== null">
          生成时间：
          <time :datetime="snapshot.generated_at">{{ generatedAt }}</time>
          <StatusBadge
            v-if="stale"
            tone="warning"
            label="旧数据"
          />
        </p>
        <p v-else-if="pending">
          <StatusBadge
            tone="unknown"
            label="加载中"
          />
          正在加载生效配置…
        </p>
        <p
          id="effective-config-refresh-feedback"
          class="effective-config__live-feedback"
          aria-live="polite"
          aria-atomic="true"
        >
          {{ liveMessage }}
        </p>
      </div>

      <div
        v-if="errorMessage !== ''"
        class="effective-config__error"
      >
        <StatusBadge
          tone="error"
          :label="stale ? '刷新失败' : '无法读取'"
        />
        <p>{{ errorMessage }}</p>
      </div>

      <template v-if="snapshot !== null">
        <div
          v-if="snapshot.display_mode === 'raw'"
          class="effective-config__warning"
          role="status"
        >
          <StatusBadge
            tone="warning"
            label="结构未验证"
          />
          <p v-if="snapshot.warnings.includes('NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS')">
            部分配置位于允许读取目录之外，当前显示未经文件拆分的原始输出。若需结构化查看，请通过
            <code>NGINX_UIX_EFFECTIVE_CONFIG_ROOTS</code>
            配置只读目录。
          </p>
          <p v-else>
            当前无法逐文件验证配置边界，正在显示未经文件拆分的原始输出。
          </p>
        </div>

        <dl class="effective-config__summary">
          <div>
            <dt>Nginx 版本：</dt>
            <dd>{{ snapshot.nginx_version }}</dd>
          </div>
          <div>
            <dt>入口配置：</dt>
            <dd>{{ snapshot.entry_config_path }}</dd>
          </div>
          <div>
            <dt>{{ snapshot.display_mode === 'raw' ? '展示模式：' : '加载项：' }}</dt>
            <dd>{{ snapshot.display_mode === 'raw' ? '原始输出' : snapshot.occurrence_count }}</dd>
          </div>
        </dl>

        <ReadOnlyCodeViewer
          v-if="snapshot.display_mode === 'raw'"
          mode="raw"
          :raw-content="snapshot.raw_content"
        />
        <div
          v-else-if="snapshot.occurrences.length > 0 && selectedOccurrence !== null"
          class="effective-config__layout"
        >
          <ConfigFileList
            :occurrences="snapshot.occurrences"
            :selected-id="selectedOccurrence.id"
            @select="selectOccurrence"
          />
          <ReadOnlyCodeViewer :occurrence="selectedOccurrence" />
        </div>
        <div
          v-else
          class="effective-config__empty"
        >
          <StatusBadge
            tone="unknown"
            label="无加载项"
          />
          <p>当前没有加载到配置文件。</p>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'

import { apiClient, APIRequestError } from '../api/client'
import type { EffectiveConfigOccurrence, EffectiveConfigResponse } from '../api/types'
import ConfigFileList from '../components/ConfigFileList.vue'
import ReadOnlyCodeViewer from '../components/ReadOnlyCodeViewer.vue'
import StatusBadge from '../components/StatusBadge.vue'

interface EffectiveConfigClient {
  getEffectiveConfig: (signal?: AbortSignal) => Promise<EffectiveConfigResponse>
}

type RefreshOrigin = 'initial' | 'manual'

const props = withDefaults(
  defineProps<{
    client?: EffectiveConfigClient
  }>(),
  { client: () => apiClient },
)

const snapshot = shallowRef<EffectiveConfigResponse | null>(null)
const selectedId = ref('')
const pending = ref(false)
const stale = ref(false)
const errorMessage = ref('')
const liveMessage = ref('')
let activeController: AbortController | null = null
let unmounted = false

const generatedAt = computed(() => {
  if (snapshot.value === null) {
    return ''
  }
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(new Date(snapshot.value.generated_at))
})

const selectedOccurrence = computed<EffectiveConfigOccurrence | null>(() => {
  if (snapshot.value === null) {
    return null
  }
  return (
    snapshot.value.occurrences.find((occurrence) => occurrence.id === selectedId.value) ?? null
  )
})

function selectOccurrence(id: string): void {
  if (snapshot.value?.occurrences.some((occurrence) => occurrence.id === id)) {
    selectedId.value = id
  }
}

function initialErrorMessage(error: unknown): string {
  if (!(error instanceof APIRequestError) || error.kind !== 'api') {
    return '暂时无法读取生效配置。'
  }
  switch (error.apiError?.code) {
    case 'NGINX_CONFIG_INVALID':
      return 'Nginx 配置当前无效，无法读取生效配置。'
    case 'NGINX_COMMAND_TIMEOUT':
      return '读取生效配置超时，请稍后重试。'
    case 'NGINX_OUTPUT_TOO_LARGE':
      return '生效配置超过安全读取限制，无法在此显示。'
    case 'AGENT_UNAVAILABLE':
      return '本地 Agent 暂时不可用，无法读取生效配置。'
    default:
      return '暂时无法读取生效配置。'
  }
}

async function refresh(origin: RefreshOrigin): Promise<void> {
  if (pending.value || unmounted) {
    return
  }

  pending.value = true
  const controller = new AbortController()
  activeController = controller

  try {
    const next = await props.client.getEffectiveConfig(controller.signal)
    if (unmounted) {
      return
    }
    snapshot.value = next
    if (!next.occurrences.some((occurrence) => occurrence.id === selectedId.value)) {
      selectedId.value = next.occurrences[0]?.id ?? ''
    }
    stale.value = false
    errorMessage.value = ''
    if (origin === 'manual') {
      liveMessage.value = '已刷新生效配置。'
    }
  } catch (error: unknown) {
    if (unmounted) {
      return
    }
    stale.value = snapshot.value !== null
    errorMessage.value = stale.value
      ? '刷新失败，正在显示上一次成功获取的数据。'
      : initialErrorMessage(error)
    if (origin === 'manual') {
      liveMessage.value = '刷新生效配置失败。'
    }
  } finally {
    if (activeController === controller) {
      activeController = null
    }
    if (!unmounted) {
      pending.value = false
    }
  }
}

onMounted(() => {
  void refresh('initial')
})

onBeforeUnmount(() => {
  unmounted = true
  activeController?.abort()
})
</script>

<style scoped>
.effective-config,
.effective-config__header,
.effective-config__header > div,
.effective-config__snapshot,
.effective-config__state,
.effective-config__error,
.effective-config__warning,
.effective-config__summary,
.effective-config__summary > div,
.effective-config__layout,
.effective-config__empty {
  min-width: 0;
}

.effective-config,
.effective-config__snapshot {
  display: grid;
  gap: var(--spacing-xl);
}

.effective-config__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--spacing-lg);
}

.effective-config__header h1,
.effective-config__header p,
.effective-config__state p,
.effective-config__error p,
.effective-config__warning p,
.effective-config__empty p {
  margin: 0;
}

.effective-config__header p,
.effective-config__state,
.effective-config__empty {
  color: var(--color-ink-muted-80);
}

.effective-config__header p {
  margin-block-start: var(--spacing-xs);
}

.effective-config__refresh {
  min-width: max-content;
  min-height: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-md);
  border: 0;
  border-radius: var(--rounded-pill);
  background: var(--color-primary);
  color: var(--color-body-on-dark);
  cursor: pointer;
}

.effective-config__refresh:active:not(:disabled) {
  transform: scale(0.95);
}

.effective-config__refresh:disabled {
  cursor: default;
}

.effective-config__state > p:first-child {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--spacing-xs);
}

.effective-config__live-feedback:empty {
  min-height: 1px;
}

.effective-config__error,
.effective-config__warning,
.effective-config__empty {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.effective-config__warning code {
	font-family: var(--font-code);
	overflow-wrap: anywhere;
}

.effective-config__summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--spacing-lg);
  margin: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.effective-config__summary dt {
  font-weight: var(--font-weight-semibold);
}

.effective-config__summary dd {
  margin: var(--spacing-xs) 0 0;
  overflow-wrap: anywhere;
}

.effective-config__layout {
  display: grid;
  grid-template-columns: minmax(0, 320px) minmax(0, 1fr);
  align-items: start;
  gap: var(--spacing-lg);
}

@media (max-width: 833px) {
  .effective-config__layout {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 640px) {
  .effective-config__header {
    flex-wrap: wrap;
  }

  .effective-config__summary {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

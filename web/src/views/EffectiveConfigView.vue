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
          {{ t('effectiveConfig.title') }}
        </h1>
        <p>{{ t('effectiveConfig.description') }}</p>
      </div>
      <button
        class="effective-config__refresh"
        type="button"
        :disabled="pending"
        aria-describedby="effective-config-refresh-feedback"
        @click="refresh('manual')"
      >
        {{ pending ? t('effectiveConfig.refreshing') : t('effectiveConfig.refresh') }}
      </button>
    </header>

    <div
      class="effective-config__snapshot"
      :aria-busy="pending"
    >
      <div class="effective-config__state">
        <p v-if="snapshot !== null">
          {{ t('effectiveConfig.generatedAt') }}
          <time :datetime="snapshot.generated_at">{{ generatedAt }}</time>
          <StatusBadge
            v-if="stale"
            tone="warning"
            :label="t('effectiveConfig.stale')"
          />
        </p>
        <p v-else-if="pending">
          <StatusBadge
            tone="unknown"
            :label="t('effectiveConfig.loadingLabel')"
          />
          {{ t('effectiveConfig.loading') }}
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
          :label="stale ? t('effectiveConfig.refreshFailed') : t('effectiveConfig.unableToRead')"
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
            :label="t('effectiveConfig.structureUnverified')"
          />
          <p v-if="snapshot.warnings.includes('NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS')">
            {{ t('effectiveConfig.outsideRootsBefore') }}
            <code>NGINX_UIX_EFFECTIVE_CONFIG_ROOTS</code>
            {{ t('effectiveConfig.outsideRootsAfter') }}
          </p>
          <p v-else>
            {{ t('effectiveConfig.unverifiedWarning') }}
          </p>
        </div>

        <dl class="effective-config__summary">
          <div>
            <dt>{{ t('effectiveConfig.nginxVersion') }}</dt>
            <dd>{{ snapshot.nginx_version }}</dd>
          </div>
          <div>
            <dt>{{ t('effectiveConfig.entryConfiguration') }}</dt>
            <dd>{{ snapshot.entry_config_path }}</dd>
          </div>
          <div>
            <dt>
              {{ snapshot.display_mode === 'raw'
                ? t('effectiveConfig.displayMode')
                : t('effectiveConfig.loadedEntries') }}
            </dt>
            <dd>
              {{ snapshot.display_mode === 'raw'
                ? t('effectiveConfig.rawOutput')
                : snapshot.occurrence_count }}
            </dd>
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
            :label="t('effectiveConfig.noEntriesLabel')"
          />
          <p>{{ t('effectiveConfig.noEntries') }}</p>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { apiClient, APIRequestError } from '../api/client'
import { formatAPIRequestError } from '../api/error_message'
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
const { d, t } = useI18n()
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
  return d(new Date(snapshot.value.generated_at), 'short')
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
  if (!(error instanceof APIRequestError)) {
    return t('effectiveConfig.unavailableError')
  }
  if (error.kind !== 'api') return formatAPIRequestError(error)
  let message: string
  switch (error.apiError?.code) {
    case 'NGINX_CONFIG_INVALID':
      message = t('effectiveConfig.invalidError')
      break
    case 'NGINX_COMMAND_TIMEOUT':
      message = t('effectiveConfig.timeoutError')
      break
    case 'NGINX_OUTPUT_TOO_LARGE':
      message = t('effectiveConfig.tooLargeError')
      break
    case 'AGENT_UNAVAILABLE':
      message = t('effectiveConfig.agentUnavailableError')
      break
    default:
      return formatAPIRequestError(error)
  }
  return error.requestID === undefined
    ? message
    : t('errors.withRequestId', { message, requestId: error.requestID })
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
      liveMessage.value = t('effectiveConfig.refreshed')
    }
  } catch (error: unknown) {
    if (unmounted) {
      return
    }
    stale.value = snapshot.value !== null
    errorMessage.value = stale.value
      ? t('effectiveConfig.staleError')
      : initialErrorMessage(error)
    if (origin === 'manual') {
      liveMessage.value = t('effectiveConfig.refreshError')
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

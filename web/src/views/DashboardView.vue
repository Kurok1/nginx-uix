<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <section
    class="dashboard"
    aria-labelledby="dashboard-title"
  >
    <header class="dashboard__header">
      <div>
        <h1 id="dashboard-title">
          {{ t('dashboard.title') }}
        </h1>
        <p>{{ t('dashboard.description') }}</p>
      </div>
      <button
        type="button"
        :disabled="pending"
        aria-describedby="dashboard-refresh-feedback"
        @click="refresh('manual')"
      >
        <svg
          aria-hidden="true"
          focusable="false"
          viewBox="0 0 24 24"
        >
          <path d="M20 7v5h-5" />
          <path d="M18.5 16a8 8 0 1 1 .2-8.2L20 12" />
        </svg>
        {{ pending ? t('dashboard.refreshing') : t('dashboard.refresh') }}
      </button>
    </header>

    <div class="dashboard__meta">
      <p v-if="snapshot !== null">
        {{ t('dashboard.sampledAt') }}
        <time
          class="dashboard__sample-time"
          :datetime="snapshot.sampled_at"
        >{{ sampledAt }}</time>
        <StatusBadge
          v-if="stale"
          tone="warning"
          :label="t('dashboard.stale')"
        />
      </p>
      <p v-else-if="pending">
        {{ t('dashboard.loading') }}
      </p>
      <p
        id="dashboard-refresh-feedback"
        class="dashboard__live-feedback"
        aria-live="polite"
        aria-atomic="true"
      >
        {{ liveMessage }}
      </p>
    </div>

    <div
      v-if="errorMessage !== ''"
      class="dashboard__error"
    >
      <StatusBadge
        tone="error"
        :label="stale ? t('dashboard.refreshFailed') : t('dashboard.unavailable')"
      />
      <p>{{ errorMessage }}</p>
    </div>

    <template v-if="snapshot !== null">
      <RuntimeStatus
        :components="snapshot.components"
        :busy="pending"
      />
      <ProcessMetrics
        :master="snapshot.master"
        :workers="snapshot.workers"
        :build="snapshot.build"
        :nginx-state="snapshot.components.nginx"
        :busy="pending"
      />
      <ValidationResult
        :validation="snapshot.startup_validation"
        :recovery="snapshot.recovery"
        :issues="snapshot.issues"
        :busy="pending"
      />
    </template>
    <div
      v-else-if="!pending"
      class="dashboard__empty"
    >
      <StatusBadge
        tone="unknown"
        :label="t('common.unableToConfirm')"
      />
      <p>{{ t('dashboard.noData') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { apiClient, APIRequestError } from '../api/client'
import { formatAPIRequestError, withAPIRequestID } from '../api/error_message'
import type { SystemStatusResponse } from '../api/types'
import ProcessMetrics from '../components/ProcessMetrics.vue'
import RuntimeStatus from '../components/RuntimeStatus.vue'
import StatusBadge from '../components/StatusBadge.vue'
import ValidationResult from '../components/ValidationResult.vue'

interface SystemStatusClient {
  getSystemStatus: (signal?: AbortSignal) => Promise<SystemStatusResponse>
}

type RefreshOrigin = 'initial' | 'poll' | 'manual'

const props = withDefaults(
  defineProps<{
    client?: SystemStatusClient
  }>(),
  { client: () => apiClient },
)

const snapshot = shallowRef<SystemStatusResponse | null>(null)
const { d, t } = useI18n()
const pending = ref(false)
const stale = ref(false)
const errorMessage = ref('')
const liveMessage = ref('')
let activeController: AbortController | null = null
let pollTimer: ReturnType<typeof setInterval> | undefined
let unmounted = false

const sampledAt = computed(() => {
  if (snapshot.value === null) {
    return ''
  }
  return d(new Date(snapshot.value.sampled_at), 'short')
})

function statusSignature(status: SystemStatusResponse): string {
  return JSON.stringify({ components: status.components, issues: status.issues })
}

async function refresh(origin: RefreshOrigin): Promise<void> {
  if (pending.value || unmounted) {
    return
  }

  pending.value = true
  const controller = new AbortController()
  activeController = controller
  const previous = snapshot.value

  try {
    const next = await props.client.getSystemStatus(controller.signal)
    if (unmounted) {
      return
    }
    snapshot.value = next
    stale.value = false
    errorMessage.value = ''
    if (origin === 'manual') {
      liveMessage.value = t('dashboard.refreshed')
    } else if (
      origin === 'poll' &&
      previous !== null &&
      statusSignature(previous) !== statusSignature(next)
    ) {
      liveMessage.value = t('dashboard.updated')
    }
  } catch (error: unknown) {
    if (unmounted) {
      return
    }
    stale.value = snapshot.value !== null
    const fallback = stale.value
      ? t('dashboard.staleError')
      : t('dashboard.unavailableError')
    errorMessage.value = error instanceof APIRequestError
      ? stale.value
        ? withAPIRequestID(fallback, error)
        : formatAPIRequestError(error)
      : fallback
    if (origin === 'manual') {
      liveMessage.value = t('dashboard.refreshFailed')
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
  pollTimer = setInterval(() => {
    void refresh('poll')
  }, 5000)
})

onBeforeUnmount(() => {
  unmounted = true
  if (pollTimer !== undefined) {
    clearInterval(pollTimer)
  }
  activeController?.abort()
})
</script>

<style scoped>
.dashboard,
.dashboard__header,
.dashboard__header > div,
.dashboard__meta,
.dashboard__error,
.dashboard__empty {
  min-width: 0;
}

.dashboard {
  display: grid;
  gap: var(--spacing-xl);
}

.dashboard__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--spacing-lg);
}

.dashboard__header h1,
.dashboard__header p,
.dashboard__meta p,
.dashboard__error p,
.dashboard__empty p,
.dashboard__live-feedback {
  margin: 0;
}

.dashboard__header p,
.dashboard__meta,
.dashboard__empty {
  color: var(--color-ink-muted-80);
}

.dashboard__header p {
  margin-block-start: var(--spacing-xs);
}

.dashboard__header button {
  display: inline-flex;
  min-width: max-content;
  min-height: var(--component-control-min-size);
  align-items: center;
  justify-content: center;
  gap: var(--spacing-xs);
  padding: var(--spacing-xs) var(--spacing-md);
  border: 0;
  border-radius: var(--rounded-pill);
  background: var(--color-primary);
  color: var(--color-body-on-dark);
  cursor: pointer;
}

.dashboard__header button:active:not(:disabled) {
  transform: scale(0.95);
}

.dashboard__header button svg {
  width: 18px;
  height: 18px;
  fill: none;
  stroke: currentcolor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.8;
}

.dashboard__header button:disabled {
  cursor: default;
}

.dashboard__meta > p:first-child {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--spacing-xs);
}

.dashboard__live-feedback:empty {
  min-height: 1px;
}

.dashboard__error,
.dashboard__empty {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.dashboard :deep(.dashboard-grid) {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--spacing-lg);
}

@media (max-width: 833px) {
  .dashboard :deep(.dashboard-grid) {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .dashboard__header {
    flex-wrap: wrap;
  }

  .dashboard :deep(.dashboard-grid) {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

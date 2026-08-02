<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <article
    class="metric-card"
    :aria-busy="busy ? 'true' : 'false'"
  >
    <dl>
      <dt>{{ label }}</dt>
      <dd>
        <time
          v-if="isTimestamp"
          :datetime="String(value)"
        >{{ formattedValue }}</time>
        <span v-else>{{ formattedValue }}</span>
      </dd>
    </dl>
    <p
      v-if="supportingText"
      class="metric-card__supporting"
    >
      {{ supportingText }}
    </p>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

export type MetricFormat = 'text' | 'number' | 'timestamp'

const props = withDefaults(
  defineProps<{
    label: string
    value: string | number | null
    format?: MetricFormat
    supportingText?: string
    busy?: boolean
  }>(),
  {
    format: 'text',
    supportingText: undefined,
    busy: false,
  },
)

const isTimestamp = computed(
  () => props.value !== null && props.format === 'timestamp' && typeof props.value === 'string',
)
const { d, n, t } = useI18n()

const formattedValue = computed(() => {
  if (props.value === null) {
    return t('common.unableToConfirm')
  }
  if (props.format === 'number' && typeof props.value === 'number') {
    return n(props.value, 'decimal')
  }
  if (props.format === 'timestamp' && typeof props.value === 'string') {
    return d(new Date(props.value), 'short')
  }
  return String(props.value)
})
</script>

<style scoped>
.metric-card {
  min-width: 0;
  padding: var(--spacing-lg);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.metric-card dl,
.metric-card dd,
.metric-card__supporting {
  margin: 0;
}

.metric-card dt {
  margin-block-end: var(--spacing-xs);
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-regular);
  letter-spacing: var(--letter-spacing-caption);
}

.metric-card dd {
  color: var(--color-ink);
  font-size: var(--font-size-body);
  font-weight: var(--font-weight-semibold);
  overflow-wrap: anywhere;
}

.metric-card__supporting {
  margin-block-start: var(--spacing-xs);
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
  overflow-wrap: anywhere;
}
</style>

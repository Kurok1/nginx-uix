<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 1.1.0
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { DEFAULT_LOCALE, parseLocale, switchLocale } from '../i18n'

const { locale, t } = useI18n()
const route = useRoute()
const router = useRouter()

async function selectLocale(event: Event): Promise<void> {
  const selected = parseLocale((event.target as HTMLSelectElement).value)
  if (selected === null || selected === locale.value) return
  await switchLocale(router, route, selected)
}
</script>

<template>
  <label class="language-selector">
    <span class="language-selector__label">{{ t('language.label') }}</span>
    <select
      class="language-selector__select"
      :aria-label="t('language.label')"
      :value="parseLocale(locale) ?? DEFAULT_LOCALE"
      @change="selectLocale"
    >
      <option value="zh-CN">
        {{ t('language.simplifiedChinese') }}
      </option>
      <option value="en-US">
        {{ t('language.english') }}
      </option>
    </select>
  </label>
</template>

<style scoped>
.language-selector {
  display: inline-flex;
  min-width: 0;
  align-items: center;
}

.language-selector__label {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.language-selector__select {
  min-height: var(--component-control-min-size);
  padding-inline: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  color: var(--color-ink);
  font: inherit;
}
</style>

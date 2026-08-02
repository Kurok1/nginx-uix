<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <section
    class="runtime-status"
    aria-labelledby="runtime-status-title"
    :aria-busy="busy ? 'true' : 'false'"
  >
    <h2 id="runtime-status-title">
      {{ t('dashboard.componentHealth') }}
    </h2>
    <div class="runtime-status__grid dashboard-grid">
      <ComponentHealth
        name="UI"
        :state="components.ui"
      />
      <ComponentHealth
        name="Agent"
        :state="components.agent"
      />
      <ComponentHealth
        name="Nginx"
        :state="components.nginx"
      />
    </div>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { SystemComponents } from '../api/types'
import ComponentHealth from './ComponentHealth.vue'

withDefaults(
  defineProps<{
    components: SystemComponents
    busy?: boolean
  }>(),
  { busy: false },
)

const { t } = useI18n()
</script>

<style scoped>
.runtime-status {
  min-width: 0;
}

.runtime-status h2 {
  margin-block-end: var(--spacing-md);
}
</style>

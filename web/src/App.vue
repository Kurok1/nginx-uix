<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <AppShell v-if="route.meta.requiresAuth">
    <RouterView />
  </AppShell>
  <RouterView v-else />
</template>

<script setup lang="ts">
import { RouterView, useRoute } from 'vue-router'

import AppShell from './components/AppShell.vue'

const route = useRoute()
</script>

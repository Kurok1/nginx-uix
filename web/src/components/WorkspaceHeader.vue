<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <header class="workspace-header">
    <div class="workspace-header__title">
      <h1>{{ workspace.name }}</h1>
      <p v-if="workspace.state === 'needs_attention'">
        Workspace ID: {{ workspace.id }}
      </p>
    </div>
    <dl>
      <div>
        <dt>State</dt>
        <dd>
          <span
            data-state-icon
            aria-hidden="true"
          >{{ stateIcon }}</span>
          {{ stateLabel }}
        </dd>
      </div>
      <div>
        <dt>Production</dt>
        <dd>{{ productionMatches ? 'Matches production snapshot' : 'Production changed' }}</dd>
      </div>
      <div>
        <dt>Draft</dt>
        <dd>{{ draftChangeCount }} draft changes</dd>
      </div>
      <div>
        <dt>Safety boundary</dt>
        <dd>尚未执行 Nginx 校验</dd>
      </div>
    </dl>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { WorkspaceDetail } from '../api/types'

const props = defineProps<{
  draftChangeCount: number
  workspace: WorkspaceDetail
}>()

const productionMatches = computed(
  () => props.workspace.production_digest === props.workspace.base_digest,
)
const stateLabel = computed(() => {
  switch (props.workspace.state) {
    case 'preparing': return 'Preparing'
    case 'ready': return 'Ready'
    case 'stale': return 'Stale'
    case 'needs_attention': return 'Needs attention'
    default: return ''
  }
})
const stateIcon = computed(() =>
  props.workspace.state === 'ready'
    ? '✓'
    : props.workspace.state === 'preparing'
      ? '◌'
      : props.workspace.state === 'stale'
        ? '△!'
        : '◇!',
)
</script>

<style scoped>
.workspace-header {
  display: grid;
  min-width: 0;
  min-height: var(--component-workspace-header-min-height);
  gap: var(--spacing-md);
}

.workspace-header h1,
.workspace-header p,
.workspace-header dl,
.workspace-header dt,
.workspace-header dd {
  margin: 0;
}

.workspace-header__title,
.workspace-header dl,
.workspace-header dl div {
  min-width: 0;
}

.workspace-header__title p {
  color: var(--color-state-danger-foreground);
  overflow-wrap: anywhere;
}

.workspace-header dl {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--spacing-sm);
}

.workspace-header dl div {
  padding: var(--spacing-sm);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.workspace-header dt {
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
}

.workspace-header dd {
  overflow-wrap: anywhere;
}

@media (max-width: 833px) {
  .workspace-header dl {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 480px) {
  .workspace-header dl {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

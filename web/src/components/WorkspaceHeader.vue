<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <header class="workspace-header">
    <div class="workspace-header__title">
      <h1>{{ workspace.name }}</h1>
      <p v-if="workspace.state === 'needs_attention'">
        {{ t('workspace.workspaceId', { id: workspace.id }) }}
      </p>
      <p v-else-if="workspace.state === 'published'">
        {{ t('workspace.releaseId', { id: workspace.last_release_id }) }}
      </p>
    </div>
    <dl>
      <div>
        <dt>{{ t('workspace.state') }}</dt>
        <dd>
          <span
            data-state-icon
            aria-hidden="true"
          >{{ stateIcon }}</span>
          {{ stateLabel }}
        </dd>
      </div>
      <div>
        <dt>{{ t('workspace.production') }}</dt>
        <dd>{{ productionMatches ? t('workspace.matchesProduction') : t('workspace.productionChanged') }}</dd>
      </div>
      <div>
        <dt>{{ t('workspace.draft') }}</dt>
        <dd>{{ t('workspace.draftChanges', { count: draftChangeCount }, draftChangeCount) }}</dd>
      </div>
      <div>
        <dt>{{ t('workspace.safetyBoundary') }}</dt>
        <dd>{{ workspace.state === 'published' ? t('workspace.publishedSafety') : t('workspace.uncheckedSafety') }}</dd>
      </div>
    </dl>
    <nav
      v-if="workspace.state === 'ready'"
      class="workspace-header__structured-links"
      :aria-label="t('workspace.structuredConfiguration')"
    >
      <a
        :href="'/config/workspaces/' + workspace.id + '/upstreams'"
        data-structured-link="upstreams"
      >
        {{ t('workspace.manageUpstreams') }}
      </a>
      <a
        :href="'/config/workspaces/' + workspace.id + '/servers'"
        data-structured-link="servers"
      >
        {{ t('workspace.manageServers') }}
      </a>
    </nav>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { WorkspaceDetail } from '../api/types'

const props = defineProps<{
  draftChangeCount: number
  workspace: WorkspaceDetail
}>()

const productionMatches = computed(
  () => props.workspace.production_digest === props.workspace.base_digest,
)
const { t } = useI18n()
const stateLabel = computed(() => {
  switch (props.workspace.state) {
    case 'preparing': return t('workspace.states.preparing')
    case 'ready': return t('workspace.states.ready')
    case 'stale': return t('workspace.states.stale')
	case 'published': return t('workspace.states.published')
    case 'needs_attention': return t('workspace.states.needsAttention')
    default: return ''
  }
})
const stateIcon = computed(() =>
  props.workspace.state === 'ready'
    ? '✓'
    : props.workspace.state === 'preparing'
      ? '◌'
	  : props.workspace.state === 'published'
		? '✓'
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

.workspace-header__structured-links {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: var(--spacing-sm);
}

.workspace-header__structured-links a {
  display: inline-flex;
  min-height: var(--component-control-min-size);
  padding-inline: var(--spacing-md);
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  color: var(--color-primary);
  text-decoration: none;
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

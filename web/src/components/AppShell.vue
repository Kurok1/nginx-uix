<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <div class="app-shell">
    <a
      class="app-shell__skip-link"
      href="#main-content"
    >{{ t('navigation.skipToContent') }}</a>
    <GlobalNav />
    <SubNav />
    <main
      id="main-content"
      class="app-shell__main"
      tabindex="-1"
    >
      <div class="app-shell__content">
        <slot />
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import GlobalNav from './GlobalNav.vue'
import SubNav from './SubNav.vue'

const { t } = useI18n()
</script>

<style scoped>
.app-shell,
.app-shell__main,
.app-shell__content {
  min-width: 0;
}

.app-shell {
  min-height: 100vh;
  overflow-x: hidden;
  background: var(--color-canvas-parchment);
}

.app-shell__skip-link {
  position: fixed;
  z-index: calc(var(--z-index-global-nav) + 1);
  top: var(--spacing-xs);
  left: var(--spacing-xs);
  min-height: var(--component-control-min-size);
  padding: var(--spacing-sm) var(--spacing-md);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
  color: var(--color-primary);
  font-size: var(--font-size-caption);
  line-height: 20px;
  transform: translateY(calc(-100% - var(--spacing-lg)));
}

.app-shell__skip-link:focus-visible {
  transform: translateY(0);
}

.app-shell__main {
  min-height: calc(
    100vh - var(--component-global-nav-height) - var(--component-sub-nav-height)
  );
  scroll-margin-top: calc(
    var(--component-global-nav-height) + var(--component-sub-nav-height) + var(--spacing-md)
  );
}

.app-shell__content {
  width: min(100%, var(--component-content-max-width));
  margin-inline: auto;
  padding: var(--spacing-xl) var(--component-page-gutter) var(--spacing-section);
}

@media (max-width: 640px) {
  .app-shell__content {
    padding-block-start: var(--spacing-lg);
    padding-inline: var(--component-page-gutter-compact);
  }
}

@supports (min-height: 100dvh) {
  .app-shell,
  .app-shell__main {
    min-height: 100dvh;
  }

  .app-shell__main {
    min-height: calc(
      100dvh - var(--component-global-nav-height) - var(--component-sub-nav-height)
    );
  }
}
</style>

<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <nav
    class="sub-nav"
    aria-label="Section navigation"
  >
    <div class="sub-nav__content">
      <span class="sub-nav__title">Operations</span>
      <ul class="sub-nav__links">
        <li>
          <RouterLink to="/">
            Dashboard
          </RouterLink>
        </li>
        <li>
          <RouterLink to="/configuration">
            Configuration
          </RouterLink>
        </li>
        <li>
          <RouterLink
            class="sub-nav__primary"
            to="/config/workspaces"
          >
            Workspaces
          </RouterLink>
        </li>
        <li>
          <RouterLink to="/config/operations">
            Recovery &amp; History
          </RouterLink>
        </li>
      </ul>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<style scoped>
.sub-nav {
  position: sticky;
  z-index: var(--z-index-sub-nav);
  top: var(--component-global-nav-height);
  height: var(--component-sub-nav-height);
  border-bottom: 1px solid var(--color-hairline);
  background: var(--color-canvas-parchment-frosted);
  -webkit-backdrop-filter: blur(20px);
  backdrop-filter: blur(20px);
}

.sub-nav__content {
  display: flex;
  width: min(100%, var(--component-content-max-width));
  height: 100%;
  min-width: 0;
  margin-inline: auto;
  padding-inline: var(--component-page-gutter);
  align-items: center;
  gap: var(--spacing-md);
}

.sub-nav__title {
  min-width: 0;
  overflow: hidden;
  color: var(--color-ink);
  font-size: var(--font-size-tagline);
  font-weight: var(--font-weight-semibold);
  line-height: 1.19;
  letter-spacing: var(--letter-spacing-body);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sub-nav__links {
  display: flex;
  margin: 0 0 0 auto;
  padding: 0;
  align-items: center;
  gap: var(--spacing-sm);
  list-style: none;
}

.sub-nav__links a {
  display: inline-flex;
  min-height: var(--component-control-min-size);
  padding-inline: var(--spacing-sm);
  align-items: center;
  color: var(--color-ink);
  font-size: var(--font-size-caption);
  font-weight: var(--font-weight-regular);
  line-height: 1.29;
  letter-spacing: var(--letter-spacing-caption);
  text-decoration: none;
}

.sub-nav__links a.router-link-exact-active:not(.sub-nav__primary) {
  color: var(--color-primary);
}

.sub-nav__links .sub-nav__primary {
  min-height: var(--component-control-min-size);
  padding-inline: var(--spacing-md);
  border-radius: var(--rounded-pill);
  background: var(--color-primary);
  color: var(--color-body-on-dark);
}

.sub-nav__links .sub-nav__primary:active {
  transform: scale(0.95);
}

@media (max-width: 833px) {
  .sub-nav__content {
    padding-inline: var(--component-page-gutter-compact);
  }

  .sub-nav__links li:first-child {
    display: none;
  }
}
</style>

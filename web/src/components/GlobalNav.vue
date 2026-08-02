<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<template>
  <header
    class="global-nav"
    @keyup.esc="closeMenu"
  >
    <nav
      class="global-nav__content"
      :aria-label="t('navigation.globalLabel')"
    >
      <button
        class="global-nav__menu-toggle"
        type="button"
        :aria-expanded="menuOpen"
        aria-controls="global-nav-menu"
        @click="menuOpen = !menuOpen"
      >
        <span
          class="global-nav__menu-icon"
          aria-hidden="true"
        >
          <span />
          <span />
          <span />
        </span>
        <span class="global-nav__visually-hidden">
          {{ menuOpen ? t('navigation.close') : t('navigation.open') }}
        </span>
      </button>

      <RouterLink
        class="global-nav__brand"
        to="/"
        :aria-label="t('navigation.homeLabel')"
      >
        Nginx UIX
      </RouterLink>

      <ul class="global-nav__links">
        <li>
          <RouterLink to="/">
            {{ t('navigation.dashboard') }}
          </RouterLink>
        </li>
        <li>
          <RouterLink to="/configuration">
            {{ t('navigation.configuration') }}
          </RouterLink>
        </li>
        <li>
          <RouterLink to="/config/workspaces">
            {{ t('navigation.workspaces') }}
          </RouterLink>
        </li>
        <li>
          <RouterLink to="/config/route-lab">
            {{ t('navigation.routeLab') }}
          </RouterLink>
        </li>
        <li>
          <RouterLink to="/certificates">
            {{ t('navigation.certificates') }}
          </RouterLink>
        </li>
        <li>
          <RouterLink to="/config/operations">
            {{ t('navigation.recoveryHistory') }}
          </RouterLink>
        </li>
      </ul>

      <LanguageSelector class="global-nav__language global-nav__language--desktop" />

      <button
        class="global-nav__logout"
        type="button"
        :disabled="logoutPending"
        @click="logout"
      >
        <svg
          aria-hidden="true"
          focusable="false"
          viewBox="0 0 24 24"
        >
          <path d="M10 4H5v16h5M14 8l4 4-4 4M9 12h9" />
        </svg>
        <span>{{ logoutPending ? t('navigation.signingOut') : t('navigation.signOut') }}</span>
      </button>

      <div
        id="global-nav-menu"
        class="global-nav__mobile-menu"
        :hidden="!menuOpen"
      >
        <ul>
          <li>
            <RouterLink
              to="/"
              @click="closeMenu"
            >
              {{ t('navigation.dashboard') }}
            </RouterLink>
          </li>
          <li>
            <RouterLink
              to="/configuration"
              @click="closeMenu"
            >
              {{ t('navigation.configuration') }}
            </RouterLink>
          </li>
          <li>
            <RouterLink
              to="/config/workspaces"
              @click="closeMenu"
            >
              {{ t('navigation.workspaces') }}
            </RouterLink>
          </li>
          <li>
            <RouterLink
              to="/config/route-lab"
              @click="closeMenu"
            >
              {{ t('navigation.routeLab') }}
            </RouterLink>
          </li>
          <li>
            <RouterLink
              to="/certificates"
              @click="closeMenu"
            >
              {{ t('navigation.certificates') }}
            </RouterLink>
          </li>
          <li>
            <RouterLink
              to="/config/operations"
              @click="closeMenu"
            >
              {{ t('navigation.recoveryHistory') }}
            </RouterLink>
          </li>
          <li class="global-nav__language-item">
            <LanguageSelector class="global-nav__language global-nav__language--mobile" />
          </li>
        </ul>
      </div>
    </nav>
  </header>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'

import { sessionStore } from '../session'
import LanguageSelector from './LanguageSelector.vue'

const menuOpen = ref(false)
const logoutPending = ref(false)
const router = useRouter()
const { t } = useI18n()

function closeMenu(): void {
  menuOpen.value = false
}

async function logout(): Promise<void> {
  if (logoutPending.value) {
    return
  }
  logoutPending.value = true
  try {
    await sessionStore.logout()
    await router.replace({ name: 'login' })
  } finally {
    logoutPending.value = false
  }
}
</script>

<style scoped>
.global-nav {
  position: sticky;
  z-index: var(--z-index-global-nav);
  top: 0;
  height: var(--component-global-nav-height);
  background: var(--color-surface-black);
  color: var(--color-body-on-dark);
}

.global-nav__content {
  position: relative;
  display: flex;
  width: min(100%, var(--component-content-max-width));
  height: 100%;
  min-width: 0;
  margin-inline: auto;
  padding-inline: var(--component-page-gutter);
  align-items: center;
  gap: var(--spacing-lg);
}

.global-nav__brand,
.global-nav__links a,
.global-nav__mobile-menu a,
.global-nav__logout {
  display: inline-flex;
  min-height: var(--component-control-min-size);
  align-items: center;
  color: var(--color-body-on-dark);
  font-size: var(--font-size-nav);
  font-weight: var(--font-weight-regular);
  line-height: 1;
  letter-spacing: var(--letter-spacing-nav);
  text-decoration: none;
}

.global-nav__logout {
  flex: 0 0 auto;
  gap: var(--spacing-xs);
  padding-inline: var(--spacing-sm);
  border: 0;
  background: transparent;
  cursor: pointer;
}

.global-nav__logout:active:not(:disabled) {
  transform: scale(0.95);
}

.global-nav__logout:disabled {
  cursor: default;
}

.global-nav__logout svg {
  width: 18px;
  height: 18px;
  fill: none;
  stroke: currentcolor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.8;
}

.global-nav__brand {
  flex: 0 0 auto;
  font-weight: var(--font-weight-semibold);
}

.global-nav__links,
.global-nav__mobile-menu ul {
  display: flex;
  margin: 0;
  padding: 0;
  align-items: center;
  gap: var(--spacing-lg);
  list-style: none;
}

.global-nav__links {
  margin-inline-start: auto;
}

.global-nav__language--mobile {
  display: none;
}

.global-nav__links a:active,
.global-nav__mobile-menu a:active {
  color: var(--color-primary-on-dark);
}

.global-nav__links a {
  min-width: var(--component-control-min-size);
  justify-content: center;
}

.global-nav__links a.router-link-exact-active,
.global-nav__mobile-menu a.router-link-exact-active {
  color: var(--color-primary-on-dark);
}

.global-nav__menu-toggle,
.global-nav__mobile-menu {
  display: none;
}

.global-nav__visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

@media (max-width: 833px) {
  .global-nav__content {
    display: grid;
    grid-template-columns: var(--component-control-min-size) minmax(0, 1fr) var(
        --component-control-min-size
      );
    padding-inline: var(--component-page-gutter-compact);
    gap: var(--spacing-xs);
  }

  .global-nav__brand {
    justify-self: center;
  }

  .global-nav__links {
    display: none;
  }

  .global-nav__language--desktop {
    display: none;
  }

  .global-nav__menu-toggle {
    display: inline-grid;
    width: var(--component-control-min-size);
    height: var(--component-control-min-size);
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--color-body-on-dark);
    cursor: pointer;
    place-items: center;
  }

  .global-nav__logout {
    width: var(--component-control-min-size);
    height: var(--component-control-min-size);
    padding: 0;
    justify-content: center;
    justify-self: end;
  }

  .global-nav__logout span {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  .global-nav__menu-icon {
    display: grid;
    width: 18px;
    gap: var(--spacing-xxs);
  }

  .global-nav__menu-icon span {
    display: block;
    height: 1px;
    background: currentcolor;
  }

  .global-nav__mobile-menu {
    position: absolute;
    top: 100%;
    right: 0;
    left: 0;
    display: block;
    padding: var(--spacing-xs) var(--component-page-gutter-compact) var(--spacing-md);
    background: var(--color-surface-black);
  }

  .global-nav__mobile-menu ul {
    display: grid;
    gap: 0;
  }

  .global-nav__mobile-menu a {
    width: 100%;
    font-size: var(--font-size-caption);
  }

  .global-nav__language-item {
    display: flex;
    padding-block-start: var(--spacing-xs);
  }

  .global-nav__language--mobile {
    display: inline-flex;
  }
}
</style>

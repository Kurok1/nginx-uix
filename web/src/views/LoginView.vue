<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.1.0
-->
<script setup lang="ts">
import { computed, inject, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { routerKey, type RouteLocationRaw } from 'vue-router'

import LanguageSelector from '../components/LanguageSelector.vue'
import LoginForm from '../components/LoginForm.vue'
import UnsavedRecovery from '../components/UnsavedRecovery.vue'
import { sessionStore, type SessionStore } from '../session'
import { workspaceStore, type WorkspaceStore } from '../workspace'

interface Props {
  store?: SessionStore
  workspace?: WorkspaceStore
}

const props = defineProps<Props>()
const router = inject(routerKey, null)
const { t } = useI18n()
const sessions = props.store ?? sessionStore
const workspaces = props.workspace ?? workspaceStore
const dirtyPaths = computed(() =>
  workspaces.state.documents.filter(({ dirty }) => dirty).map(({ path }) => path),
)
let removeReturnGuard: (() => void) | null = null

const loginStore: SessionStore = {
  state: sessions.state,
  handleAPIError: sessions.handleAPIError,
  async login(input) {
    await sessions.login(input)
    await workspaces.markSessionRestored()
    installWorkspaceReturn()
  },
  logout: sessions.logout,
  onExpired: sessions.onExpired,
  restore: sessions.restore,
}

onBeforeUnmount(() => {
  removeReturnGuard?.()
  removeReturnGuard = null
})

function workspaceReturnTarget(): RouteLocationRaw | null {
  if (router === null) return null
  const redirect = router.currentRoute.value.query.redirect
  if (typeof redirect === 'string') {
    const resolved = router.resolve(redirect)
    if (resolved.name === 'config-workspaces' || resolved.name === 'config-operations') {
      return { path: resolved.path, query: resolved.query, hash: resolved.hash }
    }
  }
  if (workspaces.state.active !== null) {
    return {
      name: 'config-workspaces',
      params: { workspaceId: workspaces.state.active.id },
    }
  }
  return null
}

function installWorkspaceReturn(): void {
  const target = workspaceReturnTarget()
  if (router === null || target === null) return
  removeReturnGuard?.()
  removeReturnGuard = router.beforeEach((to) => {
    if (to.name !== 'dashboard') return true
    const remove = removeReturnGuard
    removeReturnGuard = null
    remove?.()
    return target
  })
}
</script>

<template>
  <main
    class="login-view"
    aria-labelledby="login-title"
  >
    <section class="login-view__panel">
      <div class="login-view__language">
        <LanguageSelector />
      </div>
      <header class="login-view__header">
        <h1 id="login-title">
          {{ t('auth.title') }}
        </h1>
        <p>{{ t('auth.description') }}</p>
      </header>
      <UnsavedRecovery
        v-if="dirtyPaths.length > 0"
        :paths="dirtyPaths"
        @copy="workspaces.copyLocalContent"
      />
      <LoginForm :store="loginStore" />
    </section>
  </main>
</template>

<style scoped>
.login-view {
  display: grid;
  min-height: 100vh;
  place-items: center;
  padding: var(--spacing-xl) var(--component-page-gutter);
  background: var(--color-canvas-parchment);
}

.login-view__panel {
  width: min(100%, calc(var(--spacing-section) * 6));
  padding: var(--spacing-xl);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-lg);
  background: var(--color-canvas);
}

.login-view__header {
  margin-block-end: var(--spacing-xl);
}

.login-view__language {
  display: flex;
  margin-block-end: var(--spacing-md);
  justify-content: flex-end;
}

.login-view__panel :deep(.unsaved-recovery) {
  margin-block-end: var(--spacing-xl);
}

.login-view__header h1 {
  margin-block-end: var(--spacing-xs);
  color: var(--color-ink);
  font-size: var(--font-size-tagline);
}

.login-view__header p {
  margin: 0;
  color: var(--color-ink-muted-80);
  font-size: var(--font-size-caption);
  letter-spacing: var(--letter-spacing-caption);
}

@supports (min-height: 100dvh) {
  .login-view {
    min-height: 100dvh;
  }
}
</style>

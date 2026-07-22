/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import {
  createRouter,
  createWebHistory,
  type Router,
  type RouterHistory,
  type RouteRecordRaw,
} from 'vue-router'
import { watch } from 'vue'

import { apiClient, type APIClient } from './api/client'
import { sessionStore, type SessionStore } from './session'
import { workspaceStore, type WorkspaceStore } from './workspace'
import CertificatesView from './views/CertificatesView.vue'
import ConfigWorkspaceView from './views/ConfigWorkspaceView.vue'
import DashboardView from './views/DashboardView.vue'
import EffectiveConfigView from './views/EffectiveConfigView.vue'
import LoginView from './views/LoginView.vue'
import OperationsView from './views/OperationsView.vue'
import RouteLabView from './views/RouteLabView.vue'
import StructuredConfigView from './views/StructuredConfigView.vue'

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth: boolean
  }
}

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'dashboard',
    component: DashboardView,
    meta: { requiresAuth: true },
  },
  {
    path: '/configuration',
    name: 'configuration',
    component: EffectiveConfigView,
    meta: { requiresAuth: true },
  },
  {
    path: '/config/workspaces/:workspaceId/upstreams',
    name: 'structured-upstreams',
    component: StructuredConfigView,
    props: (route) => ({
      workspaceId: String(route.params.workspaceId),
      mode: 'upstreams',
    }),
    meta: { requiresAuth: true },
  },
  {
    path: '/config/workspaces/:workspaceId/servers',
    name: 'structured-servers',
    component: StructuredConfigView,
    props: (route) => ({
      workspaceId: String(route.params.workspaceId),
      mode: 'servers',
    }),
    meta: { requiresAuth: true },
  },
  {
    path: '/config/workspaces/:workspaceId?',
    name: 'config-workspaces',
    component: ConfigWorkspaceView,
    meta: { requiresAuth: true },
  },
  {
    path: '/config/operations',
    name: 'config-operations',
    component: OperationsView,
    meta: { requiresAuth: true },
  },
  {
    path: '/config/route-lab',
    name: 'route-lab',
    component: RouteLabView,
    meta: { requiresAuth: true },
  },
  {
    path: '/certificates/:certificateId?',
    name: 'certificates',
    component: CertificatesView,
    props: (route) => ({
      certificateId: typeof route.params.certificateId === 'string'
        ? route.params.certificateId
        : '',
    }),
    meta: { requiresAuth: true },
  },
  {
    path: '/login',
    name: 'login',
    component: LoginView,
    meta: { requiresAuth: false },
  },
]

export function createAppRouter(
  store: SessionStore = sessionStore,
  history: RouterHistory = createWebHistory(),
): Router {
  const router = createRouter({ history, routes })
  router.beforeEach(async (to) => {
    await store.restore()
    if (store.state.phase === 'authenticated' && to.name === 'login') {
      const redirect = typeof to.query.redirect === 'string' ? to.query.redirect : ''
      if (redirect !== '') {
        const resolved = router.resolve(redirect)
        if (
          resolved.name === 'config-workspaces' ||
          resolved.name === 'config-operations' ||
          resolved.name === 'route-lab' ||
          resolved.name === 'certificates' ||
          resolved.name === 'structured-upstreams' ||
          resolved.name === 'structured-servers'
        ) {
          return { path: resolved.path, query: resolved.query, hash: resolved.hash }
        }
      }
      return { name: 'dashboard' }
    }
    if (store.state.phase !== 'authenticated' && to.meta.requiresAuth) {
      return { name: 'login', query: { redirect: to.fullPath } }
    }
    return true
  })
  return router
}

export function installSessionExpiryRedirect(
  client: APIClient,
  store: SessionStore,
  router: Router,
  workspaces: WorkspaceStore = workspaceStore,
): () => void {
  return client.onError((error) => {
    if (store.handleAPIError(error)) {
      workspaces.markSessionExpired()
      const redirect = router.currentRoute.value.fullPath
      void router.replace({
        name: 'login',
        query: redirect.startsWith('/login') ? undefined : { redirect },
      })
    }
  })
}

export function installWorkspaceLeaveGuard(
  router: Router,
  store: WorkspaceStore,
  confirmLeave: (message: string) => boolean,
): () => void {
  const removeRouteGuard = router.beforeEach((to, from) => {
    if (
      from.name === 'config-workspaces' &&
      to.name !== 'config-workspaces' &&
      store.hasUnsavedChanges() &&
      store.state.banner?.kind !== 'session_expired'
    ) {
      return confirmLeave(
        'Unsaved workspace text will remain only in this browser session. Leave this page?',
      )
    }
    return true
  })

  let beforeUnloadInstalled = false
  const handleBeforeUnload = (event: BeforeUnloadEvent): void => {
    if (!store.hasUnsavedChanges()) return
    event.preventDefault()
    event.returnValue = ''
  }
  const stopWatching = watch(
    () => store.hasUnsavedChanges(),
    (dirty) => {
      if (dirty && !beforeUnloadInstalled) {
        window.addEventListener('beforeunload', handleBeforeUnload)
        beforeUnloadInstalled = true
      } else if (!dirty && beforeUnloadInstalled) {
        window.removeEventListener('beforeunload', handleBeforeUnload)
        beforeUnloadInstalled = false
      }
    },
    { immediate: true },
  )

  return () => {
    removeRouteGuard()
    stopWatching()
    if (beforeUnloadInstalled) {
      window.removeEventListener('beforeunload', handleBeforeUnload)
      beforeUnloadInstalled = false
    }
  }
}

export const appRouter = createAppRouter()
installSessionExpiryRedirect(apiClient, sessionStore, appRouter)
installWorkspaceLeaveGuard(appRouter, workspaceStore, (message) => window.confirm(message))

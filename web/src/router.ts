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

import { apiClient, type APIClient } from './api/client'
import { sessionStore, type SessionStore } from './session'
import DashboardView from './views/DashboardView.vue'
import EffectiveConfigView from './views/EffectiveConfigView.vue'
import LoginView from './views/LoginView.vue'

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
      return { name: 'dashboard' }
    }
    if (store.state.phase !== 'authenticated' && to.meta.requiresAuth) {
      return { name: 'login' }
    }
    return true
  })
  return router
}

export function installSessionExpiryRedirect(
  client: APIClient,
  store: SessionStore,
  router: Router,
): () => void {
  return client.onError((error) => {
    if (store.handleAPIError(error)) {
      void router.replace({ name: 'login' })
    }
  })
}

export const appRouter = createAppRouter()
installSessionExpiryRedirect(apiClient, sessionStore, appRouter)

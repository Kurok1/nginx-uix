/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
/// <reference types="vite/client" />

import type { ComponentInternalInstance } from 'vue'

// vue-i18n 11.4.4 references the Vue 3.6 compatibility name while Vue 3.5
// exports the same runtime concept as ComponentInternalInstance.
declare module 'vue' {
  export type GenericComponentInstance = ComponentInternalInstance
}

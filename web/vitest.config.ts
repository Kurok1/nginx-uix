/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'jsdom',
    globals: true,
    restoreMocks: true,
  },
})

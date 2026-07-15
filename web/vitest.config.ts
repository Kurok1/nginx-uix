/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import vue from '@vitejs/plugin-vue'
import { configDefaults, defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'jsdom',
    exclude: [...configDefaults.exclude, 'e2e/**'],
    execArgv: ['--no-experimental-webstorage'],
    globals: true,
    restoreMocks: true,
  },
})

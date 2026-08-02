/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { createApp } from 'vue'

import App from './App.vue'
import { appI18n } from './i18n'
import { appRouter } from './router'
import './styles/tokens.css'
import './styles/base.css'

createApp(App).use(appI18n).use(appRouter).mount('#app')

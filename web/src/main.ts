/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { createApp } from 'vue'

import App from './App.vue'
import { appRouter } from './router'
import './styles/tokens.css'
import './styles/base.css'

createApp(App).use(appRouter).mount('#app')

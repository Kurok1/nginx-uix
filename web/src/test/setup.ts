/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 1.1.0
 */
import { config } from '@vue/test-utils'

import { appI18n } from '../i18n'

config.global.plugins = [appI18n]

beforeEach(() => {
  appI18n.global.locale.value = 'zh-CN'
  document.documentElement.lang = 'zh-CN'
})

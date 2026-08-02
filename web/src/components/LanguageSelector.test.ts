/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 1.1.0
 */
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import { createAppI18n, installLocaleRouting } from '../i18n'
import LanguageSelector from './LanguageSelector.vue'
import languageSelectorSource from './LanguageSelector.vue?raw'

describe('LanguageSelector', () => {
  async function mountSelector(locale: 'zh-CN' | 'en-US' = 'en-US') {
    const i18n = createAppI18n(locale)
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div />' } }],
    })
    installLocaleRouting(router, i18n, localStorage)
    await router.push(`/?lang=${locale}`)
    await router.isReady()
    const wrapper = mount(LanguageSelector, {
      global: { plugins: [i18n, router] },
    })
    return { i18n, router, wrapper }
  }

  beforeEach(() => {
    localStorage.clear()
  })

  it('offers self-named canonical language options with an accessible label', async () => {
    const { wrapper } = await mountSelector()
    const select = wrapper.get('select')

    expect(select.attributes('aria-label')).toBe('Language')
    expect(select.element).toHaveProperty('value', 'en-US')
    expect(wrapper.findAll('option').map((option) => option.text())).toEqual([
      '简体中文',
      'English',
    ])
  })

  it('switches in place and updates the URL, document and persisted preference', async () => {
    const { i18n, router, wrapper } = await mountSelector()
    const replace = vi.spyOn(router, 'replace')

    await wrapper.get('select').setValue('zh-CN')
    await flushPromises()

    expect(replace).toHaveBeenCalledOnce()
    expect(router.currentRoute.value.fullPath).toBe('/?lang=zh-CN')
    expect(i18n.global.locale.value).toBe('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')
    expect(localStorage.getItem('nginx-uix.locale')).toBe('zh-CN')
    expect(wrapper.get('select').attributes('aria-label')).toBe('语言')
  })

  it('uses the shared minimum touch target token', () => {
    expect(languageSelectorSource).toContain('min-height: var(--component-control-min-size)')
  })
})

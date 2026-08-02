/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 1.1.0
 */
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import { appI18n } from '../i18n'
import AppShell from './AppShell.vue'

describe('AppShell locale coverage', () => {
  it.each([
    {
      locale: 'en-US' as const,
      globalNavigation: 'Global navigation',
      sectionNavigation: 'Section navigation',
      skip: 'Skip to main content',
      dashboard: 'Dashboard',
      configuration: 'Configuration',
      recovery: 'Recovery & History',
      logout: 'Sign out',
    },
    {
      locale: 'zh-CN' as const,
      globalNavigation: '全局导航',
      sectionNavigation: '分区导航',
      skip: '跳转到主要内容',
      dashboard: '仪表盘',
      configuration: '生效配置',
      recovery: '恢复与历史',
      logout: '退出登录',
    },
  ])('renders the complete shell in $locale', async (expected) => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }],
    })
    await router.push('/')
    appI18n.global.locale.value = expected.locale
    const wrapper = mount(AppShell, {
      slots: { default: '<p>page content</p>' },
      global: { plugins: [router] },
    })

    expect(wrapper.get('.app-shell__skip-link').text()).toBe(expected.skip)
    expect(wrapper.get('.global-nav__content').attributes('aria-label')).toBe(
      expected.globalNavigation,
    )
    expect(wrapper.get('.sub-nav').attributes('aria-label')).toBe(expected.sectionNavigation)
    expect(wrapper.text()).toContain(expected.dashboard)
    expect(wrapper.text()).toContain(expected.configuration)
    expect(wrapper.text()).toContain(expected.recovery)
    expect(wrapper.get('.global-nav__logout').text()).toContain(expected.logout)
  })
})

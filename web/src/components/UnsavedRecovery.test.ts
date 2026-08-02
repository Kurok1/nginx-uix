/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { mount } from '@vue/test-utils'

import { appI18n } from '../i18n'
import UnsavedRecovery from './UnsavedRecovery.vue'
import recoverySource from './UnsavedRecovery.vue?raw'

describe('UnsavedRecovery', () => {
  it('lists only dirty paths with explicit copy controls and no document content', () => {
    const wrapper = mount(UnsavedRecovery, {
      props: {
        paths: ['conf.d/site.conf', 'nginx.conf'],
      },
    })

    expect(wrapper.get('section').attributes('aria-labelledby')).toBe(
      wrapper.get('h2').attributes('id'),
    )
    expect(wrapper.text()).toContain('conf.d/site.conf')
    expect(wrapper.text()).toContain('nginx.conf')
    expect(wrapper.findAll('button')).toHaveLength(2)
    expect(
      wrapper.get('button[aria-label="复制 conf.d/site.conf 的本地内容"]').text(),
    ).toBe('复制')
    expect(wrapper.text()).not.toContain('server {')
    expect(wrapper.find('pre').exists()).toBe(false)
    expect(wrapper.find('code').exists()).toBe(false)
    expect(wrapper.find('textarea').exists()).toBe(false)
  })

  it('emits the selected path for an external clipboard callback and never submits', async () => {
    const wrapper = mount(UnsavedRecovery, {
      props: { paths: ['conf.d/site.conf'] },
    })

    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('copy')).toEqual([['conf.d/site.conf']])
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.find('[type="submit"]').exists()).toBe(false)
  })

  it.each([
    ['zh-CN' as const, '未保存的工作区变更', '复制 conf.d/site.conf 的本地内容', '复制'],
    ['en-US' as const, 'Unsaved workspace changes', 'Copy local content for conf.d/site.conf', 'Copy'],
  ])('localizes recovery controls in %s', (locale, heading, ariaLabel, buttonText) => {
    appI18n.global.locale.value = locale
    const wrapper = mount(UnsavedRecovery, { props: { paths: ['conf.d/site.conf'] } })

    expect(wrapper.get('h2').text()).toBe(heading)
    expect(wrapper.get('button').attributes('aria-label')).toBe(ariaLabel)
    expect(wrapper.get('button').text()).toBe(buttonText)
  })

  it('uses 44px controls, variables and no persistence or API surface', () => {
    expect(recoverySource).toContain('min-height: var(--component-control-min-size)')
    expect(recoverySource).toContain('min-width: var(--component-control-min-size)')
    expect(recoverySource).not.toMatch(
      /\b(?:fetch|XMLHttpRequest|localStorage|sessionStorage|indexedDB|caches)\b/,
    )
    expect(recoverySource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(recoverySource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(recoverySource).not.toContain('box-shadow')
  })
})

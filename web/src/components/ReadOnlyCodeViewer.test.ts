/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { mount } from '@vue/test-utils'

import type { EffectiveConfigOccurrence } from '../api/types'
import ReadOnlyCodeViewer from './ReadOnlyCodeViewer.vue'
import viewerSource from './ReadOnlyCodeViewer.vue?raw'

const occurrence: EffectiveConfigOccurrence = {
  id: 'occurrence-000002',
  load_order: 2,
  path: '/etc/nginx/conf.d/site.conf',
  content: 'server {\n  location /literal { return 200 "<script>&"; }\n}\n',
}

describe('ReadOnlyCodeViewer', () => {
  it('renders selectable escaped content unchanged with a separate hidden line-number pre', () => {
    const wrapper = mount(ReadOnlyCodeViewer, { props: { occurrence } })
    const scrollContainer = wrapper.get('.read-only-code-viewer__scroll')
    const code = scrollContainer.get('pre code')
    const lineNumbers = scrollContainer.get('pre[aria-hidden="true"]')

    expect(scrollContainer.attributes('tabindex')).toBe('0')
    expect(scrollContainer.attributes('role')).toBe('region')
    expect(scrollContainer.attributes('aria-labelledby')).toBeTruthy()
    expect(code.element.textContent).toBe(occurrence.content)
    expect(lineNumbers.element.textContent).toBe('1\n2\n3\n4')
    expect(scrollContainer.findAll('pre')).toHaveLength(2)
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.html()).toContain('&lt;script&gt;&amp;')
  })

  it('is one keyboard-focusable scroll region with visible file and order metadata', () => {
    const wrapper = mount(ReadOnlyCodeViewer, {
      props: { occurrence },
      attachTo: document.body,
    })
    const scrollContainer = wrapper.get('.read-only-code-viewer__scroll')

    expect(wrapper.text()).toContain('/etc/nginx/conf.d/site.conf')
    expect(wrapper.text()).toContain('第 2 项')
    expect(wrapper.findAll('[tabindex="0"]')).toHaveLength(1)
    const scrollElement = scrollContainer.element
    if (!(scrollElement instanceof HTMLElement)) {
      throw new Error('scroll container is not focusable HTML')
    }
    scrollElement.focus()
    expect(document.activeElement).toBe(scrollElement)
    wrapper.unmount()
  })

  it('defaults to no-wrap and keeps wrapping as an in-memory viewing toggle', async () => {
    const wrapper = mount(ReadOnlyCodeViewer, { props: { occurrence } })
    const button = wrapper.get('button[aria-pressed]')
    const scrollContainer = wrapper.get('.read-only-code-viewer__scroll')

    expect(button.attributes('aria-pressed')).toBe('false')
    expect(scrollContainer.classes()).not.toContain('read-only-code-viewer__scroll--wrap')
    expect(wrapper.get('code').element.textContent).toBe(occurrence.content)

    await button.trigger('click')

    expect(button.attributes('aria-pressed')).toBe('true')
    expect(scrollContainer.classes()).toContain('read-only-code-viewer__scroll--wrap')
    expect(wrapper.get('code').element.textContent).toBe(occurrence.content)
  })

  it('contains no editor, canvas, persistence, save or server-copy surface', () => {
    const wrapper = mount(ReadOnlyCodeViewer, { props: { occurrence } })

    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.find('[contenteditable]').exists()).toBe(false)
    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(wrapper.find('[role="textbox"]').exists()).toBe(false)
    expect(wrapper.findAll('button')).toHaveLength(1)
    expect(viewerSource).toContain('overflow: auto')
    expect(viewerSource).toContain('white-space: pre')
    expect(viewerSource).toContain('user-select: text')
    expect(viewerSource).toContain('min-height: var(--component-control-min-size)')
    expect(viewerSource).not.toMatch(/<(?:textarea|canvas)\b|contenteditable\s*=/i)
    expect(viewerSource).not.toMatch(
      /\b(?:localStorage|sessionStorage|indexedDB|caches|fetch)\b/,
    )
    expect(viewerSource).not.toMatch(/\b(?:save|upload|persist|copy-to-server)\b/i)
    expect(viewerSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(viewerSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(viewerSource).not.toContain('box-shadow')
    expect(viewerSource).not.toMatch(/font-weight:\s*500\b/)
  })
})

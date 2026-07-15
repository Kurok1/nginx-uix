/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { mount } from '@vue/test-utils'

import StatusBadge from './StatusBadge.vue'
import statusBadgeSource from './StatusBadge.vue?raw'

describe('StatusBadge', () => {
  it.each([
    ['success', '正常', 'circle'],
    ['warning', '降级', 'triangle'],
    ['error', '不可用', 'octagon'],
    ['unknown', '未知', 'diamond'],
  ] as const)('renders %s with visible text and its distinct %s cue', (tone, label, shape) => {
    const wrapper = mount(StatusBadge, { props: { tone, label } })
    const badge = wrapper.get('.status-badge')

    expect(badge.text()).toBe(label)
    expect(badge.attributes('aria-label')).toBe(label)
    const icon = badge.get(`svg[data-shape="${shape}"]`)
    expect(icon.attributes('aria-hidden')).toBe('true')
    expect(icon.attributes('focusable')).toBe('false')
  })

  it('is a static non-interactive label using only shared semantic tokens', () => {
    const wrapper = mount(StatusBadge, { props: { tone: 'success', label: '运行中' } })

    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.get('.status-badge').attributes('role')).toBeUndefined()
    expect(statusBadgeSource).toContain('var(--color-status-success-foreground)')
    expect(statusBadgeSource).toContain('var(--color-status-warning-foreground)')
    expect(statusBadgeSource).toContain('var(--color-status-error-foreground)')
    expect(statusBadgeSource).toContain('var(--color-status-unknown-foreground)')
    expect(statusBadgeSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(statusBadgeSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(statusBadgeSource).not.toContain('box-shadow')
    expect(statusBadgeSource).not.toMatch(/font-weight:\s*500\b/)
  })
})

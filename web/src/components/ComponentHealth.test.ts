/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { mount } from '@vue/test-utils'

import ComponentHealth from './ComponentHealth.vue'
import StatusBadge from './StatusBadge.vue'

describe('ComponentHealth', () => {
  it.each([
    ['healthy', '正常', 'success'],
    ['running', '运行中', 'success'],
    ['degraded', '降级', 'warning'],
    ['stopped', '已停止', 'error'],
    ['unknown', '未知', 'unknown'],
    ['unavailable', '不可用', 'error'],
  ] as const)('maps %s to the explicit %s state', (state, label, tone) => {
    const wrapper = mount(ComponentHealth, {
      props: { name: 'Nginx', state },
    })

    expect(wrapper.get('h3').text()).toBe('Nginx')
    expect(wrapper.getComponent(StatusBadge).props()).toMatchObject({ label, tone })
    expect(wrapper.text()).toContain(label)
  })

  it('labels the card from its visible heading and exposes no action', () => {
    const wrapper = mount(ComponentHealth, {
      props: { name: 'Agent', state: 'unavailable' },
    })
    const heading = wrapper.get('h3')

    expect(wrapper.get('article').attributes('aria-labelledby')).toBe(heading.attributes('id'))
    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.find('a').exists()).toBe(false)
  })
})

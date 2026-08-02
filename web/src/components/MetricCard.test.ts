/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { mount } from '@vue/test-utils'

import { appI18n } from '../i18n'
import MetricCard from './MetricCard.vue'
import metricCardSource from './MetricCard.vue?raw'

describe('MetricCard', () => {
  it('renders a labelled numeric metric and supporting copy as description data', () => {
    const wrapper = mount(MetricCard, {
      props: {
        label: 'Worker 数量',
        value: 1234,
        format: 'number',
        supportingText: '已验证的 Worker 进程',
      },
    })

    expect(wrapper.get('dt').text()).toBe('Worker 数量')
    expect(wrapper.get('dd').text()).toBe('1,234')
    expect(wrapper.get('.metric-card__supporting').text()).toBe('已验证的 Worker 进程')
  })

  it('renders null as unable to confirm instead of zero', () => {
    const wrapper = mount(MetricCard, {
      props: { label: 'Master PID', value: null, format: 'number' },
    })

    expect(wrapper.get('dd').text()).toBe('无法确认')
    expect(wrapper.text()).not.toContain('0')
  })

  it('uses the active locale for fallback text and timestamp formatting', () => {
    appI18n.global.locale.value = 'en-US'
    const missing = mount(MetricCard, {
      props: { label: 'Started at', value: null, format: 'timestamp' },
    })
    const timestamp = mount(MetricCard, {
      props: { label: 'Started at', value: '2026-01-02T03:04:00Z', format: 'timestamp' },
    })

    expect(missing.get('dd').text()).toBe('Unable to confirm')
    expect(timestamp.get('time').text()).toContain('Jan')
  })

  it('renders an RFC 3339 timestamp with native time semantics', () => {
    const value = '2026-07-15T08:00:00Z'
    const wrapper = mount(MetricCard, {
      props: { label: '启动时间', value, format: 'timestamp' },
    })

    const time = wrapper.get('time')
    expect(time.attributes('datetime')).toBe(value)
    expect(time.text()).toContain('2026')
  })

  it('keeps long runtime values inside a flat tokenized card', () => {
    const value = '/a/runtime/path/that/does/not/have/any/natural/wrapping/opportunity'
    const wrapper = mount(MetricCard, { props: { label: 'PID 路径', value } })

    expect(wrapper.get('dd').text()).toBe(value)
    expect(metricCardSource).toContain('overflow-wrap: anywhere')
    expect(metricCardSource).toContain('min-width: 0')
    expect(metricCardSource).toContain('border: 1px solid var(--color-hairline)')
    expect(metricCardSource).toContain('border-radius: var(--rounded-lg)')
    expect(metricCardSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(metricCardSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(metricCardSource).not.toContain('box-shadow')
    expect(metricCardSource).not.toMatch(/font-weight:\s*500\b/)
  })

  it('scopes busy semantics to the card', () => {
    const wrapper = mount(MetricCard, {
      props: { label: 'Nginx 版本', value: '1.30.3', busy: true },
    })

    expect(wrapper.get('.metric-card').attributes('aria-busy')).toBe('true')
  })
})

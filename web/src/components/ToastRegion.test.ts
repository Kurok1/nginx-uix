/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { mount } from '@vue/test-utils'

import ToastRegion from './ToastRegion.vue'
import toastSource from './ToastRegion.vue?raw'

describe('ToastRegion', () => {
  it('announces only the newest three non-critical success messages politely', () => {
    const wrapper = mount(ToastRegion, {
      props: {
        toasts: [
          { id: '1', message: 'First saved' },
          { id: '2', message: 'Second saved' },
          { id: '3', message: 'Third saved' },
          { id: '4', message: 'Fourth saved' },
        ],
      },
    })
    const region = wrapper.get('[aria-live="polite"]')

    expect(region.attributes('aria-label')).toBe('Success notifications')
    expect(wrapper.findAll('.toast-region__item')).toHaveLength(3)
    expect(wrapper.text()).not.toContain('First saved')
    expect(wrapper.text()).toContain('Second saved')
    expect(wrapper.text()).toContain('Third saved')
    expect(wrapper.text()).toContain('Fourth saved')
    expect(wrapper.findAll('[data-icon="success"][aria-hidden="true"]')).toHaveLength(3)
  })

  it('dismisses each visible toast after exactly five seconds with a deterministic clock', async () => {
    vi.useFakeTimers()
    try {
      const wrapper = mount(ToastRegion, {
        props: { toasts: [{ id: 'saved-1', message: 'File saved' }] },
      })

      await vi.advanceTimersByTimeAsync(4_999)
      expect(wrapper.text()).toContain('File saved')
      expect(wrapper.emitted('dismiss')).toBeUndefined()

      await vi.advanceTimersByTimeAsync(1)
      expect(wrapper.text()).not.toContain('File saved')
      expect(wrapper.emitted('dismiss')).toEqual([['saved-1']])
    } finally {
      vi.useRealTimers()
    }
  })

  it('drops overflow instead of resurfacing an older message after visible items dismiss', async () => {
    vi.useFakeTimers()
    try {
      const wrapper = mount(ToastRegion, {
        props: {
          toasts: [
            { id: '1', message: 'Old overflow' },
            { id: '2', message: 'Second saved' },
            { id: '3', message: 'Third saved' },
            { id: '4', message: 'Fourth saved' },
          ],
        },
      })

      await vi.advanceTimersByTimeAsync(5_000)
      expect(wrapper.text()).not.toContain('Old overflow')
      expect(wrapper.findAll('.toast-region__item')).toHaveLength(0)
      expect(wrapper.emitted('dismiss')?.flat()).toContain('1')
    } finally {
      vi.useRealTimers()
    }
  })

  it('clears pending timers on unmount and exposes no critical severity input', () => {
    vi.useFakeTimers()
    try {
      const wrapper = mount(ToastRegion, {
        props: { toasts: [{ id: 'saved-1', message: 'File saved' }] },
      })
      wrapper.unmount()
      vi.advanceTimersByTime(5_000)
      expect(wrapper.emitted('dismiss')).toBeUndefined()
    } finally {
      vi.useRealTimers()
    }

    expect(toastSource).not.toMatch(/\b(?:conflict|stale|needs_attention|agent)\b/)
    expect(toastSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(toastSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(toastSource).not.toContain('box-shadow')
  })
})

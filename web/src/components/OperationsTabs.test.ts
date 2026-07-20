/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import OperationsTabs from './OperationsTabs.vue'

describe('OperationsTabs', () => {
  it('exposes a semantic tablist and supports the keyboard tab pattern', async () => {
    const wrapper = mount(OperationsTabs, { props: { active: 'overview' }, attachTo: document.body })
    const tabs = wrapper.findAll('[role="tab"]')

    expect(wrapper.get('[role="tablist"]').attributes('aria-label')).toBe('Recovery and history tasks')
    expect(tabs.map((tab) => tab.text())).toEqual(['Overview', 'Backups', 'History', 'Audit'])
    expect(tabs[0]?.attributes('aria-selected')).toBe('true')

    await tabs[0]?.trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.emitted('select')?.at(-1)).toEqual(['backups'])
    expect(document.activeElement).toBe(tabs[1]?.element)

    await tabs[1]?.trigger('keydown', { key: 'End' })
    expect(wrapper.emitted('select')?.at(-1)).toEqual(['audit'])
    expect(document.activeElement).toBe(tabs[3]?.element)

    await tabs[3]?.trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.emitted('select')?.at(-1)).toEqual(['overview'])
    expect(document.activeElement).toBe(tabs[0]?.element)
    wrapper.unmount()
  })
})

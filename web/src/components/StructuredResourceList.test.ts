/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import { mount } from '@vue/test-utils'

import StructuredResourceList from './StructuredResourceList.vue'

describe('StructuredResourceList', () => {
  it('exposes visible state and supports listbox arrow, Home and End navigation', async () => {
    const wrapper = mount(StructuredResourceList, {
      props: {
        label: 'Upstreams',
        resources: [
          { id: 'a', label: 'alpha', meta: '2 servers', editable: true, problem: false },
          { id: 'b', label: 'beta', meta: 'Read only', editable: false, problem: true },
          { id: 'c', label: 'gamma', meta: 'No references', editable: true, problem: false },
        ],
        selectedId: 'a',
      },
    })

    const options = wrapper.get('[role="listbox"]').findAll('[role="option"]')
    expect(options).toHaveLength(3)
    expect(options[1]!.text()).toContain('Read only')
    expect(options[1]!.text()).toContain('Attention')

    await options[0]!.trigger('keydown', { key: 'ArrowDown' })
    await options[1]!.trigger('keydown', { key: 'End' })
    await options[2]!.trigger('keydown', { key: 'Home' })

    expect(wrapper.emitted('select')).toEqual([['b'], ['c'], ['a']])
  })
})

/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import { mount } from '@vue/test-utils'

import type { StructuredLocation } from '../api/structured'
import LocationTree from './LocationTree.vue'

const source = {
  path: 'conf.d/site.conf',
  start_line: 1,
  start_column: 1,
  end_line: 3,
  end_column: 2,
}

function location(
  id: string,
  matcher: string,
  children: StructuredLocation[] = [],
): StructuredLocation {
  return {
    id,
    type: 'prefix',
    matcher,
    source,
    children,
    proxy_passes: [],
    unknown_directive_count: 0,
    editable: true,
    proxy_pass_editable: true,
    instances: 1,
  }
}

describe('LocationTree', () => {
  it('uses the ARIA tree keyboard model and retains the complete matcher name', async () => {
    const wrapper = mount(LocationTree, {
      props: {
        locations: [location('parent', '/very/long/parent', [location('child', '/child')])],
        selectedId: 'parent',
      },
    })

    const parent = wrapper.get('[role="treeitem"]')
    expect(parent.attributes('aria-label')).toContain('Prefix location /very/long/parent')
    expect(parent.attributes('aria-expanded')).toBe('true')

    await parent.trigger('keydown', { key: 'ArrowDown' })
    expect(wrapper.emitted('select')).toEqual([['child']])

    await parent.trigger('keydown', { key: 'ArrowLeft' })
    expect(wrapper.findAll('[role="treeitem"]')).toHaveLength(1)
    expect(parent.attributes('aria-expanded')).toBe('false')
  })
})

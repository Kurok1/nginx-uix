/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { mount } from '@vue/test-utils'

import type { ConfigDependency, DiffResponse, SearchResponse } from '../api/types'
import ConfigReview from './ConfigReview.vue'
import reviewSource from './ConfigReview.vue?raw'

const incompleteDiff: DiffResponse = {
  files: [
    {
      path: 'conf.d/site.conf',
      status: 'modified',
      added_lines: 1,
      removed_lines: 1,
    },
  ],
  complete: false,
  reason: 'response_limit',
  patch: '@@ -1,2 +1,2 @@\n-server old;\n+server new;\n context;\n',
}

const incompleteSearch: SearchResponse = {
  matches: [{ path: 'conf.d/site.conf', line: 3, column: 2, snippet: 'listen 443;' }],
  complete: false,
}

const dependencies: ConfigDependency[] = [
  {
    source: 'nginx.conf',
    line: 4,
    column: 3,
    display_value: 'conf.d/*.conf',
    status: 'resolved',
    cycle: false,
    target: 'conf.d/site.conf',
  },
]

describe('ConfigReview', () => {
  it('renders per-file summaries, response-limit state and labelled unified lines', () => {
    const wrapper = mount(ConfigReview, {
      props: {
        diff: incompleteDiff,
        search: null,
        dependencies,
        selectedPath: 'conf.d/site.conf',
        pending: false,
      },
    })

    expect(wrapper.text()).toContain('conf.d/site.conf')
    expect(wrapper.text()).toContain('+1')
    expect(wrapper.text()).toContain('−1')
    expect(wrapper.text()).toContain('Diff incomplete: response limit reached')
    expect(wrapper.get('[data-diff-line="removed"]').text()).toContain('−')
    expect(wrapper.get('[data-diff-line="added"]').text()).toContain('+')
    expect(wrapper.get('[data-diff-line="context"]').text()).toContain('Context')
    expect(wrapper.get('.config-review__patch').attributes('tabindex')).toBe('0')
  })

  it('requests current/all diff and renders include dependencies in its own tab', async () => {
    const wrapper = mount(ConfigReview, {
      props: {
        diff: incompleteDiff,
        search: null,
        dependencies,
        selectedPath: 'conf.d/site.conf',
        pending: false,
      },
    })

    await wrapper.get('button[aria-label="Review current file diff"]').trigger('click')
    await wrapper.get('button[aria-label="Review all file diffs"]').trigger('click')
    expect(wrapper.emitted('request-diff')).toEqual([['conf.d/site.conf'], []])
    expect(wrapper.get('.config-review__tabs').attributes('role')).toBe('group')

    await wrapper.get('button[aria-label="Review include dependencies"]').trigger('click')
    expect(wrapper.text()).toContain('nginx.conf:4:3')
    expect(wrapper.text()).toContain('conf.d/*.conf')
    expect(wrapper.text()).toContain('Resolved')
  })

  it('submits search and marks an incomplete result without hiding matches', async () => {
    const wrapper = mount(ConfigReview, {
      props: {
        diff: null,
        search: incompleteSearch,
        dependencies: [],
        selectedPath: null,
        pending: false,
      },
    })

    await wrapper.get('button[aria-label="Search workspace files"]').trigger('click')
    await wrapper.get('input[name="workspace-search"]').setValue('listen 443')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('search')).toEqual([['listen 443']])
    expect(wrapper.text()).toContain('Search incomplete')
    expect(wrapper.text()).toContain('listen 443;')
    await wrapper.get('button[aria-label="Open search match conf.d/site.conf line 3"]').trigger(
      'click',
    )
    expect(wrapper.emitted('select')).toEqual([['conf.d/site.conf']])
  })

  it('keeps scrolling internal and has no API, persistence or forbidden CSS', () => {
    expect(reviewSource).toContain('overflow-x: auto')
    expect(reviewSource).toContain('min-height: var(--component-control-min-size)')
    expect(reviewSource).not.toMatch(/\b(?:apiClient|fetch|XMLHttpRequest)\b/)
    expect(reviewSource).not.toMatch(/\b(?:localStorage|sessionStorage|indexedDB|caches)\b/)
    expect(reviewSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(reviewSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(reviewSource).not.toContain('box-shadow')
  })
})

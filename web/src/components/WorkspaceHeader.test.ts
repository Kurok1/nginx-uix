/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { mount } from '@vue/test-utils'

import type { WorkspaceDetail } from '../api/types'
import WorkspaceHeader from './WorkspaceHeader.vue'
import headerSource from './WorkspaceHeader.vue?raw'

const digestA = 'a'.repeat(64)
const digestB = 'b'.repeat(64)

function workspace(overrides: Partial<WorkspaceDetail> = {}): WorkspaceDetail {
  return {
    id: 'workspace-id',
    name: 'Review changes',
    state: 'ready',
    production_digest: digestA,
    base_digest: digestA,
    draft_etag: `"draft-v1:${digestA}"`,
    entry_count: 2,
    managed_bytes: 32,
    workspace_bytes: 128,
    created_by: 7,
    created_at: '2026-07-17T08:00:00Z',
    updated_at: '2026-07-17T08:01:00Z',
    ...overrides,
  }
}

describe('WorkspaceHeader', () => {
  it('shows name, state, production match, draft count and persistent validation boundary', () => {
    const wrapper = mount(WorkspaceHeader, {
      props: { workspace: workspace(), draftChangeCount: 3 },
    })

    expect(wrapper.get('h1').text()).toBe('Review changes')
    expect(wrapper.text()).toContain('Ready')
    expect(wrapper.text()).toContain('Matches production snapshot')
    expect(wrapper.text()).toContain('3 draft changes')
    expect(wrapper.text()).toContain('尚未执行 Nginx 校验')
    expect(wrapper.find('[data-state-icon][aria-hidden="true"]').exists()).toBe(true)
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it('makes stale and needs-attention state visible without relying on color', async () => {
    const wrapper = mount(WorkspaceHeader, {
      props: {
        workspace: workspace({ state: 'stale', production_digest: digestB }),
        draftChangeCount: 0,
      },
    })

    expect(wrapper.text()).toContain('Stale')
    expect(wrapper.text()).toContain('Production changed')
    await wrapper.setProps({ workspace: workspace({ state: 'needs_attention' }) })
    expect(wrapper.text()).toContain('Needs attention')
    expect(wrapper.text()).toContain('Workspace ID: workspace-id')
  })

	it('shows the durable release identity for a published workspace', () => {
		const wrapper = mount(WorkspaceHeader, {
			props: {
				workspace: workspace({
					state: 'published',
					last_release_id: '22222222222222222222222222222222',
				}),
				draftChangeCount: 1,
			},
		})
		expect(wrapper.text()).toContain('Published')
		expect(wrapper.text()).toContain('22222222222222222222222222222222')
		expect(wrapper.text()).toContain('发布校验与运行确认已完成')
	})

  it('contains no production operation action or forbidden business CSS', () => {
    expect(headerSource).not.toMatch(/<button[\s\S]*?(?:validate|publish|reload|restart|restore)/i)
    expect(headerSource).not.toMatch(/\b(?:apiClient|fetch|XMLHttpRequest)\b/)
    expect(headerSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(headerSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(headerSource).not.toContain('box-shadow')
  })
})

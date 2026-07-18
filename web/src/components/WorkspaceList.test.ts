/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { mount } from '@vue/test-utils'

import type { WorkspaceSummary } from '../api/types'
import WorkspaceList from './WorkspaceList.vue'
import listSource from './WorkspaceList.vue?raw'

const digest = 'a'.repeat(64)

function workspace(id: string, name: string, state: WorkspaceSummary['state']): WorkspaceSummary {
  return {
    id,
    name,
    state,
    production_digest: digest,
    base_digest: digest,
    draft_etag: `"draft-v1:${digest}"`,
    entry_count: 2,
    managed_bytes: 32,
    workspace_bytes: 128,
    created_by: 7,
    created_at: '2026-07-17T08:00:00Z',
    updated_at: '2026-07-17T08:01:00Z',
  }
}

describe('WorkspaceList', () => {
  it('renders a bounded selected list and preparing status with text plus icon', async () => {
    const ready = workspace('ready-id', 'Ready changes', 'ready')
    const preparing = workspace('preparing-id', 'Preparing changes', 'preparing')
    const wrapper = mount(WorkspaceList, {
      props: {
        workspaces: [ready, preparing],
        selectedId: ready.id,
        pendingAction: null,
      },
    })

    expect(wrapper.findAll('li')).toHaveLength(2)
    expect(wrapper.get('[data-workspace-id="ready-id"] a').attributes('aria-current')).toBe(
      'page',
    )
    const preparingRow = wrapper.get('[data-workspace-id="preparing-id"]')
    expect(preparingRow.text()).toContain('Preparing')
    expect(preparingRow.find('[data-state-icon][aria-hidden="true"]').exists()).toBe(true)
    await preparingRow.get('a').trigger('click')
    expect(wrapper.emitted('select')).toEqual([['preparing-id']])
  })

  it('opens, cancels and submits the create form without pagination', async () => {
    const wrapper = mount(WorkspaceList, {
      props: { workspaces: [], selectedId: null, pendingAction: null },
    })

    expect(wrapper.text()).toContain('No workspaces yet')
    await wrapper.get('button[aria-label="Create workspace"]').trigger('click')
    const input = wrapper.get('input[name="workspace-name"]')
    expect(input.attributes('aria-label')).toBe('Workspace name')
    expect(wrapper.get('button[type="submit"]').attributes()).toHaveProperty('disabled')
    await input.setValue('Review changes')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('create')).toEqual([['Review changes']])

    await wrapper.get('button[aria-label="Cancel workspace creation"]').trigger('click')
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.find('[aria-label*="pagination" i]').exists()).toBe(false)
  })

  it('shows pending creation and emits a named-delete request', async () => {
    const item = workspace('workspace-id', 'Review changes', 'ready')
    const wrapper = mount(WorkspaceList, {
      props: {
        workspaces: [item],
        selectedId: item.id,
        pendingAction: { kind: 'create_workspace' },
      },
    })

    expect(wrapper.text()).toContain('Creating workspace…')
    expect(wrapper.get('button[aria-label="Create workspace"]').attributes()).toHaveProperty(
      'disabled',
    )
    await wrapper.get('button[aria-label="Delete workspace Review changes"]').trigger('click')
    expect(wrapper.emitted('request-delete')).toEqual([[item]])
  })

  it('uses 44px controls and has no API, persistence or forbidden business CSS', () => {
    expect(listSource).toContain('min-height: var(--component-control-min-size)')
    expect(listSource).toContain('min-width: var(--component-control-min-size)')
    expect(listSource).not.toMatch(/\b(?:apiClient|fetch|XMLHttpRequest)\b/)
    expect(listSource).not.toMatch(/\b(?:localStorage|sessionStorage|indexedDB|caches)\b/)
    expect(listSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(listSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(listSource).not.toContain('box-shadow')
  })
})

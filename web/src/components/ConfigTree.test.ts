/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { mount } from '@vue/test-utils'

import type { ConfigGroup, ConfigTreeNode } from '../api/types'
import ConfigTree from './ConfigTree.vue'
import treeSource from './ConfigTree.vue?raw'

function treeFixture(): ConfigTreeNode[] {
  return [
    {
      path: 'conf.d',
      name: 'conf.d',
      entry_type: 'directory',
      managed: false,
      read_only: true,
      status_reason_code: 'directory',
    },
    {
      path: 'conf.d/site.conf',
      name: 'site.conf',
      entry_type: 'regular',
      managed: true,
      read_only: false,
      status_reason_code: 'managed_text',
      diff_status: 'modified',
    },
    {
      path: 'nginx.conf',
      name: 'nginx.conf',
      entry_type: 'regular',
      managed: true,
      read_only: false,
      status_reason_code: 'managed_text',
      diff_status: 'unchanged',
    },
  ]
}

function semanticFixture(): ConfigTreeNode[] {
  return [
    {
      path: 'external.conf',
      name: 'external.conf',
      entry_type: 'symlink',
      managed: false,
      read_only: true,
      status_reason_code: 'symlink_external',
      dependency_status: 'external',
    },
    {
      path: 'secret.pem',
      name: 'secret.pem',
      entry_type: 'regular',
      managed: false,
      read_only: true,
      status_reason_code: 'sensitive_material',
      diff_status: 'created',
    },
  ]
}

function groupFixture(): ConfigGroup[] {
  return [
    {
      id: 'group-1',
      name: 'Frontend sites',
      sort_order: 0,
      members: ['conf.d/site.conf', 'conf.d/missing.conf'],
      missing: ['conf.d/missing.conf'],
      created_by: 7,
      created_at: '2026-07-17T08:00:00Z',
      updated_at: '2026-07-17T08:01:00Z',
    },
  ]
}

describe('ConfigTree', () => {
  it('moves visible tree focus with arrows and Home End', async () => {
    const wrapper = mount(ConfigTree, {
      props: { nodes: treeFixture(), selectedPath: null },
      attachTo: document.body,
    })
    const tree = wrapper.get('[role="tree"]')

    await tree.trigger('keydown', { key: 'Home' })
    expect(document.activeElement?.getAttribute('data-path')).toBe('conf.d')
    await tree.trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.get('[data-path="conf.d"]').attributes('aria-expanded')).toBe('true')
    await tree.trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement?.getAttribute('data-path')).toBe('conf.d/site.conf')
    await tree.trigger('keydown', { key: 'End' })
    expect(document.activeElement?.getAttribute('data-path')).toBe('nginx.conf')

    wrapper.unmount()
  })

  it('uses roving tabindex, selected state, collapse navigation and Enter file open', async () => {
    const wrapper = mount(ConfigTree, {
      props: { nodes: treeFixture(), selectedPath: 'nginx.conf' },
      attachTo: document.body,
    })
    const tree = wrapper.get('[role="tree"]')
    const selected = wrapper.get('[data-path="nginx.conf"]')

    expect(selected.attributes('aria-selected')).toBe('true')
    expect(wrapper.findAll('[role="treeitem"][tabindex="0"]')).toHaveLength(1)
    selected.element.dispatchEvent(new FocusEvent('focus'))
    await selected.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('select')).toEqual([['nginx.conf']])

    await tree.trigger('keydown', { key: 'Home' })
    await tree.trigger('keydown', { key: 'ArrowRight' })
    await tree.trigger('keydown', { key: 'ArrowDown' })
    await tree.trigger('keydown', { key: 'ArrowLeft' })
    expect(document.activeElement?.getAttribute('data-path')).toBe('conf.d')
    await tree.trigger('keydown', { key: 'ArrowLeft' })
    expect(wrapper.get('[data-path="conf.d"]').attributes('aria-expanded')).toBe('false')

    wrapper.unmount()
  })

  it('shows read-only, sensitive, external and diff states with text plus hidden icons', () => {
    const wrapper = mount(ConfigTree, {
      props: { nodes: semanticFixture(), selectedPath: null },
    })

    const external = wrapper.get('[data-path="external.conf"]')
    expect(external.text()).toContain('External symlink')
    expect(external.text()).toContain('Read-only')
    expect(external.find('[data-state-icon][aria-hidden="true"]').exists()).toBe(true)

    const sensitive = wrapper.get('[data-path="secret.pem"]')
    expect(sensitive.text()).toContain('Sensitive material')
    expect(sensitive.text()).toContain('+ Created')
    expect(sensitive.get('[data-diff-icon][aria-hidden="true"]').text()).toBe('+')
  })

  it('never opens or offers file operations for a directory or symlink', async () => {
    const wrapper = mount(ConfigTree, {
      props: { nodes: [...treeFixture(), ...semanticFixture()], selectedPath: null },
      attachTo: document.body,
    })

    for (const path of ['conf.d', 'external.conf']) {
      const row = wrapper.get(`[data-path="${path}"]`)
      row.element.dispatchEvent(new FocusEvent('focus'))
      await row.trigger('keydown', { key: 'Enter' })
    }

    expect(wrapper.emitted('select')).toBeUndefined()
    expect(wrapper.find('button[aria-label="Copy selected file"]').exists()).toBe(false)
    expect(wrapper.find('button[aria-label="Rename selected file"]').exists()).toBe(false)
    expect(wrapper.find('button[aria-label="Delete selected file"]').exists()).toBe(false)
  })

  it('emits physical file operation requests only for a managed writable file', async () => {
    const wrapper = mount(ConfigTree, {
      props: { nodes: treeFixture(), selectedPath: 'nginx.conf' },
    })

    await wrapper.get('button[aria-label="Create file"]').trigger('click')
    await wrapper.get('button[aria-label="Copy selected file"]').trigger('click')
    await wrapper.get('button[aria-label="Rename selected file"]').trigger('click')
    await wrapper.get('button[aria-label="Delete selected file"]').trigger('click')

    expect(wrapper.emitted('create')).toEqual([[]])
    expect(wrapper.emitted('copy')).toEqual([['nginx.conf']])
    expect(wrapper.emitted('rename')).toEqual([['nginx.conf']])
    expect(wrapper.emitted('delete')).toEqual([['nginx.conf']])
  })

  it('toggles logical groups, expands members and labels missing entries', async () => {
    const wrapper = mount(ConfigTree, {
      props: {
        nodes: treeFixture(),
        groups: groupFixture(),
        selectedPath: null,
      },
      attachTo: document.body,
    })

    await wrapper.get('button[aria-label="Show logical groups"]').trigger('click')
    expect(wrapper.get('button[aria-label="Show logical groups"]').attributes('aria-pressed')).toBe(
      'true',
    )
    const group = wrapper.get('[data-group-id="group-1"]')
    expect(group.attributes('aria-expanded')).toBe('false')
    group.element.dispatchEvent(new FocusEvent('focus'))
    await group.trigger('keydown', { key: 'ArrowRight' })

    expect(wrapper.get('[data-path="conf.d/site.conf"]').text()).toContain('site.conf')
    const missing = wrapper.get('[data-path="conf.d/missing.conf"]')
    expect(missing.text()).toContain('Missing')
    expect(missing.find('[data-state-icon][aria-hidden="true"]').exists()).toBe(true)
    await missing.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('select')).toBeUndefined()
  })

  it('announces a one-member logical group with natural singular grammar', async () => {
    const group = groupFixture()[0]
    expect(group).toBeDefined()
    const wrapper = mount(ConfigTree, {
      props: {
        nodes: treeFixture(),
        groups: group === undefined
          ? []
          : [{ ...group, members: ['conf.d/site.conf'], missing: [] }],
        selectedPath: null,
      },
    })

    await wrapper.get('button[aria-label="Show logical groups"]').trigger('click')

    expect(wrapper.get('[data-group-id="group-1"]').attributes('aria-label')).toBe(
      'Frontend sites, 1 member',
    )
  })

  it('keeps every tree/action target 44px and contains no API, persistence or forbidden CSS', () => {
    expect(treeSource).toContain('min-height: var(--component-control-min-size)')
    expect(treeSource).toContain('min-width: var(--component-control-min-size)')
    expect(treeSource).not.toMatch(/\b(?:apiClient|fetch|XMLHttpRequest)\b/)
    expect(treeSource).not.toMatch(
      /\b(?:localStorage|sessionStorage|indexedDB|caches|serviceWorker)\b/,
    )
    expect(treeSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(treeSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(treeSource).not.toContain('box-shadow')
  })
})

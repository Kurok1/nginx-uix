/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
import { mount } from '@vue/test-utils'

import type { PublishCheck } from '../api/types'
import PublishPanel from './PublishPanel.vue'
import panelSource from './PublishPanel.vue?raw'

const validCheck: PublishCheck = {
  id: '11111111111111111111111111111111',
  workspace_id: '0123456789abcdef0123456789abcdef',
  workspace_revision: 2,
  production_digest: 'a'.repeat(64),
  base_digest: 'a'.repeat(64),
  draft_digest: 'b'.repeat(64),
  candidate_digest: 'c'.repeat(64),
  manifest_version: 1,
  policy_version: 1,
  validator_version: 1,
  validator_build_id: 'build-id',
  state: 'valid',
  diagnostic_count: 0,
  details: { diagnostics: [] },
  started_at: '2026-07-18T04:00:00Z',
  finished_at: '2026-07-18T04:00:01Z',
  expires_at: '2026-07-18T04:10:01Z',
}

describe('PublishPanel', () => {
  it('shows an exact block reason before checking', async () => {
    const wrapper = mount(PublishPanel, {
      props: {
        check: null,
        phase: 'idle',
        blockedReason: 'Save all open documents before checking.',
        expired: false,
        error: '',
      },
    })

    expect(wrapper.text()).toContain('Save all open documents before checking.')
    expect(wrapper.get('button[data-action="check"]').attributes()).toHaveProperty('disabled')
    await wrapper.setProps({ blockedReason: '' })
    await wrapper.get('button[data-action="check"]').trigger('click')
    expect(wrapper.emitted('check')).toHaveLength(1)
  })

  it('renders bound evidence and keeps production impact explicit', async () => {
    const wrapper = mount(PublishPanel, {
      props: { check: validCheck, phase: 'checked', blockedReason: '', expired: false, error: '' },
    })

    expect(wrapper.text()).toContain('Production configuration has not been changed.')
    expect(wrapper.text()).toContain('build-id')
    expect(wrapper.text()).toContain('aaaaaaaaaaaa')
    await wrapper.get('button[data-action="publish"]').trigger('click')
    expect(wrapper.emitted('publish')).toHaveLength(1)
    await wrapper.setProps({ expired: true })
    expect(wrapper.text()).toContain('This check has expired')
    expect(wrapper.get('button[data-action="publish"]').attributes()).toHaveProperty('disabled')
  })

  it('shows only bounded relative invalid diagnostics', () => {
    const wrapper = mount(PublishPanel, {
      props: {
        check: {
          ...validCheck,
          state: 'invalid',
          diagnostic_count: 1,
          details: {
            diagnostics: [
              { code: 'syntax_error', path: 'conf.d/site.conf', line: 4, summary: '配置语法无效' },
            ],
          },
        },
        phase: 'checked',
        blockedReason: '',
        expired: false,
        error: '',
      },
    })

    expect(wrapper.text()).toContain('conf.d/site.conf:4')
    expect(wrapper.text()).toContain('syntax_error')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.find('button[data-action="publish"]').exists()).toBe(false)
  })

  it('uses tokens and no embedded API or decorative effects', () => {
    expect(panelSource).toContain('var(--component-release-diagnostic-max-height)')
    expect(panelSource).not.toMatch(/\b(?:apiClient|fetch|XMLHttpRequest)\b/)
    expect(panelSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(panelSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(panelSource).not.toContain('box-shadow')
  })
})

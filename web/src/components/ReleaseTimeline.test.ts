/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
import { mount } from '@vue/test-utils'

import type { Release } from '../api/types'
import ReleaseTimeline from './ReleaseTimeline.vue'
import timelineSource from './ReleaseTimeline.vue?raw'

function release(state: Release['state']): Release {
  const terminal = !['queued', 'running', 'rolling_back'].includes(state)
  return {
    id: '22222222222222222222222222222222',
    workspace_id: '0123456789abcdef0123456789abcdef',
    check_id: '11111111111111111111111111111111',
    backup_id: '33333333333333333333333333333333',
    state,
    stage: state === 'succeeded' ? 'committed' : state === 'rolled_back' ? 'rolled_back' : state === 'needs_attention' ? 'needs_attention' : 'failed',
    production_digest: 'a'.repeat(64),
    draft_digest: 'b'.repeat(64),
    candidate_digest: 'b'.repeat(64),
    created_at: '2026-07-18T04:00:00Z',
    updated_at: '2026-07-18T04:00:02Z',
    ...(terminal ? { finished_at: '2026-07-18T04:00:02Z' } : {}),
    stages: [
      {
        sequence: 1,
        stage: 'queued',
        result: 'pending',
        details: {},
        occurred_at: '2026-07-18T04:00:00Z',
      },
      {
        sequence: 2,
        stage: state === 'succeeded' ? 'committed' : state === 'rolled_back' ? 'rolled_back' : state === 'needs_attention' ? 'needs_attention' : 'failed',
        result: state === 'rolled_back' ? 'warning' : state === 'succeeded' ? 'success' : 'failed',
        details: {},
        occurred_at: '2026-07-18T04:00:02Z',
      },
    ],
  }
}

describe('ReleaseTimeline', () => {
  it('renders ordered persisted stages with visible status words', () => {
    const wrapper = mount(ReleaseTimeline, { props: { release: release('succeeded'), streamState: 'closed' } })
    const rows = wrapper.findAll('li')
    expect(rows).toHaveLength(2)
    expect(rows[0]?.text()).toContain('Queued')
    expect(rows[0]?.text()).toContain('Pending')
    expect(rows[1]?.text()).toContain('Committed')
    expect(rows[1]?.text()).toContain('Success')
    expect(wrapper.text()).toContain('Published successfully')
    expect(wrapper.text()).toContain('Backup ID: 33333333333333333333333333333333')
  })

  it.each([
    ['failed', 'Release failed before production was changed.'],
    ['rolled_back', 'The last valid version was restored and confirmed healthy.'],
    ['needs_attention', 'Production or runtime state cannot be confirmed.'],
  ] as const)('renders the %s terminal truth', (state, message) => {
    const wrapper = mount(ReleaseTimeline, { props: { release: release(state), streamState: 'closed' } })
    expect(wrapper.text()).toContain(message)
    expect(wrapper.get('[data-terminal]').attributes('role')).toBe(
      state === 'needs_attention' ? 'alert' : 'status',
    )
  })

  it('keeps only the current concise stage in the live region', () => {
    const wrapper = mount(ReleaseTimeline, { props: { release: release('running'), streamState: 'live' } })
    const live = wrapper.get('[aria-live="polite"]')
    expect(live.attributes('aria-atomic')).toBe('true')
    expect(wrapper.findAll('[aria-live]').length).toBe(1)
    expect(wrapper.text()).toContain('Live progress connected')
  })

  it('uses the timeline token without decorative effects', () => {
    expect(timelineSource).toContain('var(--component-release-timeline-marker)')
    expect(timelineSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(timelineSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(timelineSource).not.toContain('box-shadow')
  })
})

/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import OperationTimeline from './OperationTimeline.vue'

describe('OperationTimeline', () => {
  it('renders only persisted stages and keeps current progress as the single live message', () => {
    const wrapper = mount(OperationTimeline, {
      props: {
        title: 'Restore progress',
        operationId: 'restore-1',
        state: 'running',
        stage: 'files_restoring',
        streamState: 'live',
        stages: [{
          sequence: 4,
          stage: 'files_restoring',
          result: 'running',
          occurred_at: '2026-07-19T09:00:00Z',
        }],
      },
    })

    expect(wrapper.get('ol').findAll('li')).toHaveLength(1)
    expect(wrapper.text()).toContain('Files restoring')
    expect(wrapper.text()).not.toContain('Production validated')
    expect(wrapper.get('[aria-live="polite"]').text()).toBe('Current stage: Files restoring')
    expect(wrapper.get('[data-stream-state]').text()).toBe('Live progress connected')
  })
})

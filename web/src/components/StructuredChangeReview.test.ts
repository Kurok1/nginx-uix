/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import { mount } from '@vue/test-utils'

import type { StructuredChangePreview } from '../api/structured'
import StructuredChangeReview from './StructuredChangeReview.vue'

const preview: StructuredChangePreview = {
  preview_id: 'a'.repeat(64),
  workspace_id: '1'.repeat(32),
  draft_etag: `"draft-v1:${'b'.repeat(64)}"`,
  operation_kind: 'upstream.rename',
  target_id: '2'.repeat(32),
  changed_files: [
    {
      path: 'conf.d/upstreams.conf',
      before_digest: 'c'.repeat(64),
      after_digest: 'd'.repeat(64),
      added_lines: 1,
      removed_lines: 1,
      patch: '@@ -1 +1 @@\n-upstream old {\n+upstream backend {\n',
    },
  ],
  complete: true,
}

describe('StructuredChangeReview', () => {
  it('shows bounded diff evidence and requires an exact named confirmation', async () => {
    const wrapper = mount(StructuredChangeReview, {
      props: {
        preview,
        pending: false,
        confirmation: '',
        confirmationTarget: 'backend',
        errorMessage: '',
      },
    })

    expect(wrapper.text()).toContain('Only the workspace draft will be updated')
    expect(wrapper.text()).toContain('Added')
    expect(wrapper.text()).toContain('Removed')
    expect(wrapper.get('[data-action="apply"]').attributes('disabled')).toBeDefined()

    await wrapper.get('input').setValue('backend')
    await wrapper.setProps({ confirmation: 'backend' })
    expect(wrapper.get('[data-action="apply"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-action="apply"]').trigger('click')
    expect(wrapper.emitted('apply')).toHaveLength(1)
  })

  it('keeps an incomplete preview visibly blocked', () => {
    const wrapper = mount(StructuredChangeReview, {
      props: {
        preview: { ...preview, complete: false, changed_files: [{ ...preview.changed_files[0]!, patch: '' }] },
        pending: false,
        confirmation: 'backend',
        confirmationTarget: 'backend',
        errorMessage: '',
      },
    })

    expect(wrapper.text()).toContain('Preview incomplete')
    expect(wrapper.get('[data-action="apply"]').attributes('disabled')).toBeDefined()
  })
})

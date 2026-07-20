/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import OperationConfirmModal from './OperationConfirmModal.vue'

describe('OperationConfirmModal', () => {
  it('requires a bounded reason and the exact visible confirmation', async () => {
    const wrapper = mount(OperationConfirmModal, {
      props: {
        open: true,
        title: 'Restart Nginx?',
        consequence: 'The master process will be replaced.',
        confirmationText: 'RESTART NGINX',
        confirmLabel: 'Restart Nginx',
        trigger: null,
      },
      attachTo: document.body,
    })

    const submit = wrapper.get('button[type="submit"]')
    expect(submit.attributes()).toHaveProperty('disabled')
    await wrapper.get('textarea').setValue('investigate runtime health')
    await wrapper.get('[data-confirmation]').setValue('restart nginx')
    expect(submit.attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-confirmation]').setValue('RESTART NGINX')
    expect(submit.attributes()).not.toHaveProperty('disabled')

    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('confirm')).toEqual([['investigate runtime health', 'RESTART NGINX']])
    wrapper.unmount()
  })

  it('supports a reason-only protection action without inventing a confirmation phrase', async () => {
    const wrapper = mount(OperationConfirmModal, {
      props: {
        open: true,
        title: 'Protect backup?',
        consequence: 'Retention will keep this recovery point.',
        confirmationText: '',
        confirmLabel: 'Protect backup',
        trigger: null,
      },
    })

    expect(wrapper.find('[data-confirmation]').exists()).toBe(false)
    await wrapper.get('textarea').setValue('monthly recovery point')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('confirm')).toEqual([['monthly recovery point', '']])
  })
})

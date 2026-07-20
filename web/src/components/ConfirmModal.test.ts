/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { mount } from '@vue/test-utils'

import ConfirmModal from './ConfirmModal.vue'
import modalSource from './ConfirmModal.vue?raw'

const consequence =
  "This deletes '站点.conf' only from this workspace draft. Production configuration and files are unaffected."

describe('ConfirmModal', () => {
  it('renders the named object and consequence in a labelled modal with cancel-first focus', () => {
    const trigger = document.createElement('button')
    const wrapper = mount(ConfirmModal, {
      props: {
        open: true,
        title: 'Delete file “站点.conf”?',
        objectName: '站点.conf',
        consequence,
        confirmLabel: 'Delete file',
        trigger,
      },
      attachTo: document.body,
    })

    const dialog = wrapper.get('[role="dialog"]')
    expect(dialog.attributes('aria-modal')).toBe('true')
    expect(dialog.attributes('aria-labelledby')).toBe(wrapper.get('h2').attributes('id'))
    expect(dialog.attributes('aria-describedby')).toBe(wrapper.get('p').attributes('id'))
    expect(wrapper.text()).toContain('Delete file “站点.conf”?')
    expect(wrapper.text()).toContain(consequence)
    expect(document.activeElement).toBe(wrapper.get('button[data-action="cancel"]').element)
    wrapper.unmount()
  })

  it('enables destructive confirmation only for exact Unicode text', async () => {
    const wrapper = mount(ConfirmModal, {
      props: {
        open: true,
        title: 'Delete file “站点.conf”?',
        objectName: '站点.conf',
        consequence,
        confirmLabel: 'Delete file',
        trigger: null,
      },
    })
    const input = wrapper.get('input')
    const submit = wrapper.get('button[type="submit"]')

    expect(input.attributes('aria-label')).toContain('站点.conf')
    expect(submit.attributes()).toHaveProperty('disabled')

    for (const mismatch of ['站点.CONF', ' 站点.conf', '站点.conf ', '站點.conf']) {
      await input.setValue(mismatch)
      expect(submit.attributes()).toHaveProperty('disabled')
    }

    await input.setValue('站点.conf')
    expect(submit.attributes()).not.toHaveProperty('disabled')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('confirm')).toEqual([['站点.conf']])
  })

  it('emits cancel on Escape and restores trigger after controlled close', async () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const wrapper = mount(ConfirmModal, {
      props: {
        open: true,
        title: 'Delete workspace?',
        objectName: 'Primary',
        consequence: 'Production configuration and files are unaffected.',
        trigger,
      },
      attachTo: document.body,
    })

    await wrapper.get('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('cancel')).toHaveLength(1)
    await wrapper.setProps({ open: false })
    expect(document.activeElement).toBe(trigger)

    wrapper.unmount()
    trigger.remove()
  })

  it('uses 44px token controls and no forbidden business CSS', () => {
    expect(modalSource).toContain('min-height: var(--component-control-min-size)')
    expect(modalSource).toContain('var(--component-modal-width)')
    expect(modalSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(modalSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(modalSource).not.toContain('box-shadow')
  })
})

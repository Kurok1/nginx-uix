/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { mount } from '@vue/test-utils'

import ReviewDrawer from './ReviewDrawer.vue'
import drawerSource from './ReviewDrawer.vue?raw'

describe('ReviewDrawer', () => {
  it('renders a named modal drawer with backdrop and slotted review content', () => {
    const trigger = document.createElement('button')
    const wrapper = mount(ReviewDrawer, {
      props: { open: true, title: 'Review changes', trigger },
      slots: { default: '<p>Diff incomplete: response limit reached</p>' },
    })

    const dialog = wrapper.get('[role="dialog"]')
    const title = wrapper.get('h2')
    expect(dialog.attributes('aria-modal')).toBe('true')
    expect(dialog.attributes('aria-labelledby')).toBe(title.attributes('id'))
    expect(title.text()).toBe('Review changes')
    expect(wrapper.find('.review-drawer__backdrop').exists()).toBe(true)
    expect(dialog.text()).toContain('Diff incomplete: response limit reached')
  })

  it('traps focus, closes on Escape and restores the invoking trigger', async () => {
    const trigger = document.createElement('button')
    trigger.textContent = 'Open review'
    const background = document.createElement('main')
    document.body.append(trigger, background)
    trigger.focus()
    const wrapper = mount(ReviewDrawer, {
      props: { open: true, title: 'Review changes', trigger },
      slots: { default: '<button type="button">Review action</button>' },
      attachTo: document.body,
    })

    expect(document.activeElement?.getAttribute('aria-label')).toBe('Close review')
    expect(background.hasAttribute('inert')).toBe(true)
    await wrapper.get('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('close')).toHaveLength(1)

    await wrapper.setProps({ open: false })
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(background.hasAttribute('inert')).toBe(false)
    expect(document.activeElement).toBe(trigger)

    wrapper.unmount()
    trigger.remove()
    background.remove()
  })

  it('uses shared control sizing and contains no business CSS literals', () => {
    expect(drawerSource).toContain('min-height: var(--component-control-min-size)')
    expect(drawerSource).toContain('min-width: var(--component-control-min-size)')
    expect(drawerSource).toContain('var(--component-drawer-width)')
    expect(drawerSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(drawerSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(drawerSource).not.toContain('box-shadow')
  })
})

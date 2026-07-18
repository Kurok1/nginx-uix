/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'

import { useFocusTrap } from './useFocusTrap'

function keydown(key: string, shiftKey = false): KeyboardEvent {
  return new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key, shiftKey })
}

function fixture() {
  const trigger = document.createElement('button')
  trigger.textContent = 'Open dialog'
  const background = document.createElement('main')
  const alreadyInert = document.createElement('aside')
  alreadyInert.setAttribute('inert', '')
  const dialog = document.createElement('section')
  const first = document.createElement('button')
  first.textContent = 'Cancel'
  const last = document.createElement('button')
  last.textContent = 'Delete'
  dialog.append(first, last)
  document.body.append(trigger, background, alreadyInert, dialog)

  return {
    alreadyInert,
    background,
    dialog,
    first,
    last,
    trigger,
    cleanup: () => {
      trigger.remove()
      background.remove()
      alreadyInert.remove()
      dialog.remove()
    },
  }
}

describe('useFocusTrap', () => {
  it('focuses the first enabled visible control and makes background siblings inert', () => {
    const elements = fixture()
    const hidden = document.createElement('button')
    hidden.hidden = true
    const disabled = document.createElement('button')
    disabled.disabled = true
    elements.dialog.prepend(hidden, disabled)
    const container = ref<HTMLElement | null>(elements.dialog)
    const trigger = ref<HTMLElement | null>(elements.trigger)
    const trap = useFocusTrap(container, trigger)

    elements.trigger.focus()
    trap.activate()

    expect(document.activeElement).toBe(elements.first)
    expect(elements.background.hasAttribute('inert')).toBe(true)
    expect(elements.alreadyInert.hasAttribute('inert')).toBe(true)
    expect(elements.dialog.hasAttribute('inert')).toBe(false)
    expect(document.head.hasAttribute('inert')).toBe(false)

    trap.deactivate()
    elements.cleanup()
  })

  it('wraps Tab and Shift+Tab across nested eligible controls', () => {
    const elements = fixture()
    const group = document.createElement('div')
    const middle = document.createElement('button')
    middle.textContent = 'Details'
    const hiddenGroup = document.createElement('div')
    hiddenGroup.hidden = true
    const hiddenNested = document.createElement('button')
    hiddenNested.textContent = 'Hidden'
    hiddenGroup.append(hiddenNested)
    group.append(middle, hiddenGroup)
    elements.dialog.insertBefore(group, elements.last)
    const trap = useFocusTrap(
      ref<HTMLElement | null>(elements.dialog),
      ref<HTMLElement | null>(elements.trigger),
    )
    trap.activate()

    elements.last.focus()
    const forward = keydown('Tab')
    trap.onKeydown(forward)
    expect(forward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(elements.first)

    elements.first.focus()
    const backward = keydown('Tab', true)
    trap.onKeydown(backward)
    expect(backward.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(elements.last)

    middle.focus()
    const middleTab = keydown('Tab')
    trap.onKeydown(middleTab)
    expect(middleTab.defaultPrevented).toBe(false)

    trap.deactivate()
    elements.cleanup()
  })

  it('leaves Escape for the caller and restores exact inert values and trigger focus', () => {
    const elements = fixture()
    const trap = useFocusTrap(
      ref<HTMLElement | null>(elements.dialog),
      ref<HTMLElement | null>(elements.trigger),
    )
    elements.trigger.focus()
    trap.activate()

    const escape = keydown('Escape')
    trap.onKeydown(escape)
    expect(escape.defaultPrevented).toBe(false)
    expect(document.activeElement).toBe(elements.first)

    trap.deactivate()
    expect(elements.background.hasAttribute('inert')).toBe(false)
    expect(elements.alreadyInert.hasAttribute('inert')).toBe(true)
    expect(document.activeElement).toBe(elements.trigger)
    elements.cleanup()
  })

  it('uses the container fallback when there are no eligible controls', () => {
    const elements = fixture()
    elements.first.remove()
    elements.last.remove()
    const trap = useFocusTrap(
      ref<HTMLElement | null>(elements.dialog),
      ref<HTMLElement | null>(elements.trigger),
    )

    trap.activate()
    expect(elements.dialog.getAttribute('tabindex')).toBe('-1')
    expect(document.activeElement).toBe(elements.dialog)

    const tab = keydown('Tab')
    trap.onKeydown(tab)
    expect(tab.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(elements.dialog)
    trap.deactivate()
    elements.cleanup()
  })

  it('restores inert state and trigger focus when its Vue owner unmounts', () => {
    const trigger = document.createElement('button')
    trigger.textContent = 'Open'
    document.body.append(trigger)
    const triggerRef = ref<HTMLElement | null>(trigger)
    const Host = defineComponent({
      setup() {
        const container = ref<HTMLElement | null>(null)
        const trap = useFocusTrap(container, triggerRef)
        return { container, trap }
      },
      template: `
        <section ref="container"><button>Cancel</button></section>
      `,
    })
    const background = document.createElement('main')
    document.body.append(background)
    const wrapper = mount(Host, { attachTo: document.body })
    trigger.focus()
    wrapper.vm.trap.activate()
    expect(background.hasAttribute('inert')).toBe(true)

    wrapper.unmount()
    expect(background.hasAttribute('inert')).toBe(false)
    expect(document.activeElement).toBe(trigger)
    trigger.remove()
    background.remove()
  })
})

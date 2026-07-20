/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { getCurrentInstance, onBeforeUnmount, type Ref } from 'vue'

const focusableSelector = [
  'a[href]',
  'button',
  'input',
  'select',
  'textarea',
  '[contenteditable="true"]',
  '[tabindex]',
].join(',')

export interface FocusTrap {
  activate(): void
  deactivate(): void
  onKeydown(event: KeyboardEvent): void
}

export function useFocusTrap(
  container: Readonly<Ref<HTMLElement | null>>,
  trigger: Readonly<Ref<HTMLElement | null>>,
): FocusTrap {
  const priorInert = new Map<HTMLElement, boolean>()
  let active = false
  let fallbackTabindex: string | null | undefined

  function focusableControls(): HTMLElement[] {
    const root = container.value
    if (root === null) {
      return []
    }
    return Array.from(root.querySelectorAll<HTMLElement>(focusableSelector)).filter(
      (element) => isFocusable(element, root),
    )
  }

  function focusFallback(): void {
    const root = container.value
    if (root === null) {
      return
    }
    if (fallbackTabindex === undefined) {
      fallbackTabindex = root.getAttribute('tabindex')
    }
    root.setAttribute('tabindex', '-1')
    root.focus()
  }

  function activate(): void {
    if (active) {
      return
    }
    const root = container.value
    if (root === null) {
      return
    }

    active = true
    for (const sibling of backgroundSiblings(root)) {
      if (!priorInert.has(sibling)) {
        priorInert.set(sibling, sibling.hasAttribute('inert'))
      }
      sibling.setAttribute('inert', '')
    }

    const first = focusableControls()[0]
    if (first === undefined) {
      focusFallback()
    } else {
      first.focus()
    }
  }

  function deactivate(): void {
    if (!active) {
      return
    }
    active = false

    for (const [element, wasInert] of priorInert) {
      if (wasInert) {
        element.setAttribute('inert', '')
      } else {
        element.removeAttribute('inert')
      }
    }
    priorInert.clear()

    const root = container.value
    if (root !== null && fallbackTabindex !== undefined) {
      if (fallbackTabindex === null) {
        root.removeAttribute('tabindex')
      } else {
        root.setAttribute('tabindex', fallbackTabindex)
      }
    }
    fallbackTabindex = undefined

    trigger.value?.focus()
  }

  function onKeydown(event: KeyboardEvent): void {
    if (!active || event.key !== 'Tab') {
      return
    }

    const controls = focusableControls()
    if (controls.length === 0) {
      event.preventDefault()
      focusFallback()
      return
    }

    const first = controls[0]
    const last = controls.at(-1)
    if (first === undefined || last === undefined) {
      return
    }

    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    } else if (!controls.includes(document.activeElement as HTMLElement)) {
      event.preventDefault()
      first.focus()
    }
  }

  if (getCurrentInstance() !== null) {
    onBeforeUnmount(deactivate)
  }

  return { activate, deactivate, onKeydown }
}

function backgroundSiblings(root: HTMLElement): HTMLElement[] {
  const siblings = new Set<HTMLElement>()
  let branch: HTMLElement = root

  while (branch.parentElement !== null) {
    const parent = branch.parentElement
    for (const child of parent.children) {
      if (child !== branch && child instanceof HTMLElement) {
        siblings.add(child)
      }
    }
    if (parent === document.body) {
      break
    }
    branch = parent
  }

  return [...siblings]
}

function isFocusable(element: HTMLElement, root: HTMLElement): boolean {
  if (
    element.tabIndex < 0 ||
    element.matches(':disabled') ||
    element.getAttribute('aria-disabled') === 'true' ||
    (element instanceof HTMLInputElement && element.type === 'hidden')
  ) {
    return false
  }

  let current: HTMLElement | null = element
  while (current !== null) {
    if (
      current.hidden ||
      current.getAttribute('aria-hidden') === 'true' ||
      current.style.display === 'none' ||
      current.style.visibility === 'hidden'
    ) {
      return false
    }
    if (current === root) {
      break
    }
    current = current.parentElement
  }

  return true
}

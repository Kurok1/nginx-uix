/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { redo, redoDepth, undo, undoDepth } from '@codemirror/commands'
import { language } from '@codemirror/language'
import { openSearchPanel, searchPanelOpen } from '@codemirror/search'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'

import CodeEditor from './CodeEditor.vue'
import editorSource from './CodeEditor.vue?raw'

describe('CodeEditor', () => {
  it('emits exact text changes and updates external text without recreating the view', async () => {
    const wrapper = mount(CodeEditor, {
      props: {
        modelValue: 'events {}\n',
        readOnly: false,
        ariaLabel: 'nginx.conf editor',
      },
    })
    const firstView = wrapper.vm.editorViewForTest()

    firstView.dispatch({
      changes: {
        from: 0,
        to: firstView.state.doc.length,
        insert: 'events {}\nhttp {}\n',
      },
    })

    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toBe(
      'events {}\nhttp {}\n',
    )
    await wrapper.setProps({ modelValue: 'events {}\nhttp {}\n' })
    expect(wrapper.vm.editorViewForTest()).toBe(firstView)
    expect(firstView.state.doc.toString()).toBe('events {}\nhttp {}\n')

    wrapper.unmount()
    expect(firstView.dom.isConnected).toBe(false)
  })

  it('provides line numbers, nginx stream language, history and a search panel', () => {
    const wrapper = mount(CodeEditor, {
      props: {
        modelValue: 'events {}\nhttp {}\n',
        readOnly: false,
        ariaLabel: 'nginx.conf editor',
      },
      attachTo: document.body,
    })
    const view = wrapper.vm.editorViewForTest()

    expect(wrapper.find('.cm-lineNumbers').exists()).toBe(true)
    expect(view.state.facet(language)?.name).toBe('nginx')

    view.dispatch({ changes: { from: view.state.doc.length, insert: '# local\n' } })
    expect(undoDepth(view.state)).toBe(1)
    expect(undo(view)).toBe(true)
    expect(view.state.doc.toString()).toBe('events {}\nhttp {}\n')
    expect(redoDepth(view.state)).toBe(1)
    expect(redo(view)).toBe(true)
    expect(view.state.doc.toString()).toBe('events {}\nhttp {}\n# local\n')

    expect(openSearchPanel(view)).toBe(true)
    expect(searchPanelOpen(view.state)).toBe(true)
    expect(wrapper.find('.cm-search').exists()).toBe(true)

    wrapper.unmount()
  })

  it('reconfigures read-only in place and exposes a labelled focusable content surface', async () => {
    const wrapper = mount(CodeEditor, {
      props: {
        modelValue: 'events {}\n',
        readOnly: false,
        ariaLabel: 'nginx.conf editor',
      },
      attachTo: document.body,
    })
    const view = wrapper.vm.editorViewForTest()
    const content = wrapper.get('.cm-content')

    expect(view.state.readOnly).toBe(false)
    expect(content.attributes('aria-label')).toBe('nginx.conf editor')
    view.focus()
    expect(document.activeElement).toBe(content.element)

    await wrapper.setProps({ readOnly: true })
    expect(wrapper.vm.editorViewForTest()).toBe(view)
    expect(view.state.readOnly).toBe(true)

    await wrapper.setProps({ readOnly: false, ariaLabel: 'site.conf editor' })
    expect(view.state.readOnly).toBe(false)
    expect(content.attributes('aria-label')).toBe('site.conf editor')

    wrapper.unmount()
  })

  it('preserves exact CRLF text during controlled changes', async () => {
    const wrapper = mount(CodeEditor, {
      props: {
        modelValue: 'events {}\r\n',
        readOnly: false,
        ariaLabel: 'nginx.conf editor',
      },
    })
    const view = wrapper.vm.editorViewForTest()

    expect(view.state.sliceDoc()).toBe('events {}\r\n')
    view.dispatch({ changes: { from: view.state.doc.length, insert: 'http {}\r\n' } })
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toBe(
      'events {}\r\nhttp {}\r\n',
    )

    await wrapper.setProps({ modelValue: 'events {}\r\nhttp {}\r\n' })
    expect(view.state.sliceDoc()).toBe('events {}\r\nhttp {}\r\n')
  })

  it('keeps the same view, selection and undo history when a task panel hides it', async () => {
    const Host = defineComponent({
      components: { CodeEditor },
      setup() {
        return {
          text: ref('events {}\n'),
          visible: ref(true),
        }
      },
      template: `
        <CodeEditor
          v-show="visible"
          v-model="text"
          :read-only="false"
          aria-label="nginx.conf editor"
        />
      `,
    })
    const wrapper = mount(Host)
    const editor = wrapper.getComponent(CodeEditor)
    const view = editor.vm.editorViewForTest()

    view.dispatch({
      changes: { from: view.state.doc.length, insert: 'http {}\n' },
      selection: { anchor: 3 },
    })
    const historyDepth = undoDepth(view.state)
    wrapper.vm.visible = false
    await nextTick()
    wrapper.vm.visible = true
    await nextTick()

    expect(wrapper.getComponent(CodeEditor).vm.editorViewForTest()).toBe(view)
    expect(view.state.selection.main.anchor).toBe(3)
    expect(undoDepth(view.state)).toBe(historyDepth)
  })

  it('contains only the six locked CodeMirror capabilities and no persistence behavior', () => {
    expect(editorSource).toContain("from '@codemirror/commands'")
    expect(editorSource).toContain("from '@codemirror/language'")
    expect(editorSource).toContain("from '@codemirror/legacy-modes/mode/nginx'")
    expect(editorSource).toContain("from '@codemirror/search'")
    expect(editorSource).toContain("from '@codemirror/state'")
    expect(editorSource).toContain("from '@codemirror/view'")
    expect(editorSource).not.toMatch(
      /@codemirror\/(?:autocomplete|lint|merge)|\b(?:LSP|collab|autosave|localStorage|indexedDB|caches|fetch)\b/i,
    )
    expect(editorSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(editorSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(editorSource).not.toContain('box-shadow')
  })
})

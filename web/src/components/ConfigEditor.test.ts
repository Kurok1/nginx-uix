/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { searchPanelOpen } from '@codemirror/search'
import { mount } from '@vue/test-utils'

import type { OpenDocument } from '../workspace'
import CodeEditor from './CodeEditor.vue'
import ConfigEditor from './ConfigEditor.vue'
import editorSource from './ConfigEditor.vue?raw'

function document(path: string, dirty = false): OpenDocument {
  const serverContent = 'server { listen 80; }\n'
  return {
    path,
    serverContent,
    content: dirty ? 'server { listen 8080; }\n' : serverContent,
    lineEnding: 'lf',
    contentDigest: 'a'.repeat(64),
    dirty,
    requiresRefresh: false,
  }
}

describe('ConfigEditor', () => {
  it('keeps all document editors mounted while tabs select one dirty document', async () => {
    const documents = [document('conf.d/site.conf', true), document('nginx.conf')]
    const wrapper = mount(ConfigEditor, {
      props: {
        documents,
        selectedPath: 'conf.d/site.conf',
        canSave: true,
        pending: false,
        readOnly: false,
      },
    })

    expect(wrapper.findAll('[role="tab"]')).toHaveLength(2)
    expect(wrapper.get('[role="tablist"]').findAll('[aria-label^="Close "]')).toHaveLength(0)
    expect(wrapper.find('button[aria-label="Close conf.d/site.conf"]').exists()).toBe(true)
    expect(wrapper.get('[role="tab"][aria-selected="true"]').text()).toContain('site.conf')
    expect(wrapper.text()).toContain('Unsaved changes')
    const editors = wrapper.findAllComponents(CodeEditor)
    expect(editors).toHaveLength(2)
    const firstView = editors[0]?.vm.editorViewForTest()

    await wrapper.setProps({ selectedPath: 'nginx.conf' })
    expect(wrapper.findAllComponents(CodeEditor)).toHaveLength(2)
    await wrapper.setProps({ selectedPath: 'conf.d/site.conf' })
    expect(wrapper.findAllComponents(CodeEditor)[0]?.vm.editorViewForTest()).toBe(firstView)
  })

  it('emits exact edits, explicit save, tab selection and close intent', async () => {
    const wrapper = mount(ConfigEditor, {
      props: {
        documents: [document('nginx.conf', true)],
        selectedPath: 'nginx.conf',
        canSave: true,
        pending: false,
        readOnly: false,
      },
    })
    const codeEditor = wrapper.getComponent(CodeEditor)
    const view = codeEditor.vm.editorViewForTest()
    view.dispatch({
      changes: { from: 0, to: view.state.doc.length, insert: 'events {}\r\n' },
    })

    expect(wrapper.emitted('update')).toEqual([['nginx.conf', 'events {}\n']])
    await wrapper.get('button[aria-label="Save nginx.conf"]').trigger('click')
    await wrapper.get('button[aria-label="Select nginx.conf"]').trigger('click')
    await wrapper.get('button[aria-label="Close nginx.conf"]').trigger('click')
    expect(wrapper.emitted('save')).toEqual([['nginx.conf']])
    expect(wrapper.emitted('select')).toEqual([['nginx.conf']])
    expect(wrapper.emitted('close')).toEqual([['nginx.conf']])
  })

  it('moves tab selection and focus with arrow, Home and End keys', async () => {
    const wrapper = mount(ConfigEditor, {
      attachTo: globalThis.document.body,
      props: {
        documents: [document('conf.d/site.conf'), document('nginx.conf')],
        selectedPath: 'conf.d/site.conf',
        canSave: true,
        pending: false,
        readOnly: false,
      },
    })
    const tabs = wrapper.findAll<HTMLButtonElement>('[role="tab"]')

    await tabs[0]?.trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.emitted('select')).toEqual([['nginx.conf']])
    expect(globalThis.document.activeElement).toBe(tabs[1]?.element)

    await tabs[1]?.trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.emitted('select')?.at(-1)).toEqual(['conf.d/site.conf'])
    expect(globalThis.document.activeElement).toBe(tabs[0]?.element)

    await tabs[0]?.trigger('keydown', { key: 'End' })
    expect(wrapper.emitted('select')?.at(-1)).toEqual(['nginx.conf'])
    expect(globalThis.document.activeElement).toBe(tabs[1]?.element)

    await tabs[1]?.trigger('keydown', { key: 'Home' })
    expect(wrapper.emitted('select')?.at(-1)).toEqual(['conf.d/site.conf'])
    expect(globalThis.document.activeElement).toBe(tabs[0]?.element)

    await tabs[0]?.trigger('keydown', { key: 'ArrowLeft' })
    expect(wrapper.emitted('select')?.at(-1)).toEqual(['nginx.conf'])
    expect(globalThis.document.activeElement).toBe(tabs[1]?.element)

    wrapper.unmount()
  })

  it('opens find and gives an explicit reason whenever save is disabled', async () => {
    const wrapper = mount(ConfigEditor, {
      props: {
        documents: [document('nginx.conf')],
        selectedPath: 'nginx.conf',
        canSave: false,
        pending: false,
        readOnly: false,
      },
    })
    const save = wrapper.get('button[aria-label="Save nginx.conf"]')

    expect(save.attributes()).toHaveProperty('disabled')
    expect(wrapper.text()).toContain('No unsaved changes')
    await wrapper.get('button[aria-label="Find in nginx.conf"]').trigger('click')
    expect(searchPanelOpen(wrapper.getComponent(CodeEditor).vm.editorViewForTest().state)).toBe(true)

    await wrapper.setProps({ documents: [document('nginx.conf', true)], pending: true })
    expect(wrapper.text()).toContain('A workspace change is in progress')
    await wrapper.setProps({ pending: false, readOnly: true })
    expect(wrapper.text()).toContain('This workspace is read-only')
  })

  it('renders an accessible empty state and uses no API, persistence or forbidden CSS', () => {
    const wrapper = mount(ConfigEditor, {
      props: {
        documents: [],
        selectedPath: null,
        canSave: false,
        pending: false,
        readOnly: false,
      },
    })

    expect(wrapper.get('[role="status"]').text()).toContain('Select a managed file to edit')
    expect(editorSource).toContain('min-height: var(--component-control-min-size)')
    expect(editorSource).not.toMatch(/\b(?:apiClient|fetch|XMLHttpRequest)\b/)
    expect(editorSource).not.toMatch(
      /\b(?:localStorage|sessionStorage|indexedDB|caches|autosave)\b/i,
    )
    expect(editorSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(editorSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(editorSource).not.toContain('box-shadow')
  })
})

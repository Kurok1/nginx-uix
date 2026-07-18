/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import type {
  ConfigDependency,
  ConfigGroup,
  ConfigTreeNode,
  DiffResponse,
  WorkspaceDetail,
} from '../api/types'
import ConfigReview from '../components/ConfigReview.vue'
import ConfigTree from '../components/ConfigTree.vue'
import ConfirmModal from '../components/ConfirmModal.vue'
import type { OpenDocument, WorkspaceStateModel, WorkspaceStore } from '../workspace'
import ConfigWorkspaceView from './ConfigWorkspaceView.vue'
import viewSource from './ConfigWorkspaceView.vue?raw'

const digest = 'a'.repeat(64)

function workspace(
  state: WorkspaceDetail['state'] = 'ready',
  id = 'workspace-id',
): WorkspaceDetail {
  return {
    id,
    name: 'Review changes',
    state,
    production_digest: digest,
    base_digest: digest,
    draft_etag: `"draft-v1:${digest}"`,
    entry_count: 1,
    managed_bytes: 32,
    workspace_bytes: 128,
    created_by: 7,
    created_at: '2026-07-17T08:00:00Z',
    updated_at: '2026-07-17T08:01:00Z',
  }
}

function document(dirty = true, requiresRefresh = false): OpenDocument {
  return {
    path: 'nginx.conf',
    serverContent: 'events {}\n',
    content: dirty ? 'events { worker_connections 256; }\n' : 'events {}\n',
    lineEnding: 'lf',
    contentDigest: digest,
    dirty,
    requiresRefresh,
  }
}

const node: ConfigTreeNode = {
  path: 'nginx.conf',
  name: 'nginx.conf',
  entry_type: 'regular',
  managed: true,
  read_only: false,
  status_reason_code: 'managed_text',
  diff_status: 'modified',
}

const dependency: ConfigDependency = {
  source: 'nginx.conf',
  line: 2,
  column: 5,
  display_value: 'conf.d/*.conf',
  target: 'conf.d/site.conf',
  status: 'resolved',
  cycle: false,
}

const group: ConfigGroup = {
  id: 'group-id',
  name: 'Sites',
  sort_order: 10,
  members: ['nginx.conf'],
  missing: [],
  created_by: 7,
  created_at: '2026-07-17T08:00:00Z',
  updated_at: '2026-07-17T08:01:00Z',
}

const diff: DiffResponse = {
  files: [
    {
      path: 'nginx.conf',
      status: 'modified',
      added_lines: 1,
      removed_lines: 1,
    },
  ],
  complete: true,
  reason: '',
  patch: '@@ -1 +1 @@\n-events {}\n+events { worker_connections 256; }',
}

function createStore(options: {
  active?: WorkspaceDetail | null
  phase?: WorkspaceStateModel['phase']
  documents?: OpenDocument[]
} = {}): { state: WorkspaceStateModel; store: WorkspaceStore } {
  const active = options.active === undefined ? workspace() : options.active
  const state = reactive<WorkspaceStateModel>({
    phase: options.phase ?? (active === null ? 'idle' : 'ready'),
    workspaces: active === null ? [] : [active],
    active,
    tree: active === null ? [] : [node],
    dependencies: active === null ? [] : [dependency],
    documents: options.documents ?? (active === null ? [] : [document()]),
    selectedPath: active === null ? null : 'nginx.conf',
    activeTask: 'editor',
    search: null,
    diff: active === null ? null : diff,
    groups: active === null ? null : { groups: [group], groups_etag: '"groups-v1"' },
    pendingAction: null,
    banner: null,
  })
  const store: WorkspaceStore = {
    state,
    loadWorkspaces: vi.fn(async () => undefined),
    createWorkspace: vi.fn(async () => workspace('ready', 'created-id')),
    deleteWorkspace: vi.fn(async () => undefined),
    openWorkspace: vi.fn(async () => undefined),
    openFile: vi.fn(async () => undefined),
    closeFile: vi.fn(() => true),
    createFile: vi.fn(async () => undefined),
    copyFile: vi.fn(async () => undefined),
    renameFile: vi.fn(async () => undefined),
    deleteFile: vi.fn(async () => undefined),
    updateDocument: vi.fn(),
    saveFile: vi.fn(async () => undefined),
    reloadFile: vi.fn(async () => undefined),
    copyLocalContent: vi.fn(async () => true),
    canSave: vi.fn(() => true),
    hasUnsavedChanges: vi.fn(() => state.documents.some(({ dirty }) => dirty)),
    markSessionExpired: vi.fn(),
    markSessionRestored: vi.fn(async () => undefined),
    searchFiles: vi.fn(async () => undefined),
    loadDiff: vi.fn(async () => undefined),
    loadGroups: vi.fn(async () => undefined),
    createGroup: vi.fn(async () => undefined),
    replaceGroup: vi.fn(async () => undefined),
    deleteGroup: vi.fn(async () => undefined),
    document: vi.fn((path: string) => state.documents.find((item) => item.path === path)),
  }
  return { state, store }
}

async function mountView(
  store: WorkspaceStore,
  workspaceId: string | null = store.state.active?.id ?? null,
) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/config/workspaces/:workspaceId?',
        name: 'config-workspaces',
        component: { template: '<div />' },
      },
    ],
  })
  await router.push(
    workspaceId === null ? '/config/workspaces' : `/config/workspaces/${workspaceId}`,
  )
  await router.isReady()
  const wrapper = mount(ConfigWorkspaceView, {
    props: { store },
    global: {
      plugins: [router],
      stubs: { CodeEditor: { template: '<div data-code-editor />' } },
    },
  })
  await flushPromises()
  return { router, wrapper }
}

describe('ConfigWorkspaceView', () => {
  it('loads the bounded workspace list and renders loading and empty states', async () => {
    const { state, store } = createStore({ active: null, phase: 'loading' })
    const { wrapper } = await mountView(store)

    expect(store.loadWorkspaces).toHaveBeenCalledOnce()
    expect(wrapper.get('[aria-busy="true"]').text()).toContain('Loading workspaces')
    state.phase = 'idle'
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('No workspaces yet')
    expect(wrapper.text()).toContain('Select or create a workspace')

    state.active = workspace()
    state.phase = 'ready'
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain(
      'No managed configuration files are available in this workspace.',
    )
  })

  it('creates and deletes a workspace only through store actions with named consequences', async () => {
    const { store } = createStore({ active: null })
    const { router, wrapper } = await mountView(store)

    await wrapper.get('button[aria-label="Create workspace"]').trigger('click')
    await wrapper.get('input[name="workspace-name"]').setValue('July changes')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(store.createWorkspace).toHaveBeenCalledWith('July changes')
    expect(router.currentRoute.value.fullPath).toBe('/config/workspaces/created-id')

    const active = workspace()
    store.state.workspaces = [active]
    await wrapper.vm.$nextTick()
    await wrapper.get('button[aria-label="Delete workspace Review changes"]').trigger('click')
    const modal = wrapper.getComponent(ConfirmModal)
    expect(modal.text()).toContain('Production configuration and files are unaffected')
    await modal.get('input').setValue('Review changes')
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(store.deleteWorkspace).toHaveBeenCalledWith('workspace-id', 'Review changes')
  })

  it('returns to the workspace list after the store clears the deleted active workspace', async () => {
    const { state, store } = createStore()
    store.deleteWorkspace = vi.fn(async () => {
      state.active = null
    })
    const { router, wrapper } = await mountView(store)

    await wrapper.get('button[aria-label="Delete workspace Review changes"]').trigger('click')
    const modal = wrapper.getComponent(ConfirmModal)
    await modal.get('input').setValue('Review changes')
    await modal.get('form').trigger('submit')
    await flushPromises()

    expect(store.deleteWorkspace).toHaveBeenCalledWith('workspace-id', 'Review changes')
    expect(router.currentRoute.value.fullPath).toBe('/config/workspaces')
  })

  it('orchestrates ready editing, file operations, search, diff and dependencies', async () => {
    const { store } = createStore()
    let workspaceOpened = false
    let groupsStartedAfterWorkspaceOpen = false
    store.openWorkspace = vi.fn(async () => {
      await Promise.resolve()
      workspaceOpened = true
    })
    store.loadGroups = vi.fn(async () => {
      groupsStartedAfterWorkspaceOpen = workspaceOpened
    })
    const { wrapper } = await mountView(store)

    expect(store.openWorkspace).toHaveBeenCalledWith('workspace-id')
    expect(store.loadGroups).toHaveBeenCalledWith('workspace-id')
    expect(groupsStartedAfterWorkspaceOpen).toBe(true)
    expect(wrapper.text()).toContain('Review changes')
    await wrapper.get('button[aria-label="Save nginx.conf"]').trigger('click')
    expect(store.saveFile).toHaveBeenCalledWith('nginx.conf')

    const tree = wrapper.getComponent(ConfigTree)
    tree.vm.$emit('create')
    await wrapper.vm.$nextTick()
    await wrapper.get('input[name="mutation-path"]').setValue('conf.d/new.conf')
    await wrapper.get('textarea[name="mutation-content"]').setValue('server {}\n')
    await wrapper.get('form[aria-label="Create file"]').trigger('submit')
    await flushPromises()
    expect(store.createFile).toHaveBeenCalledWith('conf.d/new.conf', 'server {}\n')

    tree.vm.$emit('copy', 'nginx.conf')
    await wrapper.vm.$nextTick()
    await wrapper.get('input[name="mutation-destination"]').setValue('nginx.copy.conf')
    await wrapper.get('form[aria-label="Copy file"]').trigger('submit')
    await flushPromises()
    expect(store.copyFile).toHaveBeenCalledWith('nginx.conf', 'nginx.copy.conf')

    tree.vm.$emit('rename', 'nginx.conf')
    await wrapper.vm.$nextTick()
    await wrapper.get('input[name="mutation-destination"]').setValue('nginx.renamed.conf')
    await wrapper.get('form[aria-label="Rename file"]').trigger('submit')
    await flushPromises()
    expect(store.renameFile).toHaveBeenCalledWith('nginx.conf', 'nginx.renamed.conf')

    tree.vm.$emit('delete', 'nginx.conf')
    await wrapper.vm.$nextTick()
    const modal = wrapper.getComponent(ConfirmModal)
    expect(modal.text()).toContain('only from this workspace draft')
    await modal.get('input').setValue('nginx.conf')
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(store.deleteFile).toHaveBeenCalledWith('nginx.conf', 'nginx.conf')

    tree.vm.$emit('create-group')
    await wrapper.vm.$nextTick()
    await wrapper.get('input[name="group-name"]').setValue('Servers')
    await wrapper.get('input[name="group-order"]').setValue('20')
    await wrapper.get('textarea[name="group-members"]').setValue('nginx.conf\nconf.d/site.conf')
    await wrapper.get('form[aria-label="Create logical group"]').trigger('submit')
    await flushPromises()
    expect(store.createGroup).toHaveBeenCalledWith({
      name: 'Servers',
      sort_order: 20,
      members: ['nginx.conf', 'conf.d/site.conf'],
    })

    tree.vm.$emit('replace-group', group)
    await wrapper.vm.$nextTick()
    expect(wrapper.get('input[name="group-name"]').element).toHaveProperty('value', 'Sites')
    await wrapper.get('input[name="group-name"]').setValue('Public sites')
    await wrapper.get('form[aria-label="Edit logical group"]').trigger('submit')
    await flushPromises()
    expect(store.replaceGroup).toHaveBeenCalledWith('group-id', {
      name: 'Public sites',
      sort_order: 10,
      members: ['nginx.conf'],
    })

    tree.vm.$emit('delete-group', group)
    await wrapper.vm.$nextTick()
    const groupModal = wrapper.getComponent(ConfirmModal)
    expect(groupModal.text()).toContain('It does not delete files')
    await groupModal.get('input').setValue('Sites')
    await groupModal.get('form').trigger('submit')
    await flushPromises()
    expect(store.deleteGroup).toHaveBeenCalledWith('group-id', 'Sites')

    const review = wrapper.getComponent(ConfigReview)
    review.vm.$emit('request-diff', 'nginx.conf')
    review.vm.$emit('request-diff')
    review.vm.$emit('search', 'proxy_pass')
    await flushPromises()
    expect(store.loadDiff).toHaveBeenNthCalledWith(1, 'nginx.conf')
    expect(store.loadDiff).toHaveBeenNthCalledWith(2, undefined)
    expect(store.searchFiles).toHaveBeenCalledWith('proxy_pass')
    await review.get('button[aria-label="Review include dependencies"]').trigger('click')
    expect(review.text()).toContain('conf.d/*.conf')

    store.state.search = {
      matches: [{ path: 'nginx.conf', line: 2, column: 5, snippet: 'proxy_pass app;' }],
      complete: false,
    }
    await review.get('button[aria-label="Search workspace files"]').trigger('click')
    await wrapper.vm.$nextTick()
    expect(review.text()).toContain('Search incomplete: response limit reached')
  })

  it('preserves local conflict text and exposes only the defined recovery actions', async () => {
    const local = document(true, true)
    const { state, store } = createStore({ documents: [local] })
    state.banner = { kind: 'conflict', message: 'Server workspace changed.' }
    const { wrapper } = await mountView(store)

    expect(wrapper.text()).toContain(
      'This file changed on the server. Your local text has not been overwritten.',
    )
    await wrapper.get('button[aria-label="复制本地内容 nginx.conf"]').trigger('click')
    await wrapper.get('button[aria-label="读取服务器版本 nginx.conf"]').trigger('click')
    await wrapper.get('button[aria-label="查看服务器差异 nginx.conf"]').trigger('click')
    await flushPromises()
    expect(store.copyLocalContent).toHaveBeenCalledWith('nginx.conf')
    expect(store.reloadFile).toHaveBeenCalledWith('nginx.conf')
    expect(store.loadDiff).toHaveBeenCalledWith('nginx.conf')
    expect(local.content).toBe('events { worker_connections 256; }\n')
    expect(store.saveFile).not.toHaveBeenCalled()
  })

  it('keeps stale, needs-attention, Agent and session states persistent and actionable', async () => {
    const active = workspace('stale')
    const { state, store } = createStore({ active })
    state.banner = { kind: 'stale', message: 'The production configuration has changed.' }
    const { wrapper } = await mountView(store)

    expect(wrapper.text()).toContain('Production configuration changed. Create a new workspace to continue.')
    expect(wrapper.find('button[aria-label="创建新工作区"]').exists()).toBe(true)
    state.active = workspace('needs_attention')
    state.banner = { kind: 'needs_attention', message: 'Administrator attention is required.' }
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('workspace-id')
    expect(wrapper.getComponent(ConfigTree).props('readOnly')).toBe(true)
    expect(wrapper.get('button[aria-label="Save nginx.conf"]').attributes()).toHaveProperty(
      'disabled',
    )

    state.banner = { kind: 'agent_unavailable', message: 'The local agent is unavailable.' }
    await wrapper.vm.$nextTick()
    expect(wrapper.get('[role="alert"]').text()).toContain('Configuration Agent is unavailable')
    state.banner = { kind: 'session_expired', message: 'Authentication is required.' }
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('Session expired')
    expect(wrapper.text()).toContain('Copy local content before signing in again')
  })

  it('implements A1 mounted task panels without direct API, persistence or production actions', async () => {
    const { store } = createStore()
    const { wrapper } = await mountView(store)

    expect(wrapper.findAll('.workspace-task-panel')).toHaveLength(3)
    await wrapper.get('button[aria-label="Show review task"]').trigger('click')
    expect(wrapper.findAll('.workspace-task-panel')).toHaveLength(3)
    expect(wrapper.get('.workspace-review-panel').attributes('aria-hidden')).toBe('false')

    for (const button of wrapper.findAll('button')) {
      const name = button.attributes('aria-label') ?? button.text()
      expect(name).not.toMatch(/validate|publish|reload|restart|restore/i)
    }
    expect(viewSource).toContain('grid-template-columns: var(--component-workspace-tree-width) minmax(0, 1fr) var(--component-workspace-review-width)')
    expect(viewSource).toContain('.workspace-task-panel[aria-hidden="true"]')
    expect(viewSource).toMatch(
      /\.workspace-mutation-form div \{\s*display: flex;\s*justify-content: flex-end;/,
    )
    expect(viewSource).not.toMatch(/\b(?:apiClient|fetch|XMLHttpRequest)\b/)
    expect(viewSource).not.toMatch(/\b(?:localStorage|sessionStorage|indexedDB|caches)\b/)
    expect(viewSource).not.toMatch(/#[\da-f]{3,8}\b/i)
    expect(viewSource).not.toMatch(/\b(?:linear|radial)-gradient\s*\(/)
    expect(viewSource).not.toContain('box-shadow')
  })

  it('makes inactive mounted task panels inert on the mobile layout', async () => {
    const originalWidth = window.innerWidth
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 640 })
    const { store } = createStore()
    const { wrapper } = await mountView(store)

    try {
      const tree = wrapper.get('.workspace-tree-panel')
      const editor = wrapper.get('.workspace-editor-panel')
      const review = wrapper.get('.workspace-review-panel')
      const selector = wrapper.get('.workspace-file-selector')

      expect(tree.attributes('aria-hidden')).toBe('true')
      expect(tree.attributes()).toHaveProperty('inert')
      expect(selector.attributes()).toHaveProperty('inert')
      expect(editor.attributes('aria-hidden')).toBe('false')
      expect(editor.attributes()).not.toHaveProperty('inert')
      expect(review.attributes()).toHaveProperty('inert')

      await wrapper.get('button[aria-label="Show review task"]').trigger('click')
      expect(editor.attributes()).toHaveProperty('inert')
      expect(review.attributes('aria-hidden')).toBe('false')
      expect(review.attributes()).not.toHaveProperty('inert')
    } finally {
      wrapper.unmount()
      Object.defineProperty(window, 'innerWidth', {
        configurable: true,
        value: originalWidth,
      })
    }
  })
})

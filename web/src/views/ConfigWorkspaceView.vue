<!--
  @author hanchao <hanchao@66yunlian.com>
  @since 0.2.1
-->
<template>
  <main class="workspace-page">
    <h1>Configuration workspaces</h1>

    <div
      v-if="pageError !== ''"
      class="workspace-page__error"
      role="alert"
    >
      <span aria-hidden="true">◇!</span>
      {{ pageError }}
    </div>

    <section
      v-if="state.phase === 'loading' && state.active === null"
      class="workspace-page__loading"
      aria-busy="true"
      aria-live="polite"
    >
      <span aria-hidden="true">◌</span>
      Loading workspaces…
    </section>

    <WorkspaceList
      :workspaces="state.workspaces"
      :selected-id="state.active?.id ?? null"
      :pending-action="state.pendingAction"
      @create="createWorkspace"
      @request-delete="requestWorkspaceDelete"
      @select="selectWorkspace"
    />

    <p
      v-if="state.active === null && state.phase !== 'loading'"
      class="workspace-page__empty"
      role="status"
    >
      Select or create a workspace to review draft configuration changes.
    </p>

    <template v-if="state.active !== null">
      <WorkspaceHeader
        :workspace="state.active"
        :draft-change-count="draftChangeCount"
      />

      <p
        v-if="state.phase === 'ready' && state.tree.length === 0"
        class="workspace-page__empty"
        role="status"
      >
        No managed configuration files are available in this workspace.
        Create a managed text file to begin this workspace draft.
      </p>

      <InlineBanner
        v-if="state.banner?.kind === 'conflict'"
        kind="conflict"
        :message="workspaceBannerMessage"
      >
        <template #actions>
          <template
            v-for="document in conflictedDocuments"
            :key="document.path"
          >
            <button
              type="button"
              :aria-label="`复制本地内容 ${document.path}`"
              @click="copyLocal(document.path)"
            >
              复制本地内容
            </button>
            <button
              type="button"
              :aria-label="`读取服务器版本 ${document.path}`"
              @click="readServerVersion(document.path)"
            >
              读取服务器版本
            </button>
            <button
              type="button"
              :aria-label="`查看服务器差异 ${document.path}`"
              @click="showServerDiff(document.path, $event)"
            >
              查看服务器差异
            </button>
          </template>
        </template>
      </InlineBanner>

      <InlineBanner
        v-else-if="state.banner?.kind === 'stale'"
        kind="stale"
        :message="workspaceBannerMessage"
      >
        <template #actions>
          <button
            type="button"
            aria-label="创建新工作区"
            @click="replacementFormOpen = true"
          >
            创建新工作区
          </button>
        </template>
      </InlineBanner>

      <InlineBanner
        v-else-if="state.banner?.kind === 'needs_attention'"
        kind="needs_attention"
        :message="workspaceBannerMessage"
      />

      <InlineBanner
        v-else-if="state.banner?.kind === 'agent_unavailable'"
        kind="agent"
        :message="workspaceBannerMessage"
      />

      <InlineBanner
        v-else-if="state.banner?.kind === 'session_expired'"
        kind="info"
        :message="workspaceBannerMessage"
      >
        <template #actions>
          <button
            v-for="document in dirtyDocuments"
            :key="document.path"
            type="button"
            :aria-label="`Copy local content ${document.path}`"
            @click="copyLocal(document.path)"
          >
            Copy {{ document.path }}
          </button>
        </template>
      </InlineBanner>

      <form
        v-if="replacementFormOpen"
        class="workspace-mutation-form"
        aria-label="Create replacement workspace"
        @submit.prevent="createReplacementWorkspace"
      >
        <label for="replacement-workspace-name">Workspace name</label>
        <input
          id="replacement-workspace-name"
          v-model="replacementWorkspaceName"
          name="replacement-workspace-name"
          autocomplete="off"
        >
        <div>
          <button
            type="button"
            @click="closeReplacementForm"
          >
            Cancel
          </button>
          <button
            type="submit"
            :disabled="replacementWorkspaceName === '' || state.pendingAction !== null"
          >
            Create
          </button>
        </div>
      </form>

      <div
        v-if="state.phase === 'loading'"
        class="workspace-page__pane-loading"
        aria-busy="true"
        aria-live="polite"
      >
        <span aria-hidden="true">◌</span>
        Loading workspace files…
      </div>

      <nav
        class="workspace-task-tabs"
        aria-label="Workspace tasks"
      >
        <button
          type="button"
          aria-label="Show files task"
          :aria-pressed="state.activeTask === 'files'"
          @click="selectTask('files')"
        >
          Files
        </button>
        <button
          type="button"
          aria-label="Show editor task"
          :aria-pressed="state.activeTask === 'editor'"
          @click="selectTask('editor')"
        >
          Edit
        </button>
        <button
          type="button"
          aria-label="Show review task"
          :aria-pressed="state.activeTask === 'review'"
          @click="selectTask('review')"
        >
          Review
        </button>
      </nav>

      <label
        class="workspace-file-selector"
        :aria-hidden="panelHidden('files')"
        :inert="panelHidden('files') ? true : undefined"
      >
        <span>Workspace file</span>
        <select
          :value="state.selectedPath ?? ''"
          @change="selectFileFromControl"
        >
          <option value="">
            Select a managed file
          </option>
          <option
            v-for="entry in selectableFiles"
            :key="entry.path"
            :value="entry.path"
          >
            {{ entry.path }}
          </option>
        </select>
      </label>

      <div class="workspace-layout">
        <aside
          class="workspace-tree-panel workspace-task-panel"
          :aria-hidden="panelHidden('files')"
          :inert="panelHidden('files') ? true : undefined"
        >
          <ConfigTree
            :nodes="state.tree"
            :groups="state.groups?.groups ?? []"
            :selected-path="state.selectedPath"
            :read-only="workspaceReadOnly"
            @select="openFile"
            @create="openFileMutation('create')"
            @copy="openFileMutation('copy', $event)"
            @rename="openFileMutation('rename', $event)"
            @delete="requestFileDelete"
            @create-group="openGroupMutation('create')"
            @replace-group="openGroupMutation('replace', $event)"
            @delete-group="requestGroupDelete"
          />
        </aside>

        <section
          class="workspace-editor-panel workspace-task-panel"
          :aria-hidden="panelHidden('editor')"
          :inert="panelHidden('editor') ? true : undefined"
        >
          <ConfigEditor
            :documents="state.documents"
            :selected-path="state.selectedPath"
            :can-save="selectedCanSave"
            :pending="state.pendingAction !== null"
            :read-only="workspaceReadOnly"
            @update="store.updateDocument"
            @save="saveFile"
            @select="selectOpenDocument"
            @close="closeFile"
          />
        </section>

        <aside
          class="workspace-review-panel workspace-task-panel"
          :aria-hidden="panelHidden('review')"
          :inert="panelHidden('review') ? true : undefined"
        >
          <ConfigReview
            :dependencies="state.dependencies"
            :diff="state.diff"
            :pending="state.pendingAction !== null"
            :search="state.search"
            :selected-path="state.selectedPath"
            @request-diff="loadDiff"
            @search="searchFiles"
            @select="openFile"
          />
        </aside>
      </div>

      <button
        type="button"
        class="workspace-review-trigger"
        aria-label="Open workspace review"
        @click="openReviewDrawer"
      >
        Review changes
      </button>

      <ReviewDrawer
        :open="reviewDrawerOpen"
        title="Workspace review"
        :trigger="drawerTrigger"
        @close="reviewDrawerOpen = false"
      >
        <ConfigReview
          :dependencies="state.dependencies"
          :diff="state.diff"
          :pending="state.pendingAction !== null"
          :search="state.search"
          :selected-path="state.selectedPath"
          @request-diff="loadDiff"
          @search="searchFiles"
          @select="openFile"
        />
      </ReviewDrawer>

      <PublishPanel
        :check="releaseState.check"
        :phase="releaseState.phase"
        :blocked-reason="publishBlockedReason"
        :expired="publishCheckExpired"
        :error="releaseState.error"
        @check="startPublishCheck"
        @publish="requestPublish"
      />

      <ReleaseTimeline
        v-if="releaseState.release !== null"
        :release="releaseState.release"
        :stream-state="releaseState.stream"
      />

      <form
        v-if="fileMutation !== null"
        class="workspace-mutation-form"
        :aria-label="fileMutationLabel"
        @submit.prevent="submitFileMutation"
      >
        <template v-if="fileMutation.kind === 'create'">
          <label for="mutation-path">File path</label>
          <input
            id="mutation-path"
            v-model="mutationPath"
            name="mutation-path"
            autocomplete="off"
          >
          <label for="mutation-content">Initial content</label>
          <textarea
            id="mutation-content"
            v-model="mutationContent"
            name="mutation-content"
          />
        </template>
        <template v-else>
          <p>Source: {{ fileMutation.sourcePath }}</p>
          <label for="mutation-destination">Destination path</label>
          <input
            id="mutation-destination"
            v-model="mutationDestination"
            name="mutation-destination"
            autocomplete="off"
          >
        </template>
        <div>
          <button
            type="button"
            @click="closeFileMutation"
          >
            Cancel
          </button>
          <button
            type="submit"
            :disabled="!fileMutationReady || state.pendingAction !== null"
          >
            {{ fileMutationLabel }}
          </button>
        </div>
      </form>

      <form
        v-if="groupMutation !== null"
        class="workspace-mutation-form"
        :aria-label="groupMutation.kind === 'create' ? 'Create logical group' : 'Edit logical group'"
        @submit.prevent="submitGroupMutation"
      >
        <label for="group-name">Group name</label>
        <input
          id="group-name"
          v-model="groupName"
          name="group-name"
          autocomplete="off"
        >
        <label for="group-order">Sort order</label>
        <input
          id="group-order"
          v-model.number="groupSortOrder"
          name="group-order"
          type="number"
        >
        <label for="group-members">Member paths, one per line</label>
        <textarea
          id="group-members"
          v-model="groupMembers"
          name="group-members"
        />
        <div>
          <button
            type="button"
            @click="groupMutation = null"
          >
            Cancel
          </button>
          <button
            type="submit"
            :disabled="groupName === '' || state.pendingAction !== null"
          >
            Save group
          </button>
        </div>
      </form>
    </template>

    <ConfirmModal
      :open="deleteTarget !== null"
      :title="deleteTarget?.title ?? 'Confirm deletion'"
      :consequence="deleteTarget?.consequence ?? ''"
      :object-name="deleteTarget?.objectName ?? ''"
      :trigger="deleteTrigger"
      @cancel="deleteTarget = null"
      @confirm="confirmDelete"
    />
    <ConfirmModal
      :open="releaseModalOpen"
      title="Publish configuration to production?"
      consequence="The system will recheck production and the draft, create a complete backup, update production files, validate the full configuration, reload Nginx, and automatically roll back when the result is safely knowable."
      :object-name="state.active?.name ?? ''"
      confirm-label="Publish"
      :trigger="releaseTrigger"
      @cancel="releaseModalOpen = false"
      @confirm="confirmPublish"
    />
    <ToastRegion
      :toasts="toasts"
      @dismiss="dismissToast"
    />
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import type { ConfigGroup, WorkspaceSummary } from '../api/types'
import ConfigEditor from '../components/ConfigEditor.vue'
import ConfigReview from '../components/ConfigReview.vue'
import ConfigTree from '../components/ConfigTree.vue'
import ConfirmModal from '../components/ConfirmModal.vue'
import InlineBanner from '../components/InlineBanner.vue'
	import PublishPanel from '../components/PublishPanel.vue'
	import ReleaseTimeline from '../components/ReleaseTimeline.vue'
import ReviewDrawer from '../components/ReviewDrawer.vue'
import ToastRegion, { type ToastMessage } from '../components/ToastRegion.vue'
import WorkspaceHeader from '../components/WorkspaceHeader.vue'
import WorkspaceList from '../components/WorkspaceList.vue'
	import { isTerminalRelease, releaseStore, type ReleaseStore } from '../release'
import { workspaceStore, type WorkspaceStore } from '../workspace'

type WorkspaceTask = 'editor' | 'files' | 'review'
type FileMutation =
  | { kind: 'create' }
  | { kind: 'copy' | 'rename'; sourcePath: string }
type GroupMutation =
  | { kind: 'create' }
  | { kind: 'replace'; group: ConfigGroup }
type DeleteTarget =
  | {
      kind: 'file'
      path: string
      objectName: string
      title: string
      consequence: string
    }
  | {
      kind: 'group'
      group: ConfigGroup
      objectName: string
      title: string
      consequence: string
    }
  | {
      kind: 'workspace'
      workspace: WorkspaceSummary
      objectName: string
      title: string
      consequence: string
    }

const props = withDefaults(defineProps<{ store?: WorkspaceStore; releases?: ReleaseStore }>(), {
  store: () => workspaceStore,
	releases: () => releaseStore,
})
const store = props.store
const state = store.state
	const releases = props.releases
	const releaseState = releases.state
const route = useRoute()
const router = useRouter()

const pageError = ref('')
const replacementFormOpen = ref(false)
const replacementWorkspaceName = ref('')
const reviewDrawerOpen = ref(false)
const drawerTrigger = ref<HTMLElement | null>(null)
const deleteTarget = ref<DeleteTarget | null>(null)
const deleteTrigger = ref<HTMLElement | null>(null)
	const releaseModalOpen = ref(false)
	const releaseTrigger = ref<HTMLElement | null>(null)
	const expiryClock = ref(Date.now())
const fileMutation = ref<FileMutation | null>(null)
const mutationPath = ref('')
const mutationContent = ref('')
const mutationDestination = ref('')
const groupMutation = ref<GroupMutation | null>(null)
const groupName = ref('')
const groupSortOrder = ref(0)
const groupMembers = ref('')
const toasts = ref<ToastMessage[]>([])
const isMobileTaskLayout = ref(false)
let toastSequence = 0
	let expiryTimer: number | undefined
	let lastTerminalRelease = ''

const dirtyDocuments = computed(() => state.documents.filter(({ dirty }) => dirty))
const conflictedDocuments = computed(() =>
  state.documents.filter(({ requiresRefresh }) => requiresRefresh),
)
const draftChangeCount = computed(() =>
  state.tree.filter(({ diff_status }) => diff_status !== undefined && diff_status !== 'unchanged')
    .length,
)
const workspaceReadOnly = computed(
  () =>
    state.active?.state !== 'ready' ||
    state.banner?.kind === 'agent_unavailable' ||
    state.banner?.kind === 'conflict' ||
    state.banner?.kind === 'session_expired',
)
const workspaceBannerMessage = computed(() => {
  switch (state.banner?.kind) {
    case 'conflict':
      return 'This file changed on the server. Your local text has not been overwritten.'
    case 'stale':
      return 'Production configuration changed. Create a new workspace to continue.'
    case 'needs_attention':
      return `Workspace consistency cannot be confirmed. Workspace ID: ${state.active?.id ?? ''}`
    case 'agent_unavailable':
      return 'Configuration Agent is unavailable. Production configuration and files are unaffected.'
    case 'session_expired':
      return 'Session expired; local text remains in memory. Copy local content before signing in again.'
    default:
      return ''
  }
})
const selectedCanSave = computed(
  () =>
    !workspaceReadOnly.value &&
    state.selectedPath !== null &&
    store.canSave(state.selectedPath),
)
const selectableFiles = computed(() =>
  state.tree.filter(({ entry_type, managed }) => entry_type === 'regular' && managed),
)
const fileMutationLabel = computed(() => {
  if (fileMutation.value?.kind === 'copy') return 'Copy file'
  if (fileMutation.value?.kind === 'rename') return 'Rename file'
  return 'Create file'
})
const fileMutationReady = computed(() =>
  fileMutation.value?.kind === 'create'
    ? mutationPath.value !== ''
    : mutationDestination.value !== '',
)
	const publishBlockedReason = computed(() => {
		const workspace = state.active
		if (workspace === null) return 'Open a workspace before checking publication.'
		if (workspace.state !== 'ready') {
			if (workspace.state === 'published') return 'This immutable workspace has already been published.'
			if (workspace.state === 'stale') return 'Production changed. Create a new workspace before publishing.'
			if (workspace.state === 'needs_attention') return 'Workspace consistency must be resolved before publishing.'
			return 'The workspace is not ready for publication.'
		}
		if (state.pendingAction !== null) return 'Wait for the current workspace mutation to finish.'
		if (store.hasUnsavedChanges()) return 'Save all open documents before checking publication.'
		if (state.diff === null) return 'Load the complete all-files diff before checking publication.'
		if (!state.diff.complete) return 'The diff is incomplete. Reduce it until the full review is available.'
		if (!state.diff.files.some(({ status }) => status !== 'unchanged')) return 'This workspace has no publishable changes.'
		if (releaseState.release !== null && !isTerminalRelease(releaseState.release)) return 'A release or rollback is already in progress.'
		return ''
	})
	const publishCheckExpired = computed(
		() => releaseState.check !== null && Date.parse(releaseState.check.expires_at) <= expiryClock.value,
	)

watch(
  () => route.params.workspaceId,
  (workspaceId) => {
    if (typeof workspaceId === 'string' && workspaceId !== '') {
      void run(async () => {
			releases.reset()
        await store.openWorkspace(workspaceId)
        await store.loadGroups(workspaceId)
			await openRequestedRoutePath()
			const releaseID = routeReleaseID() ?? state.active?.last_release_id
			if (releaseID !== undefined) await releases.resume(releaseID)
      })
    }
  },
  { immediate: true },
)

	watch(
		() => route.query.path,
		() => {
			if (state.active?.id === route.params.workspaceId) {
				void run(openRequestedRoutePath)
			}
		},
	)

	watch(
		() => route.query.release,
		(value) => {
			if (typeof value === 'string' && /^[0-9a-f]{32}$/.test(value) && releaseState.release?.id !== value) {
				void run(() => releases.resume(value))
			}
		},
	)

	watch(
		() => releaseState.release?.state,
		(releaseStatus) => {
			const release = releaseState.release
			if (release === null || !isTerminalRelease(release) || lastTerminalRelease === release.id) return
			lastTerminalRelease = release.id
			if (release.workspace_id === state.active?.id && (releaseStatus === 'succeeded' || releaseStatus === 'needs_attention')) {
				void run(() => store.openWorkspace(release.workspace_id))
			}
			if (releaseStatus === 'succeeded') addToast('Configuration published and Nginx health confirmed.')
			else if (releaseStatus === 'rolled_back') addToast('Release failed; the last valid version was restored and confirmed healthy.')
		},
	)

onMounted(() => {
  updateMobileLayout()
  window.addEventListener('resize', updateMobileLayout)
	expiryTimer = window.setInterval(() => { expiryClock.value = Date.now() }, 30_000)
  void run(store.loadWorkspaces)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', updateMobileLayout)
	if (expiryTimer !== undefined) window.clearInterval(expiryTimer)
})

function updateMobileLayout(): void {
  isMobileTaskLayout.value = window.innerWidth <= 640
}

function panelHidden(task: WorkspaceTask): boolean {
  return isMobileTaskLayout.value && state.activeTask !== task
}

function selectTask(task: WorkspaceTask): void {
  state.activeTask = task
}

function selectWorkspace(id: string): void {
  void router.push({ name: 'config-workspaces', params: { workspaceId: id } })
}

async function createWorkspace(name: string): Promise<void> {
  await run(async () => {
    const created = await store.createWorkspace(name)
    await router.push({ name: 'config-workspaces', params: { workspaceId: created.id } })
    addToast(`Workspace “${created.name}” created.`)
  })
}

async function createReplacementWorkspace(): Promise<void> {
  const name = replacementWorkspaceName.value
  if (name === '') return
  await createWorkspace(name)
  closeReplacementForm()
}

function closeReplacementForm(): void {
  replacementFormOpen.value = false
  replacementWorkspaceName.value = ''
}

function requestWorkspaceDelete(workspace: WorkspaceSummary): void {
  deleteTrigger.value = document.activeElement instanceof HTMLElement ? document.activeElement : null
  deleteTarget.value = {
    kind: 'workspace',
    workspace,
    objectName: workspace.name,
    title: `Delete workspace “${workspace.name}”?`,
    consequence:
      'This removes the workspace draft and its metadata. Production configuration and files are unaffected.',
  }
}

function openFile(path: string): void {
  void run(() => store.openFile(path))
}

async function openRequestedRoutePath(): Promise<void> {
	const requestedPath = route.query.path
	if (typeof requestedPath !== 'string' || requestedPath === '') return
	await store.openFile(requestedPath)
	state.activeTask = 'editor'
}

function selectFileFromControl(event: Event): void {
  const value = event.target instanceof HTMLSelectElement ? event.target.value : ''
  if (value !== '') openFile(value)
}

function selectOpenDocument(path: string): void {
  state.selectedPath = path
  state.activeTask = 'editor'
}

function closeFile(path: string): void {
  if (!store.closeFile(path)) {
    pageError.value = `Save or resolve unsaved changes in ${path} before closing it.`
  }
}

function saveFile(path: string): void {
  void run(async () => {
    await store.saveFile(path)
    addToast(`${path} saved to the workspace draft.`)
  })
}

function openFileMutation(kind: FileMutation['kind'], sourcePath?: string): void {
  if (workspaceReadOnly.value) return
  mutationPath.value = ''
  mutationContent.value = ''
  mutationDestination.value = ''
  fileMutation.value =
    kind === 'create'
      ? { kind }
      : { kind, sourcePath: sourcePath ?? state.selectedPath ?? '' }
}

function closeFileMutation(): void {
  fileMutation.value = null
}

async function submitFileMutation(): Promise<void> {
  const mutation = fileMutation.value
  if (mutation === null || !fileMutationReady.value) return
  await run(async () => {
    if (mutation.kind === 'create') {
      await store.createFile(mutationPath.value, mutationContent.value)
    } else if (mutation.kind === 'copy') {
      await store.copyFile(mutation.sourcePath, mutationDestination.value)
    } else {
      await store.renameFile(mutation.sourcePath, mutationDestination.value)
    }
    addToast(`${fileMutationLabel.value} completed in the workspace draft.`)
    closeFileMutation()
  })
}

function requestFileDelete(path: string): void {
  if (workspaceReadOnly.value) return
  deleteTrigger.value = document.activeElement instanceof HTMLElement ? document.activeElement : null
  deleteTarget.value = {
    kind: 'file',
    path,
    objectName: path,
    title: `Delete file “${path}”?`,
    consequence: `This deletes '${path}' only from this workspace draft. Production configuration and files are unaffected.`,
  }
}

function openGroupMutation(kind: GroupMutation['kind'], group?: ConfigGroup): void {
  if (workspaceReadOnly.value) return
  if (kind === 'replace' && group !== undefined) {
    groupMutation.value = { kind, group }
    groupName.value = group.name
    groupSortOrder.value = group.sort_order
    groupMembers.value = group.members.join('\n')
  } else {
    groupMutation.value = { kind: 'create' }
    groupName.value = ''
    groupSortOrder.value = 0
    groupMembers.value = ''
  }
}

async function submitGroupMutation(): Promise<void> {
  const mutation = groupMutation.value
  if (mutation === null || groupName.value === '') return
  const input = {
    name: groupName.value,
    sort_order: groupSortOrder.value,
    members: groupMembers.value
      .split('\n')
      .map((member) => member.trim())
      .filter((member) => member !== ''),
  }
  await run(async () => {
    if (mutation.kind === 'create') {
      await store.createGroup(input)
    } else {
      await store.replaceGroup(mutation.group.id, input)
    }
    addToast(`Logical group “${input.name}” saved.`)
    groupMutation.value = null
  })
}

function requestGroupDelete(group: ConfigGroup): void {
  if (workspaceReadOnly.value) return
  deleteTrigger.value = document.activeElement instanceof HTMLElement ? document.activeElement : null
  deleteTarget.value = {
    kind: 'group',
    group,
    objectName: group.name,
    title: `Delete logical group “${group.name}”?`,
    consequence:
      'This removes only the logical group. It does not delete files, and production configuration is unaffected.',
  }
}

async function confirmDelete(name: string): Promise<void> {
  const target = deleteTarget.value
  if (target === null) return
  await run(async () => {
    if (target.kind === 'workspace') {
      const deletingActiveWorkspace = state.active?.id === target.workspace.id
      await store.deleteWorkspace(target.workspace.id, name)
      if (deletingActiveWorkspace) {
        await router.replace({ name: 'config-workspaces' })
      }
    } else if (target.kind === 'file') {
      await store.deleteFile(target.path, name)
    } else {
      await store.deleteGroup(target.group.id, name)
    }
    addToast(`${target.title.replace('?', '')} completed.`)
    deleteTarget.value = null
  })
}

function copyLocal(path: string): void {
  void run(async () => {
    if (await store.copyLocalContent(path)) addToast(`Local content for ${path} copied.`)
  })
}

function readServerVersion(path: string): void {
  void run(() => store.reloadFile(path))
}

function openReviewDrawer(event: MouseEvent): void {
  drawerTrigger.value = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  reviewDrawerOpen.value = true
}

function showServerDiff(path: string, event: MouseEvent): void {
  state.activeTask = 'review'
  openReviewDrawer(event)
  void loadDiff(path)
}

function loadDiff(path?: string): Promise<void> {
  return run(() => store.loadDiff(path))
}

function searchFiles(query: string): void {
  void run(() => store.searchFiles(query))
}

	function startPublishCheck(): void {
		const workspace = state.active
		if (workspace === null || publishBlockedReason.value !== '') return
		void run(async () => {
			const check = await releases.check(workspace, state.diff, store.hasUnsavedChanges())
			addToast(check.state === 'valid' ? 'Complete candidate check passed.' : 'Candidate check found configuration errors.')
		})
	}

	function requestPublish(): void {
		if (state.active === null || releaseState.check?.state !== 'valid' || publishCheckExpired.value) return
		releaseTrigger.value = document.activeElement instanceof HTMLElement ? document.activeElement : null
		releaseModalOpen.value = true
	}

	async function confirmPublish(name: string): Promise<void> {
		const workspace = state.active
		if (workspace === null) return
		await run(async () => {
			const release = await releases.queue(workspace, name)
			releaseModalOpen.value = false
			await router.replace({
				name: 'config-workspaces',
				params: { workspaceId: workspace.id },
				query: { ...route.query, release: release.id },
			})
		})
	}

	function routeReleaseID(): string | undefined {
		const value = route.query.release
		return typeof value === 'string' && /^[0-9a-f]{32}$/.test(value) ? value : undefined
	}

function addToast(message: string): void {
  toastSequence += 1
  toasts.value.push({ id: `workspace-toast-${toastSequence}`, message })
}

function dismissToast(id: string): void {
  toasts.value = toasts.value.filter((toast) => toast.id !== id)
}

async function run(operation: () => Promise<unknown>): Promise<void> {
  pageError.value = ''
  try {
    await operation()
  } catch (error) {
    pageError.value =
      error instanceof Error
        ? 'The workspace action could not be completed. Local draft text is unchanged.'
        : 'The workspace request failed.'
  }
}
</script>

<style scoped>
.workspace-page,
.workspace-layout,
.workspace-task-panel,
.workspace-editor-panel,
.workspace-review-panel,
.workspace-tree-panel {
  min-width: 0;
}

.workspace-page {
  display: grid;
  overflow-x: hidden;
  gap: var(--spacing-md);
}

.workspace-page h1,
.workspace-page p {
  margin: 0;
}

.workspace-page__loading,
.workspace-page__pane-loading,
.workspace-page__empty,
.workspace-page__error {
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  overflow-wrap: anywhere;
}

.workspace-page__error {
  border-color: var(--color-state-danger-foreground);
  background: var(--color-state-danger);
  color: var(--color-state-danger-foreground);
}

.workspace-layout {
  display: grid;
  gap: var(--spacing-md);
}

.workspace-task-tabs,
.workspace-review-trigger,
.workspace-file-selector,
.workspace-mutation-form div {
  min-width: 0;
  flex-wrap: wrap;
  gap: var(--spacing-xs);
}

.workspace-task-tabs,
.workspace-review-trigger,
.workspace-file-selector {
  display: none;
}

.workspace-page button,
.workspace-page input,
.workspace-page select {
  min-height: var(--component-control-min-size);
}

.workspace-page button {
  min-width: var(--component-control-min-size);
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-pill);
  background: var(--color-canvas);
  color: var(--color-primary);
  cursor: pointer;
}

.workspace-page button:disabled {
  border-color: var(--color-ink-muted-48);
  background: var(--color-canvas-parchment);
  color: var(--color-ink-muted-80);
  cursor: not-allowed;
}

.workspace-page button:active:not(:disabled) {
  transform: scale(0.95);
}

.workspace-mutation-form {
  display: grid;
  min-width: 0;
  padding: var(--spacing-md);
  border: 1px solid var(--color-hairline);
  border-radius: var(--rounded-sm);
  gap: var(--spacing-xs);
}

.workspace-mutation-form input,
.workspace-mutation-form textarea,
.workspace-file-selector select {
  width: 100%;
  min-width: 0;
  padding: var(--spacing-xs) var(--spacing-sm);
  border: 1px solid var(--color-ink-muted-48);
  border-radius: var(--rounded-sm);
  background: var(--color-canvas);
}

.workspace-mutation-form textarea {
  min-height: var(--component-editor-min-height);
  resize: vertical;
}

.workspace-mutation-form div {
  display: flex;
  justify-content: flex-end;
}

@media (min-width: 1069px) {
  .workspace-layout {
    grid-template-columns: var(--component-workspace-tree-width) minmax(0, 1fr) var(--component-workspace-review-width);
  }

  .workspace-review-trigger,
  .workspace-task-tabs {
    display: none;
  }
}

@media (min-width: 834px) and (max-width: 1068px) {
  .workspace-layout {
    grid-template-columns: var(--component-workspace-tree-width) minmax(0, 1fr);
  }

  .workspace-review-panel {
    display: none;
  }
}

@media (min-width: 735px) and (max-width: 833px) {
  .workspace-layout {
    grid-template-columns: var(--component-workspace-tree-width-narrow) minmax(0, 1fr);
  }

  .workspace-review-panel {
    display: none;
  }
}

@media (max-width: 1068px) and (min-width: 641px) {
  .workspace-review-trigger {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    justify-self: end;
  }
}

@media (min-width: 641px) and (max-width: 734px) {
  .workspace-review-panel {
    display: none;
  }
}

@media (max-width: 734px) {
  .workspace-layout {
    display: block;
  }

  .workspace-tree-panel {
    display: none;
  }

  .workspace-file-selector {
    display: flex;
    flex-direction: column;
  }
}

@media (max-width: 640px) {
  .workspace-task-tabs {
    display: flex;
  }

  .workspace-tree-panel {
    display: block;
  }

  .workspace-task-panel[aria-hidden="true"] {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }

  .workspace-file-selector[aria-hidden="true"] {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
}
</style>

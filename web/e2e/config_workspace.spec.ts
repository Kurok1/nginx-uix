/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { expect, type Locator, type Page, test } from '@playwright/test'

import {
  appOrigin,
  assertOnlyLocalePreferenceStorage,
  csrfToken,
  installWorkspaceAPIFixture,
  setAuthenticatedCookie,
  type WorkspaceAPIFixture,
  type WorkspaceAPIRequest,
} from './support/api'

const workspaceName = 'Review changes'
const privateMarker = 'workspace-private-marker.[literal]?=E2E-ONLY-20260717'

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

test('explicit workspace management flow keeps strict mutable API and ETag boundaries', async ({
  context,
  page,
}) => {
  const api = await installWorkspaceAPIFixture(page)

  await page.goto('/config/workspaces?lang=en-US')
  await expect(page.getByText('No workspaces yet. Create one to review draft configuration changes.')).toBeVisible()

  await page.getByRole('button', { name: 'Create workspace' }).click()
  await page.getByLabel('Workspace name').fill(workspaceName)
  await page.getByRole('button', { name: 'Create', exact: true }).click()

  await expect(page).toHaveURL(`${appOrigin}/config/workspaces/${api.workspaceId}?lang=en-US`)
  await expect(page.getByRole('heading', { level: 1, name: workspaceName })).toBeVisible()
  await expect(page.getByRole('link', { name: new RegExp(workspaceName) })).toHaveAttribute(
    'aria-current',
    'page',
  )

  await openFile(page, 'nginx.conf')
  const editor = page.getByLabel('nginx.conf editor')
  await appendEditorText(page, editor, `# ${privateMarker}`)
  await expect(page.getByText('Unsaved changes', { exact: true }).first()).toBeVisible()
  expect(fileSaveRequests(api, 'nginx.conf')).toHaveLength(0)

  const firstDraftETag = api.currentDraftETag()
  await page.getByRole('button', { name: 'Save nginx.conf' }).click()
  await expect(page.getByText('nginx.conf saved to the workspace draft.')).toBeVisible()
  await expect(page.getByText('Unsaved changes', { exact: true })).toHaveCount(0)
  expect(fileSaveRequests(api, 'nginx.conf')).toHaveLength(1)
  expect(fileSaveRequests(api, 'nginx.conf')[0]?.ifMatch).toBe(firstDraftETag)
  expect(api.currentDraftETag()).not.toBe(firstDraftETag)

  await page.getByRole('button', { name: 'Create file' }).click()
  const createFile = page.locator('form[aria-label="Create file"]')
  await createFile.getByLabel('File path').fill('conf.d/new.conf')
  await createFile.getByLabel('Initial content').fill('server {\n  listen 8081;\n}\n')
  await createFile.getByRole('button', { name: 'Create file' }).click()
  await expect(createFile).toHaveCount(0)

  await expandDirectory(page, 'conf.d')
  await openFile(page, 'conf.d/new.conf')
  await page.getByRole('button', { name: 'Copy selected file' }).click()
  const copyFile = page.locator('form[aria-label="Copy file"]')
  await copyFile.getByLabel('Destination path').fill('conf.d/copied.conf')
  await copyFile.getByRole('button', { name: 'Copy file' }).click()
  await expect(copyFile).toHaveCount(0)

  await openFile(page, 'conf.d/copied.conf')
  await page.getByRole('button', { name: 'Rename selected file' }).click()
  const renameFile = page.locator('form[aria-label="Rename file"]')
  await renameFile.getByLabel('Destination path').fill('conf.d/renamed.conf')
  await renameFile.getByRole('button', { name: 'Rename file' }).click()
  await expect(renameFile).toHaveCount(0)
  await expect(page.getByRole('treeitem', { name: /renamed\.conf/ })).toBeVisible()

  await page.getByRole('button', { name: 'Delete selected file' }).click()
  await confirmNamedDeletion(page, 'conf.d/renamed.conf')
  await expect(page.getByRole('treeitem', { name: /renamed\.conf/ })).toHaveCount(0)

  await openFile(page, 'nginx.conf')
  await page.getByRole('button', { name: 'Search workspace files' }).click()
  await page.getByLabel('Search workspace text').fill('include conf.d/*.conf;')
  await page.getByLabel('Workspace review').getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Open search match nginx.conf line 3' })).toBeVisible()

  await page.getByRole('button', { name: 'Review current file diff' }).click()
  await expect(page.getByRole('region', { name: 'Unified configuration diff' })).toBeVisible()
  await expectRequest(api, 'GET', `/api/v1/config/workspaces/${api.workspaceId}/diff`, '?path=nginx.conf')

  await page.getByRole('button', { name: 'Review all file diffs' }).click()
  await expect(reviewPanel(page).getByText('conf.d/new.conf', { exact: true })).toBeVisible()
  await expectRequest(api, 'GET', `/api/v1/config/workspaces/${api.workspaceId}/diff`, '')

  await page.getByRole('button', { name: 'Review include dependencies' }).click()
  const review = reviewPanel(page)
  await expect(review.getByText('nginx.conf:3:3', { exact: true })).toBeVisible()
  await expect(review.getByText('conf.d/*.conf', { exact: true })).toBeVisible()
  await expect(review.getByText('Resolved', { exact: true })).toBeVisible()

  const draftBeforeGroups = api.currentDraftETag()
  const groupsBeforeCreate = api.currentGroupsETag()
  await page.getByRole('button', { name: 'Show logical groups' }).click()
  await page.getByRole('button', { name: 'Create logical group' }).click()
  const createGroup = page.locator('form[aria-label="Create logical group"]')
  await fillGroup(createGroup, 'Traffic entry points', '20', 'nginx.conf\nconf.d/site.conf')
  await createGroup.getByRole('button', { name: 'Save group' }).click()
  await expect(createGroup).toHaveCount(0)
  await expect(page.getByRole('treeitem', { name: /Traffic entry points, 2 members/ })).toBeVisible()
  expect(api.currentDraftETag()).toBe(draftBeforeGroups)
  expect(api.currentGroupsETag()).not.toBe(groupsBeforeCreate)

  const groupsBeforeUpdate = api.currentGroupsETag()
  await page.getByRole('treeitem', { name: /Traffic entry points, 2 members/ }).click()
  await page.getByRole('button', { name: 'Edit selected logical group' }).click()
  const editGroup = page.locator('form[aria-label="Edit logical group"]')
  await fillGroup(editGroup, 'Primary entry points', '10', 'nginx.conf')
  await editGroup.getByRole('button', { name: 'Save group' }).click()
  await expect(page.getByRole('treeitem', { name: /Primary entry points, 1 member/ })).toBeVisible()
  expect(api.currentDraftETag()).toBe(draftBeforeGroups)
  expect(api.currentGroupsETag()).not.toBe(groupsBeforeUpdate)

  const groupsBeforeDelete = api.currentGroupsETag()
  await page.getByRole('treeitem', { name: /Primary entry points, 1 member/ }).click()
  await page.getByRole('button', { name: 'Delete selected logical group' }).click()
  await confirmNamedDeletion(page, 'Primary entry points')
  await expect(page.getByRole('treeitem', { name: /Primary entry points/ })).toHaveCount(0)
  expect(api.currentDraftETag()).toBe(draftBeforeGroups)
  expect(api.currentGroupsETag()).not.toBe(groupsBeforeDelete)

  await page.getByRole('button', { name: `Delete workspace ${workspaceName}` }).click()
  await confirmNamedDeletion(page, workspaceName)
  await expect(page).toHaveURL(`${appOrigin}/config/workspaces?lang=en-US`)
  await expect(page.getByText('No workspaces yet. Create one to review draft configuration changes.')).toBeVisible()

  assertWorkflowRequestContract(api)
  await assertPrivateMarkerBoundary(page, api)
  const cookies = await context.cookies(appOrigin)
  expect(cookies).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ name: 'nginx_uix_session', httpOnly: true, sameSite: 'Strict' }),
    ]),
  )
  expect(await page.evaluate(() => document.cookie)).not.toContain('nginx_uix_session')
  api.assertContract()
})

test('conflict preserves local text until the operator copies, reviews, and reads server state', async ({
  context,
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 900 })
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: appOrigin })
  const api = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  await openSeededWorkspace(page, api)
  await openFile(page, 'nginx.conf')
  const editor = page.getByLabel('nginx.conf editor')
  const marker = 'conflict-local-text-E2E'
  await appendEditorText(page, editor, marker)

  const conflictIfMatch = api.currentDraftETag()
  api.forceConflict()
  await page.getByRole('button', { name: 'Save nginx.conf' }).click()
  await expect(page.getByText('This file changed on the server. Your local text has not been overwritten.')).toBeVisible()
  await expect(editor).toContainText(marker)
  await expect(page.getByRole('button', { name: 'Save nginx.conf' })).toBeDisabled()
  expectDraftMutationRequest(fileSaveRequests(api, 'nginx.conf').at(-1), conflictIfMatch)

  await page.getByRole('button', { name: 'Copy local content for nginx.conf' }).click()
  await expect(page.getByText('Local content for nginx.conf copied.')).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(marker)

  const reviewTrigger = page.getByRole('button', { name: 'View server diff for nginx.conf' })
  await reviewTrigger.focus()
  await reviewTrigger.click()
  const drawer = page.getByRole('dialog', { name: 'Workspace review' })
  await expect(drawer).toBeVisible()
  await expect(drawer.getByText('nginx.conf', { exact: true })).toBeVisible()
  await expect(editor).toContainText(marker)
  await page.keyboard.press('Escape')
  await expect(drawer).toBeHidden()
  await expect(reviewTrigger).toBeFocused()

  await page.getByRole('button', { name: 'Read server version for nginx.conf' }).click()
  await expect(page.getByText('This file changed on the server. Your local text has not been overwritten.')).toHaveCount(0)
  await page.getByRole('button', { name: 'Show editor task' }).click()
  await expect(editor).not.toContainText(marker)
  await expect(page.getByRole('button', { name: 'Save nginx.conf' })).toBeDisabled()
  api.assertContract()
})

for (const scenario of [
  {
    state: 'stale' as const,
    message: 'Production configuration changed. Create a new workspace to continue.',
  },
  {
    state: 'needs_attention' as const,
    message: 'Workspace consistency cannot be confirmed. Workspace ID:',
  },
]) {
  test(`${scenario.state} response keeps local text and enters a persistent read-only state`, async ({ page }) => {
    const api = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
    await openSeededWorkspace(page, api)
    await openFile(page, 'nginx.conf')
    const editor = page.getByLabel('nginx.conf editor')
    const marker = `${scenario.state}-local-text-E2E`
    await appendEditorText(page, editor, marker)

    const failureIfMatch = api.currentDraftETag()
    api.setWorkspaceState(scenario.state)
    await page.getByRole('button', { name: 'Save nginx.conf' }).click()
    await expect(page.getByText(scenario.message, { exact: scenario.state === 'stale' })).toBeVisible()
    if (scenario.state === 'needs_attention') {
      await expect(page.getByText(new RegExp(`${scenario.message} ${api.workspaceId}$`))).toBeVisible()
    }
    await expect(editor).toContainText(marker)
    await expect(page.getByRole('button', { name: 'Save nginx.conf' })).toBeDisabled()
    expectDraftMutationRequest(fileSaveRequests(api, 'nginx.conf').at(-1), failureIfMatch)
    api.assertContract()
  })
}

test('Agent unavailable is an inline persistent failure and never discards local text', async ({ page }) => {
  const api = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  await openSeededWorkspace(page, api)
  await openFile(page, 'nginx.conf')
  const editor = page.getByLabel('nginx.conf editor')
  const marker = 'agent-unavailable-local-text-E2E'
  await appendEditorText(page, editor, marker)

  const agentIfMatch = api.currentDraftETag()
  api.setAgentUnavailable()
  await page.getByRole('button', { name: 'Save nginx.conf' }).click()
  const message = 'Configuration Agent is unavailable. Production configuration and files are unaffected.'
  await expect(page.getByText(message, { exact: true })).toBeVisible()
  await expect(editor).toContainText(marker)
  await expect(page.getByRole('button', { name: 'Save nginx.conf' })).toBeDisabled()
  await expect(page.getByText(message, { exact: true })).toBeVisible()
  expectDraftMutationRequest(fileSaveRequests(api, 'nginx.conf').at(-1), agentIfMatch)
  api.assertContract()
})

test('session expiry preserves memory through login and requires a server refresh before saving', async ({ page }) => {
  const api = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  await openSeededWorkspace(page, api)
  await openFile(page, 'nginx.conf')
  const marker = 'session-expiry-local-text-E2E'
  await appendEditorText(page, page.getByLabel('nginx.conf editor'), marker)

  const expiryIfMatch = api.currentDraftETag()
  api.expireSession()
  await page.getByRole('button', { name: 'Save nginx.conf' }).click()
  await expect(page).toHaveURL((url) =>
    url.pathname === '/login' &&
      url.searchParams.get('lang') === 'en-US' &&
      url.searchParams.get('redirect') === `/config/workspaces/${api.workspaceId}?lang=en-US`,
  )
  await expect(page.getByRole('heading', { level: 2, name: 'Unsaved workspace changes' })).toBeVisible()
  await expect(page.getByText(/Local text remains in this browser session/)).toBeVisible()
  await expect(page.getByText('nginx.conf', { exact: true })).toBeVisible()
  expectDraftMutationRequest(fileSaveRequests(api, 'nginx.conf').at(-1), expiryIfMatch)

  await page.getByLabel('Username').fill('admin')
  await page.getByLabel('Password').fill('correct horse battery staple')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page).toHaveURL(`${appOrigin}/config/workspaces/${api.workspaceId}?lang=en-US`)
  const login = api.requests().find(
    ({ method, path }) => method === 'POST' && path === '/api/v1/auth/session',
  )
  expect(login).toMatchObject({ csrf: null, ifMatch: null })
  expect(login?.headers.origin).toBe(appOrigin)
  expect(login?.body).toBe(
    JSON.stringify({ username: 'admin', password: 'correct horse battery staple' }),
  )

  const editor = page.getByLabel('nginx.conf editor')
  await expect(editor).toContainText(marker)
  await expect(page.getByText('This file changed on the server. Your local text has not been overwritten.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Save nginx.conf' })).toBeDisabled()

  await page.getByRole('button', { name: 'Read server version for nginx.conf' }).click()
  await expect(editor).not.toContainText(marker)
  await appendEditorText(page, editor, 'post-login-refreshed-save-E2E')
  await expect(page.getByRole('button', { name: 'Save nginx.conf' })).toBeEnabled()
  await page.getByRole('button', { name: 'Save nginx.conf' }).click()
  await expect(page.getByText('nginx.conf saved to the workspace draft.')).toBeVisible()
  api.assertContract()
})

async function openSeededWorkspace(page: Page, api: WorkspaceAPIFixture): Promise<void> {
  await page.goto(`/config/workspaces/${api.workspaceId}?lang=en-US`)
  await expect(page.getByRole('heading', { level: 1, name: 'E2E workspace' })).toBeVisible()
  await expect(page.getByRole('tree', { name: 'Physical configuration files' })).toBeVisible()
}

async function openFile(page: Page, path: string): Promise<void> {
  const item = page.getByRole('treeitem', { name: new RegExp(`${escapeRegExp(path.split('/').at(-1) ?? path)}`) })
  await expect(item).toBeVisible()
  await item.click()
  await expect(page.getByLabel(`${path} editor`)).toBeVisible()
}

async function expandDirectory(page: Page, name: string): Promise<void> {
  const directory = page.getByRole('treeitem', { name: new RegExp(`^${escapeRegExp(name)}, Directory`) })
  if ((await directory.getAttribute('aria-expanded')) !== 'true') {
    await directory.click()
  }
  await expect(directory).toHaveAttribute('aria-expanded', 'true')
}

async function appendEditorText(page: Page, editor: Locator, text: string): Promise<void> {
  await expect(editor).toBeVisible()
  await editor.click()
  await editor.press('ControlOrMeta+End')
  await page.keyboard.insertText(text)
  await expect(editor).toContainText(text)
}

async function fillGroup(
  form: Locator,
  name: string,
  sortOrder: string,
  members: string,
): Promise<void> {
  await form.getByLabel('Group name').fill(name)
  await form.getByLabel('Sort order').fill(sortOrder)
  await form.getByLabel('Member paths, one per line').fill(members)
}

async function confirmNamedDeletion(page: Page, name: string): Promise<void> {
  const dialog = page.getByRole('dialog', { name: /Delete/ })
  await expect(dialog).toBeVisible()
  await dialog.getByLabel(`Type ${name} exactly to confirm`).fill(name)
  await dialog.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(dialog).toHaveCount(0)
}

function fileSaveRequests(api: WorkspaceAPIFixture, path: string): readonly WorkspaceAPIRequest[] {
  return api.requests().filter(
    (request) =>
      request.method === 'PUT' &&
      request.path === `/api/v1/config/workspaces/${api.workspaceId}/files` &&
      request.query === `?path=${encodeURIComponent(path)}`,
  )
}

function expectDraftMutationRequest(
  request: WorkspaceAPIRequest | undefined,
  expectedETag: string,
): void {
  expect(request).toBeDefined()
  expect(request?.headers.cookie).toContain('nginx_uix_session=e2e-session')
  expect(request?.headers.origin).toBe(appOrigin)
  expect(request?.csrf).toBe(csrfToken)
  expect(request?.ifMatch).toBe(expectedETag)
}

function reviewPanel(page: Page): Locator {
  return page.locator('.workspace-review-panel').getByLabel('Workspace review')
}

async function expectRequest(
  api: WorkspaceAPIFixture,
  method: string,
  path: string,
  query: string,
): Promise<void> {
  await expect
    .poll(() =>
      api.requests().some(
        (request) =>
          request.method === method && request.path === path && request.query === query,
      ),
    )
    .toBe(true)
}

function assertWorkflowRequestContract(api: WorkspaceAPIFixture): void {
  const requests = api.requests()
  for (const request of requests) {
    expect(request.headers.cookie).toContain('nginx_uix_session=e2e-session')
  }

  const configMutations = requests.filter(
    ({ method, path }) => method !== 'GET' && path.startsWith('/api/v1/config/'),
  )
  expect(configMutations.length).toBeGreaterThanOrEqual(9)
  for (const request of configMutations) {
    expect(request.headers.origin).toBe(appOrigin)
    expect(request.csrf).toBe(csrfToken)
  }

  const draftMutations = configMutations.filter(({ path }) => !path.startsWith('/api/v1/config/groups'))
  const draftIfMatches = draftMutations
    .filter(({ method, path }) => !(method === 'POST' && path === '/api/v1/config/workspaces'))
    .map(({ ifMatch }) => ifMatch)
  expect(draftIfMatches.every((etag) => /^"draft-v1:[0-9a-f]{64}"$/.test(etag ?? ''))).toBe(true)
  expect(new Set(draftIfMatches).size).toBe(draftIfMatches.length)

  const groupMutations = configMutations.filter(({ path }) => path.startsWith('/api/v1/config/groups'))
  expect(groupMutations).toHaveLength(3)
  expect(groupMutations.every(({ ifMatch }) => /^"groups-v1:[0-9a-f]{64}"$/.test(ifMatch ?? ''))).toBe(true)
  expect(new Set(groupMutations.map(({ ifMatch }) => ifMatch)).size).toBe(3)
}

async function assertPrivateMarkerBoundary(page: Page, api: WorkspaceAPIFixture): Promise<void> {
  const requests = api.requests()
  const bodiesWithMarker = requests.filter(({ body }) => body?.includes(privateMarker) === true)
  expect(bodiesWithMarker).toHaveLength(1)
  expect(bodiesWithMarker[0]).toMatchObject({ method: 'PUT', query: '?path=nginx.conf' })
  for (const request of requests) {
    expect(request.path).not.toContain(privateMarker)
    expect(request.query).not.toContain(privateMarker)
    expect(JSON.stringify(request.headers)).not.toContain(privateMarker)
  }
  await assertOnlyLocalePreferenceStorage(page)
  expect(await page.evaluate(() => navigator.serviceWorker.getRegistrations().then((items) => items.length))).toBe(0)
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

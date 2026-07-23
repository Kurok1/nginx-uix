/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import { randomUUID } from 'node:crypto'
import { lstat, readFile } from 'node:fs/promises'

import { expect, type APIRequestContext, type Locator, type Page, test } from '@playwright/test'

const dockerEnvironmentNames = [
  'NGINX_UIX_E2E_BASE_URL',
  'NGINX_UIX_E2E_USERNAME',
  'NGINX_UIX_E2E_PASSWORD_FILE',
] as const
const configuredDockerEnvironmentCount = dockerEnvironmentNames.filter(
  (name) => process.env[name] !== undefined && process.env[name] !== '',
).length
const dockerViewportWidths = [1068, 833, 734, 640, 480, 320] as const

test.describe.configure({ mode: 'serial' })
test.skip(
  configuredDockerEnvironmentCount === 0,
  'real Docker browser acceptance runs in tests/docker/workspace.sh',
)

test('real Docker workspace flow covers persistence, responsive layout and keyboard access', async ({
  context,
  page,
}) => {
  test.setTimeout(120_000)

  const environment = await loadEnvironment()
  const workspaceName = `docker-workspace-${randomUUID()}`
  const savedMarker = `docker-persistence-${randomUUID()}`
  const localConflictMarker = `docker-local-conflict-${randomUUID()}`
  const serverConflictMarker = `docker-server-conflict-${randomUUID()}`
  let csrfToken = ''
  let workspaceID = ''

  await context.grantPermissions(['clipboard-read', 'clipboard-write'], {
    origin: environment.baseURL.origin,
  })

  try {
    await login(page, environment)
    csrfToken = await sessionCSRFToken(page, environment)
    await expect
      .poll(async () => page.evaluate(() => [localStorage.length, sessionStorage.length]))
      .toEqual([0, 0])

    await page.goto(urlFor(environment.baseURL, '/config/workspaces'))
    const createResponsePromise = waitForAPIResponse(page, 'POST', '/api/v1/config/workspaces')
    await page.getByRole('button', { name: 'Create workspace' }).click()
    await page.getByLabel('Workspace name').fill(workspaceName)
    await page.getByRole('button', { name: 'Create', exact: true }).click()
    const createResponse = await createResponsePromise
    expect(createResponse.status()).toBe(201)
    const createdWorkspace: unknown = await createResponse.json()
    workspaceID = requiredStringProperty(createdWorkspace, 'id')
    expect(createResponse.headers()['etag']).toBe(
      requiredStringProperty(createdWorkspace, 'draft_etag'),
    )

    await expect(page).toHaveURL(
      urlFor(environment.baseURL, `/config/workspaces/${encodeURIComponent(workspaceID)}`),
    )
    await expect(page.getByRole('heading', { level: 1, name: workspaceName })).toBeVisible()

    await openFile(page, 'nginx.conf')
    const editor = page.getByLabel('nginx.conf editor')
    await appendEditorText(page, editor, `\n# ${savedMarker}\n`)

    const saveResponsePromise = waitForAPIResponse(
      page,
      'PUT',
      `/api/v1/config/workspaces/${workspaceID}/files`,
    )
    await page.getByRole('button', { name: 'Save nginx.conf' }).click()
    expect((await saveResponsePromise).status()).toBe(200)
    await expect(page.getByText('nginx.conf saved to the workspace draft.')).toBeVisible()

    await page.getByRole('button', { name: 'Review current file diff' }).click()
    const diff = page.getByRole('region', { name: 'Unified configuration diff' })
    await expect(diff).toBeVisible()
    await expect(diff).toContainText(savedMarker)

    await page.reload()
    await expect(page.getByRole('heading', { level: 1, name: workspaceName })).toBeVisible()
    await page.goto(urlFor(environment.baseURL, '/config/workspaces'))
    await page.getByRole('link', { name: new RegExp(escapeRegExp(workspaceName)) }).click()
    await expect(page.getByRole('heading', { level: 1, name: workspaceName })).toBeVisible()
    await openFile(page, 'nginx.conf')
    const reopenedEditor = page.getByLabel('nginx.conf editor')
    await expect(reopenedEditor).toContainText(savedMarker)

    await appendEditorText(page, reopenedEditor, `\n# ${localConflictMarker}\n`)
    const currentFile = await getConfigFile(
      context.request,
      environment.baseURL,
      workspaceID,
      'nginx.conf',
    )
    const serverContent = `${currentFile.content.replace(/\n?$/, '\n')}# ${serverConflictMarker}\n`
    const directMutation = await context.request.put(
      configFileURL(environment.baseURL, workspaceID, 'nginx.conf'),
      {
        data: { content: serverContent },
        headers: mutationHeaders(environment.baseURL, csrfToken, currentFile.draftETag),
      },
    )
    expect(directMutation.status()).toBe(200)
    const rotatedETag = requiredStringProperty(await directMutation.json(), 'draft_etag')
    expect(rotatedETag).not.toBe(currentFile.draftETag)
    expect(directMutation.headers()['etag']).toBe(rotatedETag)

    const conflictResponsePromise = waitForAPIResponse(
      page,
      'PUT',
      `/api/v1/config/workspaces/${workspaceID}/files`,
    )
    await page.getByRole('button', { name: 'Save nginx.conf' }).click()
    expect((await conflictResponsePromise).status()).toBe(409)
    await expect(
      page.getByText('This file changed on the server. Your local text has not been overwritten.'),
    ).toBeVisible()
    await expect(reopenedEditor).toContainText(localConflictMarker)
    await expect(reopenedEditor).not.toContainText(serverConflictMarker)
    await expect(page.getByRole('button', { name: 'Save nginx.conf' })).toBeDisabled()

    const clipboardAvailable = await test.step('detect secure Clipboard API capability', () =>
      page.evaluate(
        () =>
          window.isSecureContext &&
          typeof navigator.clipboard?.writeText === 'function' &&
          typeof navigator.clipboard?.readText === 'function',
      ),
    )
    if (clipboardAvailable) {
      await test.step('copy and verify the preserved local conflict text', async () => {
        await page.getByRole('button', { name: '复制本地内容 nginx.conf' }).click()
        await expect(page.getByText('Local content for nginx.conf copied.')).toBeVisible()
        expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
          localConflictMarker,
        )
      })
    } else {
      test.info().annotations.push({
        type: 'limitation',
        description:
          'copy-specific checks skipped because the real HTTP Docker origin does not expose the secure Clipboard API',
      })
      console.info(
        'Docker browser limitation: secure Clipboard API unavailable; copy-specific checks skipped',
      )
    }

    await page.getByRole('button', { name: '读取服务器版本 nginx.conf' }).click()
    await expect(
      page.getByText('This file changed on the server. Your local text has not been overwritten.'),
    ).toHaveCount(0)
    await expect(reopenedEditor).toContainText(serverConflictMarker)
    await expect(reopenedEditor).not.toContainText(localConflictMarker)

    await verifyResponsiveAndKeyboardBehavior(
      page,
      environment.baseURL,
      workspaceID,
      workspaceName,
    )

    const deleteResponsePromise = waitForAPIResponse(
      page,
      'DELETE',
      `/api/v1/config/workspaces/${workspaceID}`,
    )
    await page.getByRole('button', { name: `Delete workspace ${workspaceName}` }).click()
    await confirmNamedDeletion(page, workspaceName)
    expect((await deleteResponsePromise).status()).toBe(204)
    workspaceID = ''
    await expect(page).toHaveURL(urlFor(environment.baseURL, '/config/workspaces'))

    const logout = page.getByRole('button', { name: '退出登录' })
    const precedingNavigationLink = page
      .getByRole('navigation', { name: 'Global navigation' })
      .getByRole('link', { name: 'Recovery & History', exact: true })
    await precedingNavigationLink.focus()
    await precedingNavigationLink.press('Tab')
    await expect(logout).toBeFocused()
    await assertVisibleFocus(logout)
    const logoutResponsePromise = waitForAPIResponse(page, 'DELETE', '/api/v1/auth/session')
    await logout.press('Enter')
    expect((await logoutResponsePromise).status()).toBe(204)
    await expect(page).toHaveURL(urlFor(environment.baseURL, '/login'))
  } finally {
    if (workspaceID !== '' && csrfToken !== '') {
      await cleanupWorkspace(
        context.request,
        environment.baseURL,
        workspaceID,
        workspaceName,
        csrfToken,
      )
    }
  }
})

interface DockerEnvironment {
  baseURL: URL
  username: string
  password: string
}

interface ConfigFileState {
  content: string
  draftETag: string
}

async function verifyResponsiveAndKeyboardBehavior(
  page: Page,
  baseURL: URL,
  workspaceID: string,
  workspaceName: string,
): Promise<void> {
  const workspaceURL = urlFor(
    baseURL,
    `/config/workspaces/${encodeURIComponent(workspaceID)}`,
  )
  for (const width of dockerViewportWidths) {
    await test.step(`verify real container workspace at ${width}px`, async () => {
      await page.setViewportSize({ width, height: 900 })
      await page.goto(workspaceURL)
      await expect(page.getByRole('heading', { level: 1, name: workspaceName })).toBeVisible()
      await assertResponsiveWorkspaceLayout(page, width)
      await assertNoPageOverflow(page)
      await assertMinimumVisibleTargets(page)
    })
  }

  await page.setViewportSize({ width: 1068, height: 900 })
  await page.goto(workspaceURL)
  const tree = page.getByRole('tree', { name: 'Physical configuration files' })
  const rows = tree.getByRole('treeitem')
  const first = rows.first()
  const last = rows.last()
  await first.focus()
  await assertVisibleFocus(first)
  await page.keyboard.press('End')
  await expect(last).toBeFocused()
  await page.keyboard.press('Home')
  await expect(first).toBeFocused()
  await page.keyboard.press('ArrowRight')
  await expect(first).toHaveAttribute('aria-expanded', 'true')
  await page.keyboard.press('ArrowDown')
  await expect(rows.nth(1)).toBeFocused()
  await page.keyboard.press('ArrowLeft')
  await expect(first).toBeFocused()

  const reviewTrigger = page.getByRole('button', { name: 'Open workspace review' })
  await reviewTrigger.focus()
  await reviewTrigger.press('Enter')
  const reviewDrawer = page.getByRole('dialog', { name: 'Workspace review' })
  await expect(reviewDrawer).toBeVisible()
  await expect(reviewDrawer.getByRole('button', { name: 'Close review' })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(reviewDrawer).toBeHidden()
  await expect(reviewTrigger).toBeFocused()

  await page.goto(urlFor(baseURL, '/config/operations'))
  await expect(page.getByRole('heading', { level: 1, name: 'Recovery & History' })).toBeVisible()
  const overviewTab = page.getByRole('tab', { name: 'Overview' })
  const backupsTab = page.getByRole('tab', { name: 'Backups' })
  await overviewTab.focus()
  await assertVisibleFocus(overviewTab)
  await overviewTab.press('ArrowRight')
  await expect(backupsTab).toBeFocused()
  await expect(backupsTab).toHaveAttribute('aria-selected', 'true')

  await page.goto(workspaceURL)
  const deleteTrigger = page.getByRole('button', { name: `Delete workspace ${workspaceName}` })
  await deleteTrigger.focus()
  await assertVisibleFocus(deleteTrigger)
  await deleteTrigger.press('Enter')
  const deleteDialog = page.getByRole('dialog', { name: new RegExp(escapeRegExp(workspaceName)) })
  await expect(deleteDialog).toBeVisible()
  await expect(deleteDialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(deleteDialog).toBeHidden()
  await expect(deleteTrigger).toBeFocused()
}

async function assertResponsiveWorkspaceLayout(
  page: Page,
  width: (typeof dockerViewportWidths)[number],
): Promise<void> {
  const tree = page.locator('.workspace-tree-panel')
  const editor = page.locator('.workspace-editor-panel')
  const review = page.locator('.workspace-review-panel')
  const selector = page.locator('.workspace-file-selector')
  const taskTabs = page.getByRole('navigation', { name: 'Workspace tasks' })
  const reviewTrigger = page.getByRole('button', { name: 'Open workspace review' })

  if (width >= 833) {
    await expect(tree).toBeVisible()
    await expect(editor).toBeVisible()
    await expect(review).toBeHidden()
    await expect(selector).toBeHidden()
    await expect(taskTabs).toBeHidden()
    await expect(reviewTrigger).toBeVisible()
    return
  }
  if (width === 734) {
    await expect(tree).toBeHidden()
    await expect(editor).toBeVisible()
    await expect(review).toBeHidden()
    await expect(selector).toBeVisible()
    await expect(taskTabs).toBeHidden()
    await expect(reviewTrigger).toBeVisible()
    return
  }

  await expect(taskTabs).toBeVisible()
  await expect(reviewTrigger).toBeHidden()
  await expect(tree).toHaveAttribute('aria-hidden', 'false')
  await expect(editor).toHaveAttribute('aria-hidden', 'true')
  await expect(review).toHaveAttribute('aria-hidden', 'true')
  await expect(selector).toHaveAttribute('aria-hidden', 'false')
}

async function assertNoPageOverflow(page: Page): Promise<void> {
  const dimensions = await page.evaluate(() => ({
    bodyClientWidth: document.body.clientWidth,
    bodyScrollWidth: document.body.scrollWidth,
    documentClientWidth: document.documentElement.clientWidth,
    documentScrollWidth: document.documentElement.scrollWidth,
  }))
  expect(dimensions.documentScrollWidth).toBeLessThanOrEqual(dimensions.documentClientWidth)
  expect(dimensions.bodyScrollWidth).toBeLessThanOrEqual(dimensions.bodyClientWidth)
}

async function assertMinimumVisibleTargets(page: Page): Promise<void> {
  const undersized = await page
    .locator(
      'a[href], button, input, select, textarea, [role="treeitem"], [tabindex]:not([tabindex="-1"])',
    )
    .evaluateAll((elements) =>
      elements.flatMap((element) => {
        if (element.closest('[aria-hidden="true"], [inert]') !== null) return []
        const rect = element.getBoundingClientRect()
        const style = getComputedStyle(element)
        if (
          rect.width === 0 ||
          rect.height === 0 ||
          element.getClientRects().length === 0 ||
          style.display === 'none' ||
          style.visibility === 'hidden'
        ) {
          return []
        }
        if (rect.width >= 43.5 && rect.height >= 43.5) return []
        return [{ element: element.outerHTML.slice(0, 180), height: rect.height, width: rect.width }]
      }),
    )
  expect(undersized, 'visible interactive targets smaller than 44×44 CSS pixels').toEqual([])
}

async function assertVisibleFocus(locator: Locator): Promise<void> {
  const focus = await locator.evaluate((element) => {
    const style = getComputedStyle(element)
    const rect = element.getBoundingClientRect()
    return {
      outlineStyle: style.outlineStyle,
      outlineWidth: Number.parseFloat(style.outlineWidth),
      visibleInViewport:
        rect.bottom > 0 &&
        rect.right > 0 &&
        rect.top < window.innerHeight &&
        rect.left < window.innerWidth,
    }
  })
  expect(focus.outlineStyle).not.toBe('none')
  expect(focus.outlineWidth).toBeGreaterThanOrEqual(2)
  expect(focus.visibleInViewport).toBe(true)
}

async function loadEnvironment(): Promise<DockerEnvironment> {
  const baseURL = parseBaseURL(requiredEnvironment('NGINX_UIX_E2E_BASE_URL'))
  const username = singleLineValue('NGINX_UIX_E2E_USERNAME')
  const passwordFile = requiredEnvironment('NGINX_UIX_E2E_PASSWORD_FILE')
  const password = await readPassword(passwordFile)
  return { baseURL, username, password }
}

function requiredEnvironment(name: string): string {
  const value = process.env[name]
  if (value === undefined || value === '') {
    throw new Error(`${name} must be set for the real Docker browser test`)
  }
  return value
}

function singleLineValue(name: string): string {
  const value = requiredEnvironment(name)
  if (/[\r\n\0]/u.test(value)) {
    throw new Error(`${name} must be a non-empty single-line value`)
  }
  return value
}

function parseBaseURL(raw: string): URL {
  if (raw.trim() !== raw || /[\r\n\0]/u.test(raw)) {
    throw new Error('NGINX_UIX_E2E_BASE_URL must not contain whitespace or control characters')
  }
  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    throw new Error('NGINX_UIX_E2E_BASE_URL must be an absolute HTTP(S) URL')
  }
  if (
    !['http:', 'https:'].includes(parsed.protocol) ||
    parsed.username !== '' ||
    parsed.password !== '' ||
    parsed.search !== '' ||
    parsed.hash !== '' ||
    parsed.pathname !== '/'
  ) {
    throw new Error('NGINX_UIX_E2E_BASE_URL must contain only an HTTP(S) origin')
  }
  return new URL(parsed.origin)
}

async function readPassword(path: string): Promise<string> {
  const metadata = await lstat(path)
  if (metadata.isSymbolicLink() || !metadata.isFile()) {
    throw new Error('NGINX_UIX_E2E_PASSWORD_FILE must identify a regular file, not a symlink')
  }
  const raw = await readFile(path, 'utf8')
  const password = raw.endsWith('\r\n')
    ? raw.slice(0, -2)
    : raw.endsWith('\n')
      ? raw.slice(0, -1)
      : raw
  if (password === '' || /[\r\n\0]/u.test(password)) {
    throw new Error('NGINX_UIX_E2E_PASSWORD_FILE must contain one non-empty password line')
  }
  return password
}

async function login(page: Page, environment: DockerEnvironment): Promise<void> {
  await page.goto(urlFor(environment.baseURL, '/login'))
  await page.getByLabel('用户名').fill(environment.username)
  await page.getByLabel('密码').fill(environment.password)
  const responsePromise = waitForAPIResponse(page, 'POST', '/api/v1/auth/session')
  await page.getByRole('button', { name: '登录' }).click()
  const response = await responsePromise
  expect(response.status()).toBe(200)
  await expect(page).toHaveURL(urlFor(environment.baseURL, '/'))
}

async function sessionCSRFToken(page: Page, environment: DockerEnvironment): Promise<string> {
  const response = await page.request.get(urlFor(environment.baseURL, '/api/v1/auth/session'))
  expect(response.status()).toBe(200)
  return requiredStringProperty(await response.json(), 'csrf_token')
}

async function openFile(page: Page, path: string): Promise<void> {
  const filename = path.split('/').at(-1) ?? path
  const item = page.getByRole('treeitem', { name: new RegExp(escapeRegExp(filename)) })
  await expect(item).toBeVisible()
  await item.click()
  await expect(page.getByLabel(`${path} editor`)).toBeVisible()
}

async function appendEditorText(page: Page, editor: Locator, text: string): Promise<void> {
  await expect(editor).toBeVisible()
  await editor.click()
  await editor.press('ControlOrMeta+End')
  await page.keyboard.insertText(text)
  await expect(editor).toContainText(text.trim())
}

async function confirmNamedDeletion(page: Page, expectedName: string): Promise<void> {
  const modal = page.getByRole('dialog')
  await modal.getByLabel(`Type ${expectedName} exactly to confirm`).fill(expectedName)
  await modal.getByRole('button', { name: 'Delete', exact: true }).click()
}

async function getConfigFile(
  request: APIRequestContext,
  baseURL: URL,
  workspaceID: string,
  path: string,
): Promise<ConfigFileState> {
  const response = await request.get(configFileURL(baseURL, workspaceID, path))
  expect(response.status()).toBe(200)
  const payload: unknown = await response.json()
  const state = {
    content: requiredStringProperty(payload, 'content', true),
    draftETag: requiredStringProperty(payload, 'draft_etag'),
  }
  expect(response.headers()['etag']).toBe(state.draftETag)
  return state
}

async function cleanupWorkspace(
  request: APIRequestContext,
  baseURL: URL,
  workspaceID: string,
  workspaceName: string,
  csrfToken: string,
): Promise<void> {
  const workspaceURL = urlFor(
    baseURL,
    `/api/v1/config/workspaces/${encodeURIComponent(workspaceID)}`,
  )
  const getResponse = await request.get(workspaceURL)
  if (getResponse.status() === 404) {
    return
  }
  expect(getResponse.status()).toBe(200)
  const draftETag = requiredStringProperty(await getResponse.json(), 'draft_etag')
  expect(getResponse.headers()['etag']).toBe(draftETag)
  const deleteResponse = await request.delete(workspaceURL, {
    data: { confirm_name: workspaceName },
    headers: mutationHeaders(baseURL, csrfToken, draftETag),
  })
  expect([204, 404]).toContain(deleteResponse.status())
}

function mutationHeaders(baseURL: URL, csrfToken: string, draftETag: string): Record<string, string> {
  return {
    'Content-Type': 'application/json',
    'If-Match': draftETag,
    Origin: baseURL.origin,
    'X-CSRF-Token': csrfToken,
  }
}

function configFileURL(baseURL: URL, workspaceID: string, path: string): string {
  const url = new URL(
    `/api/v1/config/workspaces/${encodeURIComponent(workspaceID)}/files`,
    baseURL,
  )
  url.searchParams.set('path', path)
  return url.href
}

function waitForAPIResponse(page: Page, method: string, pathname: string) {
  return page.waitForResponse((response) => {
    const request = response.request()
    return request.method() === method && new URL(response.url()).pathname === pathname
  })
}

function requiredStringProperty(payload: unknown, property: string, allowEmpty = false): string {
  if (typeof payload !== 'object' || payload === null) {
    throw new Error(`API response must be an object containing ${property}`)
  }
  const value = Reflect.get(payload, property)
  if (typeof value !== 'string' || (!allowEmpty && value === '')) {
    throw new Error(`API response property ${property} must be a non-empty string`)
  }
  return value
}

function urlFor(baseURL: URL, path: string): string {
  return new URL(path, baseURL).href
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&')
}

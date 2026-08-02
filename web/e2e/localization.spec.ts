/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 1.1.0
 */
import { expect, test, type Locator, type Page } from '@playwright/test'

import {
  apiError,
  appOrigin,
  assertNoAxeViolations,
  assertOnlyLocalePreferenceStorage,
  authenticatedSession,
  healthyStatus,
  installAPIMocks,
  installWorkspaceAPIFixture,
  setAuthenticatedCookie,
} from './support/api'
import { installReleaseAPIFixture } from './support/release'

const anonymousSession = {
  status: 401,
  body: apiError('unauthenticated', 'Authentication required'),
}

const localeCases = [
  {
    locale: 'zh-CN',
    username: '用户名',
    password: '密码',
    signIn: '登录',
    dashboard: '运行状态',
    openNavigation: '打开导航',
    filesTree: '物理配置文件',
    editor: 'nginx.conf 编辑器',
    save: '保存 nginx.conf',
    saved: 'nginx.conf 已保存到工作区草稿。',
    allDiff: '审查全部文件差异',
    unifiedDiff: '统一格式配置差异',
    deleteWorkspace: '删除工作区 E2E workspace',
    deleteDialog: '删除工作区“E2E workspace”？',
    confirmation: '准确输入 E2E workspace 以确认',
    deleteAction: '删除',
    noWorkspaces: '尚无工作区。请创建一个工作区以审查配置草稿变更。',
    published: '已发布',
    publishedSafety: '发布校验与运行确认已完成',
    releasePrefix: '发布 ID：',
    readOnly: '此工作区为只读。',
  },
  {
    locale: 'en-US',
    username: 'Username',
    password: 'Password',
    signIn: 'Sign in',
    dashboard: 'Runtime status',
    openNavigation: 'Open navigation',
    filesTree: 'Physical configuration files',
    editor: 'nginx.conf editor',
    save: 'Save nginx.conf',
    saved: 'nginx.conf saved to the workspace draft.',
    allDiff: 'Review all file diffs',
    unifiedDiff: 'Unified configuration diff',
    deleteWorkspace: 'Delete workspace E2E workspace',
    deleteDialog: 'Delete workspace “E2E workspace”?',
    confirmation: 'Type E2E workspace exactly to confirm',
    deleteAction: 'Delete',
    noWorkspaces: 'No workspaces yet. Create one to review draft configuration changes.',
    published: 'Published',
    publishedSafety: 'Publication validation and runtime confirmation completed',
    releasePrefix: 'Release ID: ',
    readOnly: 'This workspace is read-only.',
  },
] as const

const responsiveWidths = [1440, 833, 480] as const

for (const copy of localeCases) {
  test(`${copy.locale} login keeps the URL locale through authentication`, async ({ page }) => {
    const api = await installAPIMocks(page, {
      session: anonymousSession,
      login: {
        status: 200,
        body: authenticatedSession,
        headers: {
          'Set-Cookie': 'nginx_uix_session=e2e-session; Path=/; HttpOnly; SameSite=Strict',
        },
      },
      status: { status: 200, body: healthyStatus },
    })

    await page.goto(`/login?lang=${copy.locale}`)
    await expect(page.locator('html')).toHaveAttribute('lang', copy.locale)
    await expect(page.getByRole('combobox', { name: copy.locale === 'zh-CN' ? '语言' : 'Language' })).toHaveValue(copy.locale)
    await page.getByLabel(copy.username).fill('admin')
    await page.getByLabel(copy.password).fill('correct horse battery staple')
    await page.getByRole('button', { name: copy.signIn }).click()

    await expect(page).toHaveURL(`${appOrigin}/?lang=${copy.locale}`)
    await expect(page.getByRole('heading', { level: 1, name: copy.dashboard })).toBeVisible()
    await assertOnlyLocalePreferenceStorage(page)
    await assertNoAxeViolations(page)
    api.assertContract()
  })

  test(`${copy.locale} workspace edit and named deletion stay localized`, async ({ context, page }) => {
    await setAuthenticatedCookie(context)
    const api = await installWorkspaceAPIFixture(page, { seedWorkspace: true })

    await page.goto(`/config/workspaces/${api.workspaceId}?lang=${copy.locale}`)
    await expect(page.getByRole('tree', { name: copy.filesTree })).toBeVisible()
    await page.getByRole('treeitem', { name: /nginx\.conf/u }).click()
    const editor = page.getByLabel(copy.editor)
    await appendEditorText(page, editor, `# localized-${copy.locale}-E2E`)
    await page.getByRole('button', { name: copy.save }).click()
    await expect(page.getByText(copy.saved, { exact: true })).toBeVisible()
    const review = page.locator('.workspace-review-panel')
    await review.getByRole('button', { name: copy.allDiff }).click()
    await expect(review.getByRole('region', { name: copy.unifiedDiff })).toBeVisible()

    await page.getByRole('button', { name: copy.deleteWorkspace }).click()
    const dialog = page.getByRole('dialog', { name: copy.deleteDialog })
    const deleteAction = dialog.getByRole('button', { name: copy.deleteAction, exact: true })
    await expect(dialog).toBeVisible()
    await expect(deleteAction).toBeDisabled()
    await dialog.getByLabel(copy.confirmation).fill('E2E workspace')
    await expect(deleteAction).toBeEnabled()
    await deleteAction.click()

    await expect(page).toHaveURL(`${appOrigin}/config/workspaces?lang=${copy.locale}`)
    await expect(page.getByText(copy.noWorkspaces, { exact: true })).toBeVisible()
    expect(
      api.requests().some(
        ({ method, path }) =>
          method === 'PUT' &&
          path === `/api/v1/config/workspaces/${api.workspaceId}/files`,
      ),
    ).toBe(true)
    await assertOnlyLocalePreferenceStorage(page)
    await assertNoAxeViolations(page)
    api.assertContract()
  })

  test(`${copy.locale} published workspace reopens as an immutable release snapshot`, async ({ context, page }) => {
    await setAuthenticatedCookie(context)
    const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
    const release = await installReleaseAPIFixture(page, workspace, { initialOutcome: 'succeeded' })

    await page.goto(`/config/workspaces/${workspace.workspaceId}?lang=${copy.locale}`)
    await expect(page.locator('.workspace-header').getByText(copy.published)).toBeVisible()
    await expect(
      page
        .locator('.workspace-header')
        .getByText(`${copy.releasePrefix}${release.releaseId}`, { exact: true }),
    ).toBeVisible()
    await expect(page.getByText(copy.publishedSafety, { exact: true })).toBeVisible()
    await page.getByRole('treeitem', { name: /nginx\.conf/u }).click()
    await expect(page.getByText(copy.readOnly, { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: copy.save })).toBeDisabled()
    await expect(page.getByRole('button', { name: copy.deleteWorkspace })).toBeDisabled()
    expect(workspace.requests().filter(({ method }) => method !== 'GET')).toHaveLength(0)
    await assertOnlyLocalePreferenceStorage(page)
    await assertNoAxeViolations(page)
    release.assertContract()
    workspace.assertContract()
  })

  for (const width of responsiveWidths) {
    test(`${copy.locale} dashboard is reachable and reflows at ${width}px`, async ({ context, page }) => {
      await setAuthenticatedCookie(context)
      await page.setViewportSize({ width, height: 900 })
      const api = await installAPIMocks(page, {
        session: { status: 200, body: authenticatedSession },
        status: { status: 200, body: healthyStatus },
      })

      await page.goto(`/?lang=${copy.locale}`)
      await expect(page.locator('html')).toHaveAttribute('lang', copy.locale)
      await expect(page.getByRole('heading', { level: 1, name: copy.dashboard })).toBeVisible()
      if (width <= 833) {
        await expect(page.getByRole('button', { name: copy.openNavigation })).toBeVisible()
      } else {
        await expect(page.locator('.global-nav__links')).toBeVisible()
      }
      await assertNoPageOverflow(page)
      await assertMinimumTargets(page)
      await assertOnlyLocalePreferenceStorage(page)
      await assertNoAxeViolations(page)
      api.assertContract()
    })
  }
}

async function appendEditorText(page: Page, editor: Locator, text: string): Promise<void> {
  await expect(editor).toBeVisible()
  await editor.click()
  await editor.press('ControlOrMeta+End')
  await page.keyboard.insertText(text)
  await expect(editor).toContainText(text)
}

async function assertNoPageOverflow(page: Page): Promise<void> {
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }))
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.clientWidth)
}

async function assertMinimumTargets(page: Page): Promise<void> {
  const undersized = await page
    .locator('a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])')
    .evaluateAll((elements) =>
      elements.flatMap((element) => {
        const rect = element.getBoundingClientRect()
        if (rect.width === 0 || rect.height === 0 || element.getClientRects().length === 0) return []
        if (rect.width >= 43.5 && rect.height >= 43.5) return []
        return [{ element: element.outerHTML.slice(0, 160), height: rect.height, width: rect.width }]
      }),
    )
  expect(undersized, 'visible interactive targets smaller than 44×44 CSS pixels').toEqual([])
}

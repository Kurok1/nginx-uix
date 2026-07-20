/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

import {
  installWorkspaceAPIFixture,
  setAuthenticatedCookie,
  type WorkspaceAPIFixture,
} from './support/api'
import { installReleaseAPIFixture } from './support/release'

const releaseViewports = [1068, 833, 734, 640, 480, 320] as const

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

for (const width of releaseViewports) {
  test(`safe publication remains named, recoverable and reflowed at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
    const release = await installReleaseAPIFixture(page, workspace, { terminalDelayMs: 400 })
    await openPublishableWorkspace(page, workspace, width)

    const checkTrigger = page.getByRole('button', { name: 'Check publication' })
    await expect(checkTrigger).toBeEnabled()
    await checkTrigger.click()
    await expect(page.getByText('Production configuration has not been changed.')).toBeVisible()
    await expect(page.getByText('nginx-1.30.3-e2e')).toBeVisible()

    const publishTrigger = page.getByRole('button', { name: 'Publish…' })
    await publishTrigger.focus()
    await publishTrigger.click()
    const modal = page.getByRole('dialog', { name: 'Publish configuration to production?' })
    const cancel = modal.getByRole('button', { name: 'Cancel' })
    const confirmation = modal.getByRole('textbox')
    const confirm = modal.getByRole('button', { name: 'Publish', exact: true })
    await expect(modal).toBeVisible()
    await expect(cancel).toBeFocused()
    expect(await page.locator('[inert]').count()).toBeGreaterThan(0)
    await assertMinimumTargets(modal)

    await page.keyboard.press('Shift+Tab')
    await expect(confirmation).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(cancel).toBeFocused()
    await confirmation.fill('E2E workspace typo')
    await expect(confirm).toBeDisabled()
    await page.keyboard.press('Escape')
    await expect(modal).toBeHidden()
    await expect(publishTrigger).toBeFocused()

    await publishTrigger.click()
    await modal.getByRole('textbox').fill('E2E workspace')
    await expect(confirm).toBeEnabled()
    await confirm.click()
    await expect(page).toHaveURL(new RegExp(`[?&]release=${release.releaseId}(?:&|$)`))
    const timeline = page.getByRole('region', { name: 'Release progress' })
    await expect(timeline).toBeVisible()
    await expect(timeline.getByText('Current stage: Queued')).toBeVisible()
    await expect(timeline.getByText('Published successfully', { exact: true })).toBeVisible()
    await expect(timeline.getByText('Nginx is running and the fixed health check passed.')).toBeVisible()
    await expect(timeline.getByText(release.backupId)).toBeVisible()
    await assertNoPageOverflow(page)
    await assertNoSeriousOrCriticalAxeViolations(page)

    workspace.assertContract()
    release.assertContract()
  })
}

test('needs-attention truth survives a 320px refresh without opening an event stream', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 })
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const release = await installReleaseAPIFixture(page, workspace, {
    initialOutcome: 'needs_attention',
  })
  const url = `/config/workspaces/${workspace.workspaceId}?release=${release.releaseId}`

  await page.goto(url)
  const alert = page.getByRole('alert').filter({ hasText: 'Administrator attention required' })
  await expect(alert).toContainText('Production or runtime state cannot be confirmed.')
  await expect(page.getByText('Persisted progress')).toBeVisible()
  await expect(page.getByText('Workspace consistency must be resolved before publishing.')).toBeVisible()
  await assertNoPageOverflow(page)

  await page.reload()
  await expect(alert).toContainText('Ordinary changes remain blocked.')
  await expect(page.getByText('Persisted progress')).toBeVisible()
  expect(release.requests().filter(({ path }) => path.endsWith('/events'))).toHaveLength(0)
  expect(release.requests().filter(({ path }) => path === `/api/v1/config/releases/${release.releaseId}`).length).toBeGreaterThanOrEqual(2)
  await assertNoPageOverflow(page)
  await assertNoSeriousOrCriticalAxeViolations(page)
  workspace.assertContract()
  release.assertContract()
})

async function openPublishableWorkspace(
  page: Page,
  workspace: WorkspaceAPIFixture,
  width: (typeof releaseViewports)[number],
): Promise<void> {
  await page.goto(`/config/workspaces/${workspace.workspaceId}`)
  await expect(page.getByRole('heading', { level: 1, name: 'Configuration workspaces' })).toBeVisible()
  if (width <= 640) {
    await page.getByRole('button', { name: 'Show review task' }).click()
    await page.getByRole('button', { name: 'Review all file diffs' }).click()
    await expect(page.locator('.workspace-review-panel').getByRole('region', { name: 'Unified configuration diff' })).toBeVisible()
    return
  }
  const trigger = page.getByRole('button', { name: 'Open workspace review' })
  await trigger.click()
  const drawer = page.getByRole('dialog', { name: 'Workspace review' })
  await drawer.getByRole('button', { name: 'Review all file diffs' }).click()
  await expect(drawer.getByRole('region', { name: 'Unified configuration diff' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(drawer).toBeHidden()
  await expect(trigger).toBeFocused()
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

async function assertMinimumTargets(container: Locator): Promise<void> {
  const undersized = await container.locator('button, input').evaluateAll((elements) =>
    elements.flatMap((element) => {
      const rectangle = element.getBoundingClientRect()
      return rectangle.width >= 43.5 && rectangle.height >= 43.5
        ? []
        : [{ element: element.outerHTML.slice(0, 120), width: rectangle.width, height: rectangle.height }]
    }),
  )
  expect(undersized, 'release modal targets smaller than 44×44 CSS pixels').toEqual([])
}

async function assertNoSeriousOrCriticalAxeViolations(page: Page): Promise<void> {
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'])
    .analyze()
  const severe = results.violations
    .filter(({ impact }) => impact === 'serious' || impact === 'critical')
    .map(({ id, impact, nodes }) => ({
      id,
      impact,
      targets: nodes.map(({ target }) => target),
    }))
  expect(severe, 'serious or critical axe violations').toEqual([])
}

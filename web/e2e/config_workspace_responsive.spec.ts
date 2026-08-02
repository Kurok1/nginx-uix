/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

import {
  installWorkspaceAPIFixture,
  setAuthenticatedCookie,
  type WorkspaceAPIFixture,
} from './support/api'

interface WorkspaceViewport {
  width: 1069 | 1068 | 833 | 734 | 640 | 480 | 320
  layout: 'desktop' | 'mobile-task' | 'selector' | 'tree-editor'
}

const workspaceViewports: readonly WorkspaceViewport[] = [
  { width: 1069, layout: 'desktop' },
  { width: 1068, layout: 'tree-editor' },
  { width: 833, layout: 'tree-editor' },
  { width: 734, layout: 'selector' },
  { width: 640, layout: 'mobile-task' },
  { width: 480, layout: 'mobile-task' },
  { width: 320, layout: 'mobile-task' },
]

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

for (const viewport of workspaceViewports) {
  test(`configuration workspace follows A1 layout at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: 900 })
    const fixture = await installWorkspaceAPIFixture(page, { seedWorkspace: true })

    await openWorkspace(page, fixture)
    await assertWorkspaceLayout(page, viewport.layout)
    await assertNoPageOverflow(page)
    await assertMinimumVisibleTargets(page)
    await assertNoSeriousOrCriticalAxeViolations(page)
    fixture.assertContract()
  })
}

test('desktop keeps editor and diff overflow inside their labelled panes', async ({ page }) => {
  await page.setViewportSize({ width: 1069, height: 900 })
  const fixture = await installWorkspaceAPIFixture(page, { seedWorkspace: true })

  await openWorkspace(page, fixture)
  await page.getByRole('treeitem', { name: /nginx\.conf/ }).click()
  const editor = page.getByLabel('nginx.conf editor')
  await expect(editor).toBeVisible()
  const editorScroller = page.locator('.workspace-editor-panel .cm-scroller')
  await expectHorizontalOverflow(editorScroller, 'editor')

  await page.getByRole('button', { name: 'Review all file diffs' }).click()
  const diffScroller = page.locator('.workspace-review-panel .config-review__patch')
  await expect(diffScroller).toBeVisible()
  await expectHorizontalOverflow(diffScroller, 'diff')
  await diffScroller.focus()
  await expect(diffScroller).toBeFocused()

  await assertNoPageOverflow(page)
  fixture.assertContract()
})

for (const width of [640, 320] as const) {
  test(`mobile task switches preserve mounted editor text and undo at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    const fixture = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
    const marker = `mounted-undo-${width}`

    await openWorkspace(page, fixture)
    await page.getByRole('treeitem', { name: /nginx\.conf/ }).click()
    const editor = page.getByLabel('nginx.conf editor')
    const mountedEditor = page.locator('.cm-content[aria-label="nginx.conf editor"]')
    await expect(editor).toBeVisible()
    await editor.press('Control+End')
    await editor.pressSequentially(marker)
    await expect(editor).toContainText(marker)

    await page.getByRole('button', { name: 'Show files task' }).click()
    await expect(page.locator('.workspace-editor-panel')).toHaveAttribute('aria-hidden', 'true')
    await expect(mountedEditor).toHaveCount(1)
    await page.getByRole('button', { name: 'Show editor task' }).click()
    await expect(page.locator('.workspace-editor-panel')).toHaveAttribute('aria-hidden', 'false')
    await expect(editor).toContainText(marker)

    await editor.press(process.platform === 'darwin' ? 'Meta+z' : 'Control+z')
    await expect(editor).not.toContainText(marker)
    await page.getByRole('button', { name: 'Show review task' }).click()
    await expect(page.locator('.workspace-review-panel')).toHaveAttribute('aria-hidden', 'false')
    await expect(mountedEditor).toHaveCount(1)

    await assertNoPageOverflow(page)
    fixture.assertContract()
  })
}

test('workspace tree implements Home End and arrow-key navigation', async ({ page }) => {
  await page.setViewportSize({ width: 1069, height: 900 })
  const fixture = await installWorkspaceAPIFixture(page, { seedWorkspace: true })

  await openWorkspace(page, fixture)
  const tree = page.getByRole('tree', { name: 'Physical configuration files' })
  const rows = tree.getByRole('treeitem')
  const first = rows.first()
  const last = rows.last()
  await first.focus()

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
  await page.keyboard.press('ArrowLeft')
  await expect(first).toHaveAttribute('aria-expanded', 'false')

  fixture.assertContract()
})

test('review drawer traps focus, closes on Escape and restores its trigger', async ({ page }) => {
  await page.setViewportSize({ width: 1068, height: 900 })
  const fixture = await installWorkspaceAPIFixture(page, { seedWorkspace: true })

  await openWorkspace(page, fixture)
  const trigger = page.getByRole('button', { name: 'Open workspace review' })
  await trigger.focus()
  await trigger.click()
  const drawer = page.getByRole('dialog', { name: 'Workspace review' })
  const close = drawer.getByRole('button', { name: 'Close review' })
  const last = drawer.getByRole('button', { name: 'Search workspace files' })
  await expect(drawer).toBeVisible()
  await expect(close).toBeFocused()
  expect(await page.locator('[inert]').count()).toBeGreaterThan(0)

  await page.keyboard.press('Shift+Tab')
  await expect(last).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(close).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(drawer).toBeHidden()
  await expect(trigger).toBeFocused()

  fixture.assertContract()
})

test('named deletion modal traps focus, closes on Escape and restores its trigger', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1069, height: 900 })
  const fixture = await installWorkspaceAPIFixture(page, { seedWorkspace: true })

  await openWorkspace(page, fixture)
  const trigger = page.locator('.workspace-list button[aria-label^="Delete workspace "]')
  await trigger.focus()
  await trigger.click()
  const modal = page.getByRole('dialog', { name: /Delete workspace/ })
  const cancel = modal.getByRole('button', { name: 'Cancel' })
  const confirmation = modal.getByRole('textbox')
  await expect(modal).toBeVisible()
  await expect(cancel).toBeFocused()
  expect(await page.locator('[inert]').count()).toBeGreaterThan(0)

  await page.keyboard.press('Shift+Tab')
  await expect(confirmation).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(cancel).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(modal).toBeHidden()
  await expect(trigger).toBeFocused()

  fixture.assertContract()
})

async function openWorkspace(page: Page, fixture: WorkspaceAPIFixture): Promise<void> {
  await page.goto(`/config/workspaces/${fixture.workspaceId}?lang=en-US`)
  await expect(
    page.getByRole('heading', { level: 1, name: 'Configuration workspaces' }),
  ).toBeVisible()
  await expect(page.locator('.workspace-layout')).toBeVisible()
  await expect(page.getByText('Nginx validation has not run', { exact: true })).toBeVisible()
}

async function assertWorkspaceLayout(
  page: Page,
  layout: WorkspaceViewport['layout'],
): Promise<void> {
  const tree = page.locator('.workspace-tree-panel')
  const editor = page.locator('.workspace-editor-panel')
  const review = page.locator('.workspace-review-panel')
  const selector = page.locator('.workspace-file-selector')
  const taskTabs = page.getByRole('navigation', { name: 'Workspace tasks' })
  const reviewTrigger = page.getByRole('button', { name: 'Open workspace review' })

  if (layout === 'desktop') {
    await expect(tree).toBeVisible()
    await expect(editor).toBeVisible()
    await expect(review).toBeVisible()
    await expect(selector).toBeHidden()
    await expect(taskTabs).toBeHidden()
    await expect(reviewTrigger).toBeHidden()
    const columns = await page.locator('.workspace-layout').evaluate((element) =>
      getComputedStyle(element).gridTemplateColumns.trim().split(/\s+/),
    )
    expect(columns).toHaveLength(3)
    expect(Number.parseFloat(columns[0] ?? '')).toBeCloseTo(240, 0)
    expect(Number.parseFloat(columns[2] ?? '')).toBeCloseTo(360, 0)
    return
  }
  if (layout === 'tree-editor') {
    await expect(tree).toBeVisible()
    await expect(editor).toBeVisible()
    await expect(review).toBeHidden()
    await expect(selector).toBeHidden()
    await expect(taskTabs).toBeHidden()
    await expect(reviewTrigger).toBeVisible()
    const columns = await page.locator('.workspace-layout').evaluate((element) =>
      getComputedStyle(element).gridTemplateColumns.trim().split(/\s+/),
    )
    expect(columns).toHaveLength(2)
    expect(Number.parseFloat(columns[0] ?? '')).toBeCloseTo(
      page.viewportSize()?.width === 833 ? 208 : 240,
      0,
    )
    return
  }

  if (layout === 'selector') {
    await expect(tree).toBeHidden()
    await expect(editor).toBeVisible()
    await expect(review).toBeHidden()
    await expect(selector).toBeVisible()
    await expect(taskTabs).toBeHidden()
    await expect(reviewTrigger).toBeVisible()
    const display = await page
      .locator('.workspace-layout')
      .evaluate((element) => getComputedStyle(element).display)
    expect(display).toBe('block')
    return
  }

  await expect(taskTabs).toBeVisible()
  await expect(reviewTrigger).toBeHidden()
  await expect(tree).toBeVisible()
  await expect(selector).toBeVisible()
  await expect(tree).toHaveAttribute('aria-hidden', 'false')
  await expect(editor).toHaveAttribute('aria-hidden', 'true')
  await expect(review).toHaveAttribute('aria-hidden', 'true')
  await expect(selector).toHaveAttribute('aria-hidden', 'false')
  const activeSize = await renderedSize(tree)
  const hiddenEditorSize = await renderedSize(editor)
  const hiddenReviewSize = await renderedSize(review)
  expect(activeSize.height).toBeGreaterThanOrEqual(44)
  expect(activeSize.width).toBeGreaterThanOrEqual(44)
  expect(hiddenEditorSize.height).toBeLessThanOrEqual(1)
  expect(hiddenEditorSize.width).toBeLessThanOrEqual(1)
  expect(hiddenReviewSize.height).toBeLessThanOrEqual(1)
  expect(hiddenReviewSize.width).toBeLessThanOrEqual(1)
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
        return [
          {
            element: element.outerHTML.slice(0, 180),
            height: rect.height,
            width: rect.width,
          },
        ]
      }),
    )
  expect(undersized, 'visible interactive targets smaller than 44×44 CSS pixels').toEqual([])
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

async function expectHorizontalOverflow(locator: Locator, name: string): Promise<void> {
  const dimensions = await locator.evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
  }))
  expect(
    dimensions.scrollWidth,
    `${name} scroll dimensions: ${JSON.stringify(dimensions)}`,
  ).toBeGreaterThan(dimensions.clientWidth)
}

async function renderedSize(locator: Locator): Promise<{ height: number; width: number }> {
  return locator.evaluate((element) => {
    const rect = element.getBoundingClientRect()
    return { height: rect.height, width: rect.width }
  })
}

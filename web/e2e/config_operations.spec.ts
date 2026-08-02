/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

import { expect, test, type Page } from '@playwright/test'

import {
  assertOnlyLocalePreferenceStorage,
  assertNoAxeViolations,
  setAuthenticatedCookie,
} from './support/api'
import {
  installOperationsAPIFixture,
  operationsBackupID,
  operationsRestartID,
  operationsRestoreID,
  operationsRetentionID,
} from './support/operations'

const viewports = [1068, 833, 734, 640, 480, 320] as const

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

for (const width of viewports) {
  test(`recovery evidence reflows without clipping at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    const api = await installOperationsAPIFixture(page)

    await page.goto('/config/operations')
    await expect(page.getByRole('heading', { level: 1, name: 'Recovery & History' })).toBeVisible()
    await expect(page.locator('[data-attention-case]')).toContainText('Needs attention')
    await expect(page.locator('[data-runtime-control]')).toContainText('Nginx running')
    await expect(page.getByLabel('Backup evidence summary')).toContainText('1')

    const overviewTab = page.getByRole('tab', { name: 'Overview' })
    const backupsTab = page.getByRole('tab', { name: 'Backups' })
    await overviewTab.focus()
    await overviewTab.press('ArrowRight')
    await expect(backupsTab).toBeFocused()
    await expect(backupsTab).toHaveAttribute('aria-selected', 'true')
    await expect(page).toHaveURL((url) =>
      url.pathname === '/config/operations' &&
      url.searchParams.get('lang') === 'en-US' &&
      url.searchParams.get('tab') === 'backups',
    )

    const backupTable = page.locator('[data-backup-table]')
    const backupCards = page.locator('[data-backup-cards]')
    if (width > 734) {
      await expect(backupTable).toBeVisible()
      await expect(backupCards).toBeHidden()
    } else {
      await expect(backupTable).toBeHidden()
      await expect(backupCards).toBeVisible()
    }

    await assertNoPageOverflow(page)
    await assertMinimumTargets(page)
    await assertHeadingOrder(page)
    await assertOnlyLocalePreferenceStorage(page)
    await assertNoAxeViolations(page)
    api.assertContract()
  })
}

test('fixed restart requires exact confirmation, restores focus, and rebuilds terminal evidence', async ({
  page,
}) => {
  const api = await installOperationsAPIFixture(page)
  await page.goto('/config/operations')
  await expect(page.locator('[data-runtime-control]')).toContainText('Nginx running')

  const restartTrigger = page.locator('[data-runtime-control] [data-action="restart-nginx"]')
  await restartTrigger.click()
  let dialog = page.getByRole('dialog', { name: 'Restart Nginx?' })
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
  await dialog.getByLabel('Reason').fill('replace unhealthy master')
  await dialog.getByLabel('Type RESTART NGINX exactly to confirm').fill('restart nginx')
  await expect(dialog.getByRole('button', { name: 'Restart Nginx' })).toBeDisabled()
  await dialog.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(restartTrigger).toBeFocused()

  await restartTrigger.click()
  dialog = page.getByRole('dialog', { name: 'Restart Nginx?' })
  await dialog.getByLabel('Reason').fill('replace unhealthy master')
  await dialog.getByLabel('Type RESTART NGINX exactly to confirm').fill('RESTART NGINX')
  await dialog.getByRole('button', { name: 'Restart Nginx' }).click()

  const restartTimeline = page.getByLabel('Restart progress')
  await expect(restartTimeline.getByRole('heading', { level: 3, name: 'Restart progress' })).toBeVisible()
  await expect(restartTimeline.getByText('Operation succeeded', { exact: true })).toBeVisible()
  const createCalls = api.callsFor('POST', '/api/v1/nginx/restarts')
  expect(createCalls).toHaveLength(1)
  expect(JSON.parse(createCalls[0]?.body ?? '')).toEqual({
    attention_case_id: '',
    reason: 'replace unhealthy master',
    confirmation: 'RESTART NGINX',
  })
  expect(api.callsFor('GET', `/api/v1/nginx/restarts/${operationsRestartID}/events`)).toHaveLength(1)
  expect(api.callsFor('GET', `/api/v1/nginx/restarts/${operationsRestartID}`).length).toBeGreaterThan(0)
  await assertNoAxeViolations(page)
  api.assertContract()
})

test('verified restore, retention dry-run, and attention verification keep exact evidence links', async ({
  page,
}) => {
  const api = await installOperationsAPIFixture(page)
  await page.goto('/config/operations')
  await expect(page.locator('[data-attention-case]')).toBeVisible()

  await page.locator('[data-action="verify-attention"]').click()
  await expect(page.locator('[data-attention-case]')).toHaveCount(0)
  await expect(page.getByRole('heading', { level: 3, name: 'Latest fixed verification' })).toBeVisible()

  await page.getByRole('tab', { name: 'Backups' }).click()
  await page.locator('[data-backup-table] [data-action="restore"]').click()
  let dialog = page.getByRole('dialog', { name: `Restore backup “${operationsBackupID}”?` })
  await dialog.getByLabel('Reason').fill('restore verified recovery point')
  await dialog.getByLabel(`Type ${operationsBackupID} exactly to confirm`).fill(operationsBackupID)
  await dialog.getByRole('button', { name: 'Start verified restore' }).click()

  const restoreTimeline = page.getByLabel('Restore progress')
  await expect(restoreTimeline.getByRole('heading', { level: 3, name: 'Restore progress' })).toBeVisible()
  await expect(restoreTimeline.getByText('Operation succeeded', { exact: true })).toBeVisible()
  const restoreCalls = api.callsFor(
    'POST',
    `/api/v1/config/backups/${operationsBackupID}/restores`,
  )
  expect(restoreCalls).toHaveLength(1)
  expect(JSON.parse(restoreCalls[0]?.body ?? '')).toEqual({
    attention_case_id: '',
    reason: 'restore verified recovery point',
    confirm_backup_id: operationsBackupID,
  })
  expect(api.callsFor('GET', `/api/v1/config/restores/${operationsRestoreID}/events`)).toHaveLength(1)

  await page.getByRole('tab', { name: 'Backups' }).click()
  const planButton = page.locator('[data-action="plan-retention"]')
  await expect(planButton).toBeEnabled()
  await planButton.click()
  await expect(page.getByText(operationsRetentionID, { exact: true })).toBeVisible()
  await page.locator('[data-action="execute-retention"]').click()
  dialog = page.getByRole('dialog', {
    name: `Execute retention plan “${operationsRetentionID}”?`,
  })
  const executeButton = dialog.getByRole('button', { name: 'Execute retention plan' })
  await expect(executeButton).toBeDisabled()
  await dialog.getByLabel(`Type ${operationsRetentionID} exactly to confirm`).fill(
    operationsRetentionID,
  )
  await executeButton.click()

  const executeCalls = api.callsFor(
    'POST',
    `/api/v1/config/backup-retention-runs/${operationsRetentionID}/executions`,
  )
  expect(executeCalls).toHaveLength(1)
  expect(JSON.parse(executeCalls[0]?.body ?? '')).toEqual({ confirmation: operationsRetentionID })
  await assertNoAxeViolations(page)
  api.assertContract()
})

async function assertNoPageOverflow(page: Page): Promise<void> {
  const dimensions = await page.evaluate(() => ({
    documentClientWidth: document.documentElement.clientWidth,
    documentScrollWidth: document.documentElement.scrollWidth,
    bodyClientWidth: document.body.clientWidth,
    bodyScrollWidth: document.body.scrollWidth,
  }))
  expect(dimensions.documentScrollWidth).toBeLessThanOrEqual(dimensions.documentClientWidth)
  expect(dimensions.bodyScrollWidth).toBeLessThanOrEqual(dimensions.bodyClientWidth)
}

async function assertMinimumTargets(page: Page): Promise<void> {
  const undersized = await page
    .locator('a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])')
    .evaluateAll((elements) => elements.flatMap((element) => {
      const rect = element.getBoundingClientRect()
      if (rect.width === 0 || rect.height === 0 || element.getClientRects().length === 0) return []
      if (rect.width >= 43.5 && rect.height >= 43.5) return []
      return [{ element: element.outerHTML.slice(0, 160), width: rect.width, height: rect.height }]
    }))
  expect(undersized, 'visible interactive targets smaller than 44×44 CSS pixels').toEqual([])
}

async function assertHeadingOrder(page: Page): Promise<void> {
  const levels = await page.locator('h1, h2, h3, h4, h5, h6').evaluateAll((headings) =>
    headings.flatMap((heading) =>
      heading.getClientRects().length === 0 ? [] : [Number(heading.tagName.slice(1))],
    ),
  )
  expect(levels[0]).toBe(1)
  expect(levels.filter((level) => level === 1)).toHaveLength(1)
  for (let index = 1; index < levels.length; index += 1) {
    expect(levels[index]).toBeLessThanOrEqual((levels[index - 1] ?? 0) + 1)
  }
}

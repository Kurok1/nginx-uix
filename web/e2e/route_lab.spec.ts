/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */
import { expect, test, type Page } from '@playwright/test'

import {
  assertOnlyLocalePreferenceStorage,
  assertNoAxeViolations,
  installWorkspaceAPIFixture,
  setAuthenticatedCookie,
} from './support/api'
import {
  installRouteLabAPIFixture,
  routeSideEffectConfirmation,
} from './support/route_lab'

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

const routeLocaleCases = [
  {
    locale: 'zh-CN',
    title: '路由实验室',
    uri: 'URI 路径',
    run: '运行隔离测试',
    completed: '运行完成',
  },
  {
    locale: 'en-US',
    title: 'Route Lab',
    uri: 'URI path',
    run: 'Run isolated test',
    completed: 'Runtime completed',
  },
] as const

for (const copy of routeLocaleCases) {
  test(`${copy.locale} safe GET reaches isolated runtime evidence without confirmation`, async ({ page }) => {
    const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
    const routeLab = await installRouteLabAPIFixture(page, workspace)

    await page.goto(`/config/route-lab?lang=${copy.locale}`)
    await expect(page.getByRole('heading', { level: 1, name: copy.title })).toBeVisible()
    await page.getByRole('textbox', { name: /^Host/u }).fill('example.test')
    await page.getByLabel(copy.uri).fill('/api/users')
    await page.getByRole('button', { name: copy.run }).click()

    await expect(page.getByText(copy.completed, { exact: true })).toBeVisible()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    expect(routeLab.callsFor(
      'POST',
      `/api/v1/config/workspaces/${workspace.workspaceId}/route-tests`,
    )).toHaveLength(1)
    routeLab.assertContract()
    workspace.assertContract()
  })
}

test('switching a side-effecting request to zh-CN preserves its exact request semantics', async ({ page }) => {
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const routeLab = await installRouteLabAPIFixture(page, workspace)

  await page.goto('/config/route-lab?lang=en-US')
  await page.getByRole('textbox', { name: /^Host/u }).fill('example.test')
  await page.getByLabel('URI path').fill('/api/users')
  await page.getByLabel('Method').selectOption('POST')
  await page.getByLabel('Body').fill('{"private":"value"}')
  await page.getByRole('combobox', { name: 'Language' }).selectOption('zh-CN')

  await expect(page.getByRole('textbox', { name: /^Host/u })).toHaveValue('example.test')
  await expect(page.getByLabel('URI 路径')).toHaveValue('/api/users')
  await expect(page.getByLabel('方法')).toHaveValue('POST')
  await expect(page.getByLabel('Body')).toHaveValue('{"private":"value"}')
  await page.getByRole('button', { name: '运行隔离测试' }).click()
  const dialog = page.getByRole('dialog', { name: '运行可能产生副作用的请求？' })
  await dialog
    .getByLabel(`准确输入 ${routeSideEffectConfirmation} 以确认`)
    .fill(routeSideEffectConfirmation)
  await dialog.getByRole('button', { name: '运行隔离测试' }).click()

  await expect(page.getByText('运行完成', { exact: true })).toBeVisible()
  const requests = routeLab.callsFor(
    'POST',
    `/api/v1/config/workspaces/${workspace.workspaceId}/route-tests`,
  )
  expect(requests).toHaveLength(1)
  expect(requests[0]?.body).toMatchObject({
    method: 'POST',
    uri: '/api/users',
    body: '{"private":"value"}',
    confirmation: routeSideEffectConfirmation,
  })
  routeLab.assertContract()
  workspace.assertContract()
})

test('static prediction, named runtime confirmation, mismatch evidence and safe history copy stay distinct', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 1000 })
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const routeLab = await installRouteLabAPIFixture(page, workspace)

  await page.goto('/config/route-lab')
  await expect(page.getByRole('heading', { level: 1, name: 'Route Lab' })).toBeVisible()
  await page.getByRole('textbox', { name: /^Host HTTP/ }).fill('example.test')
  await page.getByLabel(/URI path/).fill('/api/users')
  await page.getByLabel('Expected status (optional)').fill('200')
  await page.getByRole('button', { name: 'Analyze route' }).click()

  const staticRegion = page.getByRole('region', { name: 'Candidate explanation' })
  await expect(staticRegion.getByText('Static analysis — prediction only')).toBeVisible()
  await expect(staticRegion.getByText('Exact server name match')).toBeVisible()
  await expect(staticRegion.getByText('Longest matching prefix')).toBeVisible()

  await page.getByLabel('Method').selectOption('POST')
  await page.getByLabel('Body').fill('{"private":"value"}')
  await page.getByRole('button', { name: 'Add header' }).click()
  await page.getByLabel('Header 1 name').fill('Authorization')
  await page.getByLabel('Header 1 value').fill('Bearer browser-only-secret')

  const runTrigger = page.getByRole('button', { name: 'Run isolated test' })
  await runTrigger.click()
  let dialog = page.getByRole('dialog', { name: 'Run a potentially side-effecting request?' })
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
  await expect(dialog).toContainText('may still reach a configured upstream')
  const confirmation = dialog.getByLabel(`Type ${routeSideEffectConfirmation} exactly to confirm`)
  await confirmation.fill('run side-effecting request')
  await expect(dialog.getByRole('button', { name: 'Run isolated test' })).toBeDisabled()
  await dialog.press('Escape')
  await expect(runTrigger).toBeFocused()

  await runTrigger.click()
  dialog = page.getByRole('dialog', { name: 'Run a potentially side-effecting request?' })
  await dialog.getByLabel(`Type ${routeSideEffectConfirmation} exactly to confirm`).fill(
    routeSideEffectConfirmation,
  )
  await dialog.getByRole('button', { name: 'Run isolated test' }).click()

  const runtimeRegion = page.getByRole('region', { name: 'Runtime evidence' })
  await expect(runtimeRegion.getByText('Isolated runtime result — production Nginx was not reloaded')).toBeVisible()
  await expect(runtimeRegion.getByText('Runtime completed')).toBeVisible()
  await expect(runtimeRegion.getByText('Predicted', { exact: true })).toBeVisible()
  await expect(runtimeRegion.getByText('Observed', { exact: true })).toBeVisible()
  await expect(runtimeRegion.getByText(/Static prediction and runtime observation differ/)).toBeVisible()
  await expect(runtimeRegion.getByRole('heading', { name: 'Sandbox cleanup confirmed' })).toBeVisible()

  await page.locator('[data-action="use-route-parameters"]').click()
  await expect(page.getByText(/Body and sensitive headers were not copied/)).toBeVisible()
  await expect(page.getByRole('textbox', { name: /^Body Never/ })).toHaveValue('')
  await expect(page.getByLabel('Header 1 name')).toHaveCount(0)

  expect(routeLab.callsFor('POST', `/api/v1/config/workspaces/${workspace.workspaceId}/route-analyses`)).toHaveLength(1)
  expect(routeLab.callsFor('POST', `/api/v1/config/workspaces/${workspace.workspaceId}/route-tests`)).toHaveLength(1)
  await assertOnlyLocalePreferenceStorage(page)
  await assertNoAxeViolations(page)
  routeLab.assertContract()
  workspace.assertContract()
})

test('an EventSource reconnect resumes persisted stages and never implies cancellation', async ({ page }) => {
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const routeLab = await installRouteLabAPIFixture(page, workspace, {
    reconnectBeforeTerminal: true,
  })

  await page.goto('/config/route-lab')
  await page.getByRole('textbox', { name: /^Host HTTP/ }).fill('example.test')
  await page.getByRole('button', { name: 'Run isolated test' }).click()

  await expect(page.getByText('Runtime completed')).toBeVisible()
  await expect.poll(() => routeLab.callsFor('GET', `/api/v1/route-tests/${routeLab.runId}/events`).length).toBeGreaterThanOrEqual(2)
  expect(routeLab.callsFor('POST', `/api/v1/route-tests/${routeLab.runId}/cancellations`)).toHaveLength(0)
  routeLab.assertContract()
  workspace.assertContract()
})

test('explicit cancellation remains nonterminal until persisted sandbox cleanup completes', async ({ page }) => {
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const routeLab = await installRouteLabAPIFixture(page, workspace, {
    runningUntilCancelled: true,
  })

  await page.goto('/config/route-lab')
  await page.getByRole('textbox', { name: /^Host HTTP/ }).fill('example.test')
  await page.getByRole('button', { name: 'Run isolated test' }).click()
  const cancel = page.getByRole('button', { name: 'Cancel test' })
  await expect(cancel).toBeVisible()
  await cancel.click()

  await expect(page.getByText('Cancellation requested')).toBeVisible()
  await expect(page.getByText('Route test cancelled', { exact: true })).toBeVisible()
  expect(routeLab.callsFor('POST', `/api/v1/route-tests/${routeLab.runId}/cancellations`)).toHaveLength(1)
  routeLab.assertContract()
  workspace.assertContract()
})

for (const width of [1068, 833, 640, 320]) {
  test(`Route Lab reflows without page overflow at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 })
    const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
    const routeLab = await installRouteLabAPIFixture(page, workspace)

    await page.goto('/config/route-lab')
    await expect(page.getByRole('heading', { level: 1, name: 'Route Lab' })).toBeVisible()
    if (width <= 640) {
      const taskNavigation = page.getByRole('navigation', { name: 'Route Lab tasks' })
      await expect(taskNavigation).toBeVisible()
      await expect(taskNavigation.getByRole('button', { name: 'Request' })).toHaveAttribute('aria-pressed', 'true')
      const historyTask = taskNavigation.getByRole('button', { name: 'History' })
      await historyTask.focus()
      await historyTask.press('Space')
      await expect(historyTask).toBeFocused()
      await expect(page.getByRole('heading', { level: 2, name: 'Route-test history' })).toBeVisible()
    } else {
      await expect(page.getByRole('navigation', { name: 'Route Lab tasks' })).toBeHidden()
      await expect(page.getByRole('heading', { level: 2, name: 'Candidate explanation' })).toBeVisible()
    }

    await assertNoPageOverflow(page)
    await assertMinimumTargets(page)
    await assertHeadingOrder(page)
    await assertOnlyLocalePreferenceStorage(page)
    await assertNoAxeViolations(page)
    routeLab.assertContract()
    workspace.assertContract()
  })
}

test('Route Lab reflows at 200 percent browser-style content zoom without persistence', async ({ page }) => {
  await page.setViewportSize({ width: 640, height: 1000 })
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const routeLab = await installRouteLabAPIFixture(page, workspace)

  await page.goto('/config/route-lab')
  await page.evaluate(() => {
    document.documentElement.style.zoom = '2'
  })
  await expect(page.getByRole('heading', { level: 1, name: 'Route Lab' })).toBeVisible()
  await assertNoPageOverflow(page)
  await assertOnlyLocalePreferenceStorage(page)
  routeLab.assertContract()
  workspace.assertContract()
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

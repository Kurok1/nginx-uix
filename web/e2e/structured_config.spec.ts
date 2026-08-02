/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'

import {
  appOrigin,
  installWorkspaceAPIFixture,
  setAuthenticatedCookie,
} from './support/api'
import {
  installStructuredAPIFixture,
  structuredPreviewID,
  structuredUpstreamID,
} from './support/structured'

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

const structuredLocaleCases = [
  {
    locale: 'zh-CN',
    upstreams: '上游',
    draftOnly: '仅修改草稿——尚未执行完整 Nginx 校验',
    upstreamName: '上游名称',
    reviewRename: '审查上游重命名',
    diff: '生成的统一差异',
    confirm: '输入与 application 完全相同的内容以确认',
    apply: '应用到草稿',
    updated: '草稿已更新：已变更 1 个文件。',
    servers: 'Server 与 Location',
    matcherType: '匹配类型',
  },
  {
    locale: 'en-US',
    upstreams: 'Upstreams',
    draftOnly: 'Draft only — full Nginx validation has not run',
    upstreamName: 'Upstream name',
    reviewRename: 'Review upstream rename',
    diff: 'Generated unified diff',
    confirm: 'Type application exactly to confirm',
    apply: 'Apply to draft',
    updated: 'Draft updated: 1 file changed.',
    servers: 'Servers & Locations',
    matcherType: 'Matcher type',
  },
] as const

for (const copy of structuredLocaleCases) {
  test(`${copy.locale} structured upstream preview and apply preserve configuration identifiers`, async ({ page }) => {
    const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
    const structured = await installStructuredAPIFixture(page, workspace)

    await page.goto(`/config/workspaces/${workspace.workspaceId}/upstreams?lang=${copy.locale}`)
    await expect(page.getByRole('heading', { level: 1, name: copy.upstreams })).toBeVisible()
    await expect(page.getByText(copy.draftOnly, { exact: true })).toBeVisible()
    await page.getByLabel(copy.upstreamName).fill('application')
    await page.getByRole('button', { name: copy.reviewRename }).click()
    const review = page.locator('.structured-workbench__review')
    await expect(review.getByRole('region', { name: copy.diff })).toContainText('upstream backend')
    await review.getByLabel(copy.confirm).fill('application')
    await review.getByRole('button', { name: copy.apply }).click()
    await expect(page.getByText(copy.updated)).toBeVisible()
    await expect(page.getByLabel(copy.upstreamName)).toHaveValue('application')
    structured.assertContract()
    workspace.assertContract()
  })

  test(`${copy.locale} structured server and location projection remains inspectable`, async ({ page }) => {
    await page.setViewportSize({ width: 640, height: 900 })
    const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
    const structured = await installStructuredAPIFixture(page, workspace)

    await page.goto(`/config/workspaces/${workspace.workspaceId}/servers?lang=${copy.locale}`)
    await expect(page.getByRole('heading', { level: 1, name: copy.servers })).toBeVisible()
    await expect(page.locator('[data-structured-selector="server"]')).toBeVisible()
    await expect(page.locator('[data-structured-selector="location"]')).toBeVisible()
    await expect(page.getByLabel(copy.matcherType)).toHaveValue('exact')
    structured.assertContract()
    workspace.assertContract()
  })
}

test('upstream rename is previewed, explicitly confirmed and applied only to the draft', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const structured = await installStructuredAPIFixture(page, workspace)
  const initialETag = workspace.currentDraftETag()

  await page.goto('/config/workspaces/' + workspace.workspaceId + '/upstreams')
  await expect(page.getByRole('heading', { level: 1, name: 'Upstreams' })).toBeVisible()
  await expect(page.getByText('Draft only — full Nginx validation has not run')).toBeVisible()
  await expect(page.getByText('Preserved read-only directives:')).toContainText('zone')
  await expect(page.getByText('Preserved read-only parameters:')).toContainText('resolve')

  await page.getByLabel('Upstream name').fill('application')
  await page.getByRole('button', { name: 'Review upstream rename' }).click()

  const review = page.locator('.structured-workbench__review')
  await expect(review.getByText('conf.d/upstreams.conf', { exact: true })).toBeVisible()
  await expect(review.getByRole('region', { name: 'Generated unified diff' })).toContainText(
    'upstream backend',
  )
  await expect(review.getByRole('button', { name: 'Apply to draft' })).toBeDisabled()
  await review.getByLabel('Type application exactly to confirm').fill('application')
  await review.getByRole('button', { name: 'Apply to draft' }).click()

  await expect(page.getByText(/Draft updated: 1 file changed\./)).toBeVisible()
  await expect(page.getByLabel('Upstream name')).toHaveValue('application')
  expect(workspace.currentDraftETag()).not.toBe(initialETag)

  const mutations = structured.requests().filter(({ method }) => method === 'POST')
  expect(mutations).toHaveLength(2)
  expect(mutations[0]).toMatchObject({
    path:
      '/api/v1/config/workspaces/' +
      workspace.workspaceId +
      '/structured-change-previews',
    body: {
      kind: 'upstream.rename',
      input: { upstream_id: structuredUpstreamID, new_name: 'application' },
    },
  })
  expect(mutations[0]?.headers.origin).toBe(appOrigin)
  expect(mutations[0]?.headers['if-match']).toBeUndefined()
  expect(mutations[1]).toMatchObject({
    path: '/api/v1/config/workspaces/' + workspace.workspaceId + '/structured-changes',
    body: {
      preview_id: structuredPreviewID,
      kind: 'upstream.rename',
      input: { upstream_id: structuredUpstreamID, new_name: 'application' },
    },
  })
  expect(mutations[1]?.headers['if-match']).toBe(initialETag)
  expect(structured.requests().some(({ path }) => path.includes('publish'))).toBe(false)
  structured.assertContract()
  workspace.assertContract()
})

test('compact server view exposes all location matchers, both selectors and accessible targets', async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 900 })
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const structured = await installStructuredAPIFixture(page, workspace)

  await page.goto('/config/workspaces/' + workspace.workspaceId + '/servers')
  await expect(
    page.getByRole('heading', { level: 1, name: 'Servers & Locations' }),
  ).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Structured workspace tasks' })).toBeVisible()
  await expect(page.locator('[data-structured-selector="server"]')).toBeVisible()
  const locationSelector = page.locator('[data-structured-selector="location"]')
  await expect(locationSelector).toBeVisible()

  for (const name of [
    'Exact (=) location /health',
    'Prefix location /api',
    'Priority prefix (^~) location /assets/',
    'Regular expression (~) location \\.php$',
    'Case-insensitive regex (~*) location \\.(gif|jpg)$',
    'Named (@) location @fallback',
  ]) {
    await expect(page.getByRole('treeitem', { name })).toBeVisible()
  }

  await locationSelector.locator('select').selectOption('8'.repeat(32))
  await expect(page.getByRole('button', { name: 'Edit' })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await expect(page.getByLabel('Matcher type')).toHaveValue('regex')
  await expect(page.getByRole('textbox', { name: 'Matcher', exact: true })).toHaveValue(
    '\\.php$',
  )

  await assertNoPageOverflow(page)
  await assertMinimumVisibleTargets(page)
  await assertNoSeriousOrCriticalAxeViolations(page)
  structured.assertContract()
  workspace.assertContract()
})

test('structured pages reflow without horizontal overflow at every documented breakpoint', async ({
  page,
}) => {
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const structured = await installStructuredAPIFixture(page, workspace)

  for (const width of [1068, 833, 734, 640, 480, 320]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/config/workspaces/' + workspace.workspaceId + '/upstreams')
    await expect(page.getByRole('heading', { level: 1, name: 'Upstreams' })).toBeVisible()
    await assertNoPageOverflow(page)

    if (width <= 734) {
      await expect(page.locator('.structured-resource-selector')).toBeVisible()
    } else {
      await expect(page.getByRole('listbox', { name: 'Upstreams' })).toBeVisible()
    }
    if (width <= 640) {
      await expect(
        page.getByRole('navigation', { name: 'Structured workspace tasks' }),
      ).toBeVisible()
    }
  }

  structured.assertContract()
  workspace.assertContract()
})

test('an incomplete projection blocks mutations and keeps the raw editor fallback available', async ({
  page,
}) => {
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const structured = await installStructuredAPIFixture(page, workspace, { incomplete: true })

  await page.goto('/config/workspaces/' + workspace.workspaceId + '/upstreams')
  await expect(page.getByText('Incomplete — raw editing only')).toBeVisible()
  await expect(
    page.getByText(/Structured edits are blocked because the include graph or syntax projection is incomplete/),
  ).toBeVisible()
  await expect(page.getByText('include_unresolved', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Create upstream' })).toBeDisabled()
  await expect(page.getByRole('link', { name: 'Open raw editor' })).toHaveAttribute(
    'href',
    '/config/workspaces/' + workspace.workspaceId,
  )
  expect(structured.requests().filter(({ method }) => method === 'POST')).toHaveLength(0)
  structured.assertContract()
  workspace.assertContract()
})

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

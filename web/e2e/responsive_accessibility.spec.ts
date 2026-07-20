/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { expect, test, type Locator, type Page } from '@playwright/test'

import {
  assertNoApplicationStorage,
  assertNoAxeViolations,
  authenticatedSession,
  healthyStatus,
  installAPIMocks,
  repeatedEffectiveConfig,
  setAuthenticatedCookie,
} from './support/api'

const viewports = [
  { width: 1440, dashboardColumns: 3, mobileNavigation: false },
  { width: 1068, dashboardColumns: 3, mobileNavigation: false },
  { width: 833, dashboardColumns: 2, mobileNavigation: true },
  { width: 734, dashboardColumns: 2, mobileNavigation: true },
  { width: 640, dashboardColumns: 1, mobileNavigation: true },
  { width: 480, dashboardColumns: 1, mobileNavigation: true },
] as const

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

for (const viewport of viewports) {
  test(`Dashboard reflows without overflow at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: 900 })
    const api = await installAPIMocks(page, {
      session: { status: 200, body: authenticatedSession },
      status: { status: 200, body: healthyStatus },
    })

    await page.goto('/')
    await expect(page.getByRole('heading', { level: 1, name: '运行状态' })).toBeVisible()

    const runtimeGrid = page.locator('.runtime-status__grid')
    await expect(runtimeGrid).toBeVisible()
    expect(await gridColumnCount(runtimeGrid)).toBe(viewport.dashboardColumns)
    await assertNavigationMode(page, viewport.mobileNavigation)
    await assertNoPageOverflow(page)
    await assertMinimumTargets(page)
    await assertHeadingOrder(page)
    await assertNoApplicationStorage(page)
    await assertNoAxeViolations(page)
    api.assertContract()
  })

  test(`effective configuration contains long lines at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: 900 })
    const api = await installAPIMocks(page, {
      session: { status: 200, body: authenticatedSession },
      effectiveConfig: { status: 200, body: repeatedEffectiveConfig },
    })

    await page.goto('/configuration')
    await expect(page.getByRole('heading', { level: 1, name: '生效配置' })).toBeVisible()

    const desktopNavigator = page.getByRole('navigation', { name: '生效配置加载顺序' })
    const selector = page.getByLabel('配置加载项')
    if (viewport.mobileNavigation) {
      await expect(desktopNavigator).toBeHidden()
      await expect(selector).toBeVisible()
      await selector.selectOption('occurrence-3')
      await expect(page.getByRole('region', { name: /配置内容/ }).last()).toContainText(
        'second.example.test',
      )
      await selector.selectOption('occurrence-1')
    } else {
      await expect(desktopNavigator).toBeVisible()
      await expect(selector).toBeHidden()
    }

    const scrollRegion = page.getByRole('region', {
      name: '配置内容 /etc/nginx/nginx.conf',
    })
    const internalOverflow = await scrollRegion.evaluate(
      (element) => element.scrollWidth > element.clientWidth,
    )
    expect(internalOverflow).toBe(true)
    await assertNoPageOverflow(page)
    await assertMinimumTargets(page)
    await assertHeadingOrder(page)
    await assertNoApplicationStorage(page)
    await assertNoAxeViolations(page)
    api.assertContract()
  })
}

test('desktop tab order exposes the skip link and visible focus in DOM order', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const api = await installAPIMocks(page, {
    session: { status: 200, body: authenticatedSession },
    status: { status: 200, body: healthyStatus },
  })
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1, name: '运行状态' })).toBeVisible()

  const skipLink = page.getByRole('link', { name: 'Skip to main content' })
  await page.keyboard.press('Tab')
  await expect(skipLink).toBeFocused()
  await assertVisibleFocus(skipLink)
  await skipLink.press('Enter')
  await expect(page.locator('#main-content')).toBeFocused()

  await page.reload()
  await expect(page.getByRole('heading', { level: 1, name: '运行状态' })).toBeVisible()
  const orderedControls = [
    skipLink,
    page.locator('.global-nav__brand'),
    page.locator('.global-nav__links a').nth(0),
    page.locator('.global-nav__links a').nth(1),
    page.locator('.global-nav__links a').nth(2),
    page.locator('.global-nav__links a').nth(3),
    page.getByRole('button', { name: '退出登录' }),
    page.locator('.sub-nav__links a').nth(0),
    page.locator('.sub-nav__links a').nth(1),
    page.locator('.sub-nav__links a').nth(2),
    page.locator('.sub-nav__links a').nth(3),
    page.getByRole('button', { name: '刷新状态' }),
  ]
  for (const control of orderedControls) {
    await page.keyboard.press('Tab')
    await expect(control).toBeFocused()
    await assertVisibleFocus(control)
  }
  api.assertContract()
})

test('mobile navigation opens with Enter and Space from fresh component states', async ({
  page,
}) => {
  await page.setViewportSize({ width: 833, height: 900 })
  const api = await installAPIMocks(page, {
    session: { status: 200, body: authenticatedSession },
    status: { status: 200, body: healthyStatus },
  })
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1, name: '运行状态' })).toBeVisible()

  let menuButton = page.getByRole('button', { name: 'Open navigation' })
  await menuButton.focus()
  await menuButton.press('Enter')
  await expect(page.getByRole('button', { name: 'Close navigation' })).toHaveAttribute(
    'aria-expanded',
    'true',
  )
  await expect(page.locator('#global-nav-menu')).toBeVisible()

  await page.reload()
  await expect(page.getByRole('heading', { level: 1, name: '运行状态' })).toBeVisible()
  menuButton = page.getByRole('button', { name: 'Open navigation' })
  await menuButton.focus()
  await menuButton.press('Space')
  await expect(page.getByRole('button', { name: 'Close navigation' })).toHaveAttribute(
    'aria-expanded',
    'true',
  )
  await expect(page.locator('#global-nav-menu')).toBeVisible()
  await assertMinimumTargets(page)
  await assertVisibleFocus(page.getByRole('button', { name: 'Close navigation' }))
  api.assertContract()
})

test('configuration selection and wrapping work with Enter and Space', async ({ page }) => {
  await page.setViewportSize({ width: 1068, height: 900 })
  const api = await installAPIMocks(page, {
    session: { status: 200, body: authenticatedSession },
    effectiveConfig: { status: 200, body: repeatedEffectiveConfig },
  })
  await page.goto('/configuration')
  await expect(page.getByRole('heading', { level: 1, name: '生效配置' })).toBeVisible()

  const thirdOccurrence = page.getByRole('button', {
    name: '第 3 项 /etc/nginx/conf.d/repeated.conf',
  })
  await thirdOccurrence.focus()
  await thirdOccurrence.press('Enter')
  await expect(thirdOccurrence).toHaveAttribute('aria-current', 'true')
  await assertVisibleFocus(thirdOccurrence)

  const secondOccurrence = page.getByRole('button', {
    name: '第 2 项 /etc/nginx/conf.d/repeated.conf',
  })
  await secondOccurrence.focus()
  await secondOccurrence.press('Space')
  await expect(secondOccurrence).toHaveAttribute('aria-current', 'true')
  await assertVisibleFocus(secondOccurrence)

  const wrapButton = page.getByRole('button', { name: '自动换行' })
  await wrapButton.focus()
  await wrapButton.press('Space')
  const activeWrapButton = page.getByRole('button', { name: '关闭自动换行' })
  await expect(activeWrapButton).toHaveAttribute('aria-pressed', 'true')
  await assertVisibleFocus(activeWrapButton)

  const codeRegion = page.getByRole('region', {
    name: '配置内容 /etc/nginx/conf.d/repeated.conf',
  })
  await codeRegion.focus()
  await expect(codeRegion).toBeFocused()
  await assertVisibleFocus(codeRegion)
  api.assertContract()
})

test('stable pages expose labelled local semantics rather than page-wide announcements', async ({
  page,
}) => {
  const api = await installAPIMocks(page, {
    session: { status: 200, body: authenticatedSession },
    effectiveConfig: { status: 200, body: repeatedEffectiveConfig },
  })
  await page.goto('/configuration')
  await expect(page.getByRole('heading', { level: 1, name: '生效配置' })).toBeVisible()

  await expect(page.locator('.app-shell[aria-live], main[aria-live]')).toHaveCount(0)
  await expect(page.locator('[aria-live="polite"]')).toHaveCount(1)
  await expect(page.getByRole('navigation', { name: 'Global navigation' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Section navigation' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: '生效配置加载顺序' })).toBeVisible()
  await expect(page.getByRole('region', { name: '配置内容 /etc/nginx/nginx.conf' })).toBeVisible()
  await expect(page.getByRole('button', { name: '刷新配置' })).toBeVisible()
  await expect(page.getByRole('button', { name: '自动换行' })).toHaveAttribute(
    'aria-pressed',
    'false',
  )
  await assertHeadingOrder(page)
  await assertNoAxeViolations(page)
  api.assertContract()
})

async function assertNavigationMode(page: Page, mobile: boolean): Promise<void> {
  const desktopLinks = page.locator('.global-nav__links')
  const menuButton = page.getByRole('button', { name: 'Open navigation' })
  if (mobile) {
    await expect(desktopLinks).toBeHidden()
    await expect(menuButton).toBeVisible()
  } else {
    await expect(desktopLinks).toBeVisible()
    await expect(menuButton).toBeHidden()
  }
  await expect(page.getByRole('button', { name: '退出登录' })).toBeVisible()
}

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
    .evaluateAll((elements) =>
      elements.flatMap((element) => {
        const rect = element.getBoundingClientRect()
        if (rect.width === 0 || rect.height === 0 || element.getClientRects().length === 0) {
          return []
        }
        if (rect.width >= 43.5 && rect.height >= 43.5) {
          return []
        }
        return [
          {
            element: element.outerHTML.slice(0, 160),
            height: rect.height,
            width: rect.width,
          },
        ]
      }),
    )
  expect(undersized, 'visible interactive targets smaller than 44×44 CSS pixels').toEqual([])
}

async function assertHeadingOrder(page: Page): Promise<void> {
  const levels = await page.locator('h1, h2, h3, h4, h5, h6').evaluateAll((headings) =>
    headings.flatMap((heading) => {
      if (heading.getClientRects().length === 0) {
        return []
      }
      return [Number(heading.tagName.slice(1))]
    }),
  )
  expect(levels[0]).toBe(1)
  expect(levels.filter((level) => level === 1)).toHaveLength(1)
  for (let index = 1; index < levels.length; index += 1) {
    expect(levels[index]).toBeLessThanOrEqual((levels[index - 1] ?? 0) + 1)
  }
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

async function gridColumnCount(locator: Locator): Promise<number> {
  return locator.evaluate((element) => {
    const columns = getComputedStyle(element).gridTemplateColumns.trim()
    return columns === '' || columns === 'none' ? 0 : columns.split(/\s+/).length
  })
}

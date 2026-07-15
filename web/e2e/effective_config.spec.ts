/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { expect, test } from '@playwright/test'

import {
  apiError,
  appOrigin,
  assertNoApplicationStorage,
  assertNoAxeViolations,
  authenticatedSession,
  expectSameOriginCookie,
  installAPIMocks,
  repeatedEffectiveConfig,
  setAuthenticatedCookie,
} from './support/api'

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

test('repeated paths remain independently selectable by response-scoped ID', async ({ page }) => {
  const api = await installAPIMocks(page, {
    session: { status: 200, body: authenticatedSession },
    effectiveConfig: { status: 200, body: repeatedEffectiveConfig },
  })

  await page.goto('/configuration')

  await expect(page.getByRole('heading', { level: 1, name: '生效配置' })).toBeVisible()
  const navigator = page.getByRole('navigation', { name: '生效配置加载顺序' })
  await expect(navigator.getByRole('button')).toHaveCount(3)
  await expect(
    navigator.getByRole('button', { name: '第 2 项 /etc/nginx/conf.d/repeated.conf' }),
  ).toBeVisible()
  const thirdOccurrence = navigator.getByRole('button', {
    name: '第 3 项 /etc/nginx/conf.d/repeated.conf',
  })
  await thirdOccurrence.click()

  await expect(thirdOccurrence).toHaveAttribute('aria-current', 'true')
  const viewer = page.getByRole('region', {
    name: '配置内容 /etc/nginx/conf.d/repeated.conf',
  })
  await expect(viewer).toContainText('listen 8080;')
  await expect(viewer).toContainText('second.example.test')
  await expect(viewer).not.toContainText('first.example.test')
  await expect(page.getByText('第 3 项', { exact: true }).last()).toBeVisible()

  const wrapButton = page.getByRole('button', { name: '自动换行' })
  await wrapButton.focus()
  await wrapButton.press('Space')
  await expect(page.getByRole('button', { name: '关闭自动换行' })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await expect(viewer).toContainText('second.example.test')
  await expect(page.locator('textarea, [contenteditable="true"]')).toHaveCount(0)
  await expect(page.getByRole('button', { name: /保存|发布|上传|写入/i })).toHaveCount(0)
  await expect(page.locator('#effective-config-refresh-feedback')).toHaveAttribute(
    'aria-live',
    'polite',
  )

  const configCall = api.callsFor('effectiveConfig')[0]
  expect(configCall?.method).toBe('GET')
  expect(configCall?.url).toBe(`${appOrigin}/api/v1/nginx/effective-config`)
  expect(configCall).toBeDefined()
  if (configCall !== undefined) {
    expectSameOriginCookie(configCall)
  }
  await assertNoApplicationStorage(page)
  await assertNoAxeViolations(page)
  api.assertContract()
})

test('failed config refresh keeps selected content and labels it stale', async ({ page }) => {
  const api = await installAPIMocks(page, {
    session: { status: 200, body: authenticatedSession },
    effectiveConfig: [
      { status: 200, body: repeatedEffectiveConfig },
      {
        status: 504,
        body: apiError('NGINX_COMMAND_TIMEOUT', 'nginx -T timed out'),
      },
    ],
  })

  await page.goto('/configuration')
  const secondOccurrence = page.getByRole('button', {
    name: '第 2 项 /etc/nginx/conf.d/repeated.conf',
  })
  await secondOccurrence.click()
  const viewer = page.getByRole('region', {
    name: '配置内容 /etc/nginx/conf.d/repeated.conf',
  })
  await expect(viewer).toContainText('first.example.test')
  await page.getByRole('button', { name: '刷新配置' }).click()

  await expect(page.getByText('旧数据', { exact: true })).toBeVisible()
  await expect(page.getByText('刷新失败', { exact: true })).toBeVisible()
  await expect(page.getByText('刷新失败，正在显示上一次成功获取的数据。')).toBeVisible()
  await expect(page.locator('#effective-config-refresh-feedback')).toHaveText(
    '刷新生效配置失败。',
  )
  await expect(viewer).toContainText('first.example.test')
  expect(api.callsFor('effectiveConfig')).toHaveLength(2)
  await assertNoApplicationStorage(page)
  await assertNoAxeViolations(page)
  api.assertContract()
})

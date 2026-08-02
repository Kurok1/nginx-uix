/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
import { expect, test } from '@playwright/test'

import {
  apiError,
  appOrigin,
  assertOnlyLocalePreferenceStorage,
  assertNoAxeViolations,
  authenticatedSession,
  expectSameOriginCookie,
  installAPIMocks,
  rawEffectiveConfig,
  repeatedEffectiveConfig,
  setAuthenticatedCookie,
} from './support/api'

const localeCases = [
  {
    locale: 'zh-CN',
    title: '生效配置',
    loadOrder: '生效配置加载顺序',
    secondEntry: '第 2 项 /etc/nginx/conf.d/repeated.conf',
    thirdEntry: '第 3 项 /etc/nginx/conf.d/repeated.conf',
    thirdEntryLabel: '第 3 项',
    contentRegion: '配置内容 /etc/nginx/conf.d/repeated.conf',
    wrap: '自动换行',
    disableWrap: '关闭自动换行',
    refresh: '刷新配置',
    stale: '旧数据',
    refreshFailed: '刷新失败',
    staleMessage: '刷新失败，正在显示上一次成功获取的数据。',
    refreshFeedback: '刷新生效配置失败。',
    structureUnverified: '结构未验证',
    displayMode: '展示模式：',
    rawOutput: '原始输出',
    rawRegion: '原始 Nginx 输出 nginx -T 标准输出',
  },
  {
    locale: 'en-US',
    title: 'Effective configuration',
    loadOrder: 'Effective configuration load order',
    secondEntry: 'Entry 2 /etc/nginx/conf.d/repeated.conf',
    thirdEntry: 'Entry 3 /etc/nginx/conf.d/repeated.conf',
    thirdEntryLabel: 'Entry 3',
    contentRegion: 'Configuration content /etc/nginx/conf.d/repeated.conf',
    wrap: 'Wrap lines',
    disableWrap: 'Disable line wrapping',
    refresh: 'Refresh configuration',
    stale: 'Stale data',
    refreshFailed: 'Refresh failed',
    staleMessage: 'Refresh failed. Showing the last successfully retrieved data.',
    refreshFeedback: 'Effective configuration refresh failed.',
    structureUnverified: 'Structure unverified',
    displayMode: 'Display mode:',
    rawOutput: 'Raw output',
    rawRegion: 'Raw Nginx output nginx -T standard output',
  },
] as const

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

for (const copy of localeCases) {
  test(`${copy.locale} repeated paths remain independently selectable by response-scoped ID`, async ({ page }) => {
    const api = await installAPIMocks(page, {
      session: { status: 200, body: authenticatedSession },
      effectiveConfig: { status: 200, body: repeatedEffectiveConfig },
    })

    await page.goto(`/configuration?lang=${copy.locale}`)

    await expect(page.getByRole('heading', { level: 1, name: copy.title })).toBeVisible()
    const navigator = page.getByRole('navigation', { name: copy.loadOrder })
    await expect(navigator.getByRole('button')).toHaveCount(3)
    await expect(
      navigator.getByRole('button', { name: copy.secondEntry }),
    ).toBeVisible()
    const thirdOccurrence = navigator.getByRole('button', {
      name: copy.thirdEntry,
    })
    await thirdOccurrence.click()

    await expect(thirdOccurrence).toHaveAttribute('aria-current', 'true')
    const viewer = page.getByRole('region', {
      name: copy.contentRegion,
    })
    await expect(viewer).toContainText('listen 8080;')
    await expect(viewer).toContainText('second.example.test')
    await expect(viewer).not.toContainText('first.example.test')
    await expect(page.getByText(copy.thirdEntryLabel, { exact: true }).last()).toBeVisible()

    const wrapButton = page.getByRole('button', { name: copy.wrap })
    await wrapButton.focus()
    await wrapButton.press('Space')
    await expect(page.getByRole('button', { name: copy.disableWrap })).toHaveAttribute(
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
    await assertOnlyLocalePreferenceStorage(page)
    await assertNoAxeViolations(page)
    api.assertContract()
  })

  test(`${copy.locale} failed config refresh keeps selected content and labels it stale`, async ({ page }) => {
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

    await page.goto(`/configuration?lang=${copy.locale}`)
    const secondOccurrence = page.getByRole('button', {
      name: copy.secondEntry,
    })
    await secondOccurrence.click()
    const viewer = page.getByRole('region', {
      name: copy.contentRegion,
    })
    await expect(viewer).toContainText('first.example.test')
    await page.getByRole('button', { name: copy.refresh }).click()

    await expect(page.getByText(copy.stale, { exact: true })).toBeVisible()
    await expect(page.getByText(copy.refreshFailed, { exact: true })).toBeVisible()
    await expect(page.getByText(copy.staleMessage)).toBeVisible()
    await expect(page.locator('#effective-config-refresh-feedback')).toHaveText(
      copy.refreshFeedback,
    )
    await expect(viewer).toContainText('first.example.test')
    expect(api.callsFor('effectiveConfig')).toHaveLength(2)
    await assertOnlyLocalePreferenceStorage(page)
    await assertNoAxeViolations(page)
    api.assertContract()
  })

  test(`${copy.locale} raw fallback remains usable without presenting unverified file boundaries`, async ({ page }) => {
    const api = await installAPIMocks(page, {
      session: { status: 200, body: authenticatedSession },
      effectiveConfig: { status: 200, body: rawEffectiveConfig },
    })

    await page.goto(`/configuration?lang=${copy.locale}`)

    await expect(page.getByText(copy.structureUnverified, { exact: true })).toBeVisible()
    await expect(page.getByText('NGINX_UIX_EFFECTIVE_CONFIG_ROOTS', { exact: true })).toBeVisible()
    const displayMode = page.locator('.effective-config__summary > div').filter({
      has: page.getByText(copy.displayMode, { exact: true }),
    })
    await expect(displayMode.getByText(copy.rawOutput, { exact: true })).toBeVisible()
    const viewer = page.getByRole('region', {
      name: copy.rawRegion,
    })
    await expect(viewer).toContainText('configuration file /etc/nginx/nginx.conf')
    await expect(page.getByRole('navigation', { name: copy.loadOrder })).toHaveCount(0)
    await assertOnlyLocalePreferenceStorage(page)
    await assertNoAxeViolations(page)
    api.assertContract()
  })
}

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
  healthyStatus,
  installAPIMocks,
  type PublicSystemStatusDTO,
  setAuthenticatedCookie,
} from './support/api'

const degradedStatus = {
  ...healthyStatus,
  components: { ...healthyStatus.components, nginx: 'degraded' },
  issues: ['NGINX_WORKER_COUNT_MISMATCH'],
} satisfies PublicSystemStatusDTO

const stoppedStatus = {
  ...healthyStatus,
  components: { ...healthyStatus.components, nginx: 'stopped' },
  master: null,
  workers: [],
  startup_validation: null,
  recovery: {
    count: 2,
    last_result: 'invalid_config',
    permanent: false,
  },
  issues: ['NGINX_NOT_RUNNING'],
} satisfies PublicSystemStatusDTO

const unknownStatus = {
  ...healthyStatus,
  components: { ...healthyStatus.components, nginx: 'unknown' },
  master: null,
  workers: [],
  build: null,
  startup_validation: null,
  recovery: null,
  issues: ['NGINX_STATUS_UNKNOWN'],
} satisfies PublicSystemStatusDTO

const agentUnavailableStatus = {
  ...unknownStatus,
  components: { ui: 'healthy', agent: 'unavailable', nginx: 'unknown' },
  issues: ['AGENT_UNAVAILABLE'],
} satisfies PublicSystemStatusDTO

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

test('healthy Dashboard exposes complete read-only runtime evidence', async ({ page }) => {
  const api = await installAPIMocks(page, {
    session: { status: 200, body: authenticatedSession },
    status: { status: 200, body: healthyStatus },
  })

  await page.goto('/?lang=zh-CN')

  await expect(page.getByRole('heading', { level: 1, name: '运行状态' })).toBeVisible()
  await expect(page.getByRole('heading', { level: 2, name: '组件健康' })).toBeVisible()
  await expect(page.getByRole('article', { name: 'UI' })).toContainText('正常')
  await expect(page.getByRole('article', { name: 'Agent' })).toContainText('正常')
  await expect(page.getByRole('article', { name: 'Nginx' })).toContainText('运行中')
  await expect(page.getByText('101', { exact: true })).toBeVisible()
  await expect(page.getByText('102、103', { exact: true })).toBeVisible()
  await expect(page.getByText('nginx/1.30.3', { exact: true })).toBeVisible()
  await expect(page.getByText('--with-http_ssl_module', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { level: 3, name: '启动校验' })).toBeVisible()
  await expect(page.getByRole('heading', { level: 3, name: '自动恢复' })).toBeVisible()
  await expect(page.getByText('未发现问题。')).toBeVisible()
  await expect(page.locator('.status-badge svg[aria-hidden="true"][data-shape]')).toHaveCount(5)
  await expect(page.locator('#dashboard-refresh-feedback')).toHaveAttribute('aria-live', 'polite')
  await expect(page.getByRole('main')).toHaveCount(1)
  await expect(page.getByRole('navigation', { name: '全局导航' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: '分区导航' })).toBeVisible()
  await expect(
    page.getByRole('button', { name: /^(启动|停止|重新加载|重启|start|stop|reload|restart)$/i }),
  ).toHaveCount(0)

  const statusCall = api.callsFor('status')[0]
  expect(statusCall?.method).toBe('GET')
  expect(statusCall?.url).toBe(`${appOrigin}/api/v1/system/status`)
  expect(statusCall).toBeDefined()
  if (statusCall !== undefined) {
    expectSameOriginCookie(statusCall)
  }
  await assertOnlyLocalePreferenceStorage(page)
  await assertNoAxeViolations(page)
  api.assertContract()
})

for (const variant of [
  { name: 'degraded', status: degradedStatus, label: '降级', unknownValues: false },
  { name: 'stopped', status: stoppedStatus, label: '已停止', unknownValues: true },
  { name: 'unknown', status: unknownStatus, label: '未知', unknownValues: true },
  {
    name: 'Agent unavailable',
    status: agentUnavailableStatus,
    label: '不可用',
    unknownValues: true,
  },
] as const) {
  test(`Dashboard keeps ${variant.name} distinct with text and shape`, async ({ page }) => {
    const api = await installAPIMocks(page, {
      session: { status: 200, body: authenticatedSession },
      status: { status: 200, body: variant.status },
    })

    await page.goto('/?lang=zh-CN')

    await expect(page.getByText(variant.label, { exact: true }).first()).toBeVisible()
    const labelledStatus = page.locator('.status-badge', { hasText: variant.label }).first()
    await expect(labelledStatus.locator('svg[aria-hidden="true"][data-shape]')).toHaveCount(1)
    if (variant.unknownValues) {
      await expect(page.getByText('无法确认', { exact: true }).first()).toBeVisible()
    }
    await assertNoAxeViolations(page)
    api.assertContract()
  })
}

test('failed manual refresh retains and labels the last successful sample', async ({ page }) => {
  const api = await installAPIMocks(page, {
    session: { status: 200, body: authenticatedSession },
    status: [
      { status: 200, body: healthyStatus },
      {
        status: 502,
        body: apiError('AGENT_UNAVAILABLE', 'Agent unavailable during refresh'),
      },
    ],
  })

  await page.goto('/?lang=zh-CN')
  await expect(page.getByText('nginx/1.30.3', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '刷新状态' }).click()

  await expect(page.getByText('旧数据', { exact: true })).toBeVisible()
  await expect(
    page.locator('.status-badge', { hasText: '刷新运行状态失败。' }),
  ).toBeVisible()
  await expect(page.getByText('刷新失败，正在显示上一次成功获取的数据。')).toBeVisible()
  await expect(page.locator('#dashboard-refresh-feedback')).toHaveText('刷新运行状态失败。')
  await expect(page.getByText('nginx/1.30.3', { exact: true })).toBeVisible()
  expect(api.callsFor('status')).toHaveLength(2)
  await assertOnlyLocalePreferenceStorage(page)
  await assertNoAxeViolations(page)
  api.assertContract()
})

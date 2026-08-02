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
  csrfToken,
  expectSameOriginCookie,
  healthyStatus,
  installAPIMocks,
  setAuthenticatedCookie,
} from './support/api'

const anonymousSession = {
  status: 401,
  body: apiError('unauthenticated', 'Authentication required'),
}

test('anonymous users are redirected to the accessible Login page', async ({ page }) => {
  const api = await installAPIMocks(page, { session: anonymousSession })

  await page.goto('/?lang=zh-CN')

  await expect(page).toHaveURL((url) =>
    url.pathname === '/login' &&
    url.searchParams.get('lang') === 'zh-CN' &&
    url.searchParams.get('redirect') === '/?lang=zh-CN',
  )
  await expect(page.getByRole('main')).toHaveCount(1)
  await expect(page.getByRole('heading', { level: 1, name: '登录 Nginx UIX' })).toBeVisible()
  await expect(page.getByLabel('用户名')).toHaveAttribute('autocomplete', 'username')
  await expect(page.getByLabel('密码')).toHaveAttribute('autocomplete', 'current-password')
  await expect(page.getByRole('button', { name: '登录' })).toBeVisible()
  expect(api.callsFor('session')[0]?.headers.cookie).toBeUndefined()
  await assertNoAxeViolations(page)
  api.assertContract()
})

test('successful keyboard login sends Origin and logout sends current CSRF', async ({ page }) => {
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
    logout: {
      status: 204,
      headers: {
        'Set-Cookie': 'nginx_uix_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Strict',
      },
    },
  })

  await page.goto('/login?lang=zh-CN')
  await page.getByLabel('用户名').fill('admin')
  await page.getByLabel('密码').fill('correct horse battery staple')
  await page.getByLabel('密码').press('Enter')

  await expect(page).toHaveURL(`${appOrigin}/?lang=zh-CN`)
  await expect(page.getByRole('heading', { level: 1, name: '运行状态' })).toBeVisible()
  const loginCall = api.callsFor('login')[0]
  expect(loginCall).toBeDefined()
  expect(loginCall?.headers.origin).toBe(appOrigin)
  expect(loginCall?.headers['x-csrf-token']).toBeUndefined()
  expect(loginCall?.headers.cookie).toBeUndefined()
  expect(loginCall?.postData).toBe(
    JSON.stringify({ username: 'admin', password: 'correct horse battery staple' }),
  )
  await assertOnlyLocalePreferenceStorage(page)
  await assertNoAxeViolations(page)

  const statusCall = api.callsFor('status')[0]
  expect(statusCall).toBeDefined()
  if (statusCall !== undefined) {
    expectSameOriginCookie(statusCall)
  }

  await page.getByRole('button', { name: '退出登录' }).click()

  await expect(page).toHaveURL(`${appOrigin}/login?lang=zh-CN`)
  const logoutCall = api.callsFor('logout')[0]
  expect(logoutCall).toBeDefined()
  expect(logoutCall?.headers.origin).toBe(appOrigin)
  expect(logoutCall?.headers['x-csrf-token']).toBe(csrfToken)
  expect(logoutCall).toBeDefined()
  if (logoutCall !== undefined) {
    expectSameOriginCookie(logoutCall)
  }
  await assertNoAxeViolations(page)
  api.assertContract()
})

test('generic login failure is announced without moving password focus', async ({ page }) => {
  const api = await installAPIMocks(page, {
    session: anonymousSession,
    login: {
      status: 503,
      body: apiError('SERVICE_UNAVAILABLE', 'Authentication service unavailable'),
    },
  })

  await page.goto('/login?lang=zh-CN')
  await page.getByLabel('用户名').fill('admin')
  await page.getByLabel('密码').fill('wrong-password')
  await page.getByLabel('密码').press('Enter')

  await expect(page.getByText('登录服务暂时不可用，请稍后重试。')).toBeVisible()
  await expect(page.getByLabel('密码')).toBeFocused()
  await expect(page.locator('#login-error')).toHaveAttribute('aria-live', 'polite')
  await assertNoAxeViolations(page)
  api.assertContract()
})

test('rate-limited login exposes and completes the Retry-After countdown', async ({ page }) => {
  const api = await installAPIMocks(page, {
    session: anonymousSession,
    login: {
      status: 429,
      body: apiError('rate_limited', 'Too many attempts'),
      headers: { 'Retry-After': '2' },
    },
  })

  await page.goto('/login?lang=zh-CN')
  await page.getByLabel('用户名').fill('admin')
  await page.getByLabel('密码').fill('wrong-password')
  await page.getByRole('button', { name: '登录' }).click()

  await expect(page.getByText('登录尝试过于频繁，请稍后重试。')).toBeVisible()
  await expect(page.getByText('2 秒后可重试。')).toBeVisible()
  await expect(page.getByRole('button', { name: '2 秒后重试' })).toBeDisabled()
  await expect(page.getByLabel('用户名')).toHaveAttribute(
    'aria-describedby',
    'login-error login-retry-status',
  )
  await assertNoAxeViolations(page)
  await expect(page.getByRole('button', { name: '登录' })).toBeEnabled({ timeout: 3_500 })
  await assertOnlyLocalePreferenceStorage(page)
  api.assertContract()
})

test('a restored HttpOnly-cookie session bypasses Login without Web Storage', async ({
  context,
  page,
}) => {
  await setAuthenticatedCookie(context)
  const api = await installAPIMocks(page, {
    session: { status: 200, body: authenticatedSession },
    status: { status: 200, body: healthyStatus },
  })

  await page.goto('/login?lang=zh-CN')

  await expect(page).toHaveURL(`${appOrigin}/?lang=zh-CN`)
  await expect(page.getByRole('heading', { level: 1, name: '运行状态' })).toBeVisible()
  await assertOnlyLocalePreferenceStorage(page)
  await assertNoAxeViolations(page)
  const sessionCall = api.callsFor('session')[0]
  const statusCall = api.callsFor('status')[0]
  expect(sessionCall).toBeDefined()
  expect(statusCall).toBeDefined()
  if (sessionCall !== undefined) {
    expectSameOriginCookie(sessionCall)
  }
  if (statusCall !== undefined) {
    expectSameOriginCookie(statusCall)
  }
  api.assertContract()
})

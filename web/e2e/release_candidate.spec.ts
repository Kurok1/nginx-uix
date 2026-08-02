/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.6.0
 */
import { expect, test } from '@playwright/test'

import {
  apiError,
  appOrigin,
  assertOnlyLocalePreferenceStorage,
  authenticatedSession,
  csrfToken,
  healthyStatus,
  installAPIMocks,
  installWorkspaceAPIFixture,
} from './support/api'
import {
  certificateOrderPlan,
  certificatePlanID,
  certificateServerCandidate,
  dnsCredentialID,
  installCertificateAPIFixture,
  productionAccountID,
  productionRiskPhrase,
  stagingAccountID,
} from './support/certificates'
import { installReleaseAPIFixture } from './support/release'
import { installRouteLabAPIFixture } from './support/route_lab'

const anonymousSession = {
  status: 401,
  body: apiError('unauthenticated', 'Authentication required'),
}

test('login-publish-route-test-and-issue-certificate', async ({ context, page }) => {
  await page.setViewportSize({ width: 1280, height: 1000 })

  // 1. Authenticate through the public boundary and establish only an HttpOnly cookie.
  const loginAPI = await installAPIMocks(page, {
    session: anonymousSession,
    login: {
      status: 200,
      body: authenticatedSession,
      headers: {
        'Set-Cookie': 'nginx_uix_session=e2e-session; Path=/; HttpOnly; SameSite=Strict',
      },
    },
    status: { status: 200, body: healthyStatus },
  })
  await page.goto('/login?lang=en-US')
  await page.getByLabel('Username').fill('admin')
  await page.getByLabel('Password').fill('correct horse battery staple')
  await page.getByLabel('Password').press('Enter')
  await expect(page).toHaveURL(`${appOrigin}/?lang=en-US`)
  await expect(page.getByRole('heading', { level: 1, name: 'Runtime status' })).toBeVisible()
  await assertOnlyLocalePreferenceStorage(page)
  expect(loginAPI.callsFor('login')).toHaveLength(1)
  loginAPI.assertContract()

  // 2. Review the complete draft, validate it, and publish with the workspace-name confirmation.
  await page.unrouteAll({ behavior: 'wait' })
  const workspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const release = await installReleaseAPIFixture(page, workspace)
  await page.goto(`/config/workspaces/${workspace.workspaceId}?lang=en-US`)
  const workspaceReview = page.getByRole('region', { name: 'Workspace review' })
  await workspaceReview.getByRole('button', { name: 'Review all file diffs' }).click()
  await expect(workspaceReview.getByRole('region', { name: 'Unified configuration diff' })).toBeVisible()
  await page.getByRole('button', { name: 'Check publication' }).click()
  await expect(page.getByText('Production configuration has not been changed.')).toBeVisible()
  await page.getByRole('button', { name: 'Publish…' }).click()
  const publishDialog = page.getByRole('dialog', { name: 'Publish configuration to production?' })
  await publishDialog.getByRole('textbox').fill('E2E workspace')
  await publishDialog.getByRole('button', { name: 'Publish', exact: true }).click()
  const releaseProgress = page.getByRole('region', { name: 'Release progress' })
  await expect(releaseProgress.getByText('Published successfully', { exact: true })).toBeVisible()
  await expect(releaseProgress.getByText('Nginx is running and the fixed health check passed.')).toBeVisible()
  await expect(releaseProgress.getByText(release.backupId)).toBeVisible()
  expect(release.requests().filter(({ method, path }) =>
    method === 'POST' && path.endsWith('/releases'))).toHaveLength(1)
  workspace.assertContract()
  release.assertContract()

  // 3. Keep the same browser session while proving static and isolated runtime route evidence.
  await page.unrouteAll({ behavior: 'wait' })
  const routeWorkspace = await installWorkspaceAPIFixture(page, { seedWorkspace: true })
  const routeLab = await installRouteLabAPIFixture(page, routeWorkspace)
  await page.goto('/config/route-lab?lang=en-US')
  await page.getByRole('textbox', { name: /^Host HTTP/ }).fill('example.test')
  await page.getByLabel(/URI path/).fill('/api/users')
  await page.getByRole('button', { name: 'Analyze route' }).click()
  await expect(page.getByRole('region', { name: 'Candidate explanation' })).toContainText(
    'Static analysis — prediction only',
  )
  await page.getByRole('button', { name: 'Run isolated test' }).click()
  const runtimeEvidence = page.getByRole('region', { name: 'Runtime evidence' })
  await expect(runtimeEvidence.getByText('Runtime completed')).toBeVisible()
  await expect(runtimeEvidence).toContainText('production Nginx was not reloaded')
  expect(routeLab.callsFor(
    'POST',
    `/api/v1/config/workspaces/${routeWorkspace.workspaceId}/route-tests`,
  )).toHaveLength(1)
  routeLab.assertContract()
  routeWorkspace.assertContract()

  // 4. Plan and execute a production wildcard DNS-01 certificate request with both confirmations.
  await page.unrouteAll({ behavior: 'wait' })
  const certificateAPI = await installCertificateAPIFixture(page)
  await page.goto('/certificates?lang=en-US')
  await page.getByRole('button', { name: 'Request', exact: true }).click()
  await page.getByLabel('Domain 1').fill('*.example.test')
  await page.getByLabel('Validation method').selectOption('cloudflare_dns_01')
  await page.getByLabel('ACME account', { exact: true }).selectOption(productionAccountID)
  await page.getByLabel('Staging preflight account').selectOption(stagingAccountID)
  await page.getByLabel('Cloudflare Token credential').selectOption(dnsCredentialID)
  await page
    .locator('section[aria-labelledby="certificate-request-title"]')
    .getByRole('checkbox', { name: /Editable/ })
    .check()
  await page.getByRole('button', { name: 'Review certificate request' }).click()
  const certificateReview = page.locator('.certificate-request__review')
  await certificateReview.getByLabel('Complete certificate binding diff').click()
  await page.getByLabel('Type “*.example.test” exactly to confirm').fill('*.example.test')
  await page.getByLabel(`Type “${productionRiskPhrase}” to acknowledge the missing staging evidence`).fill(
    productionRiskPhrase,
  )
  await certificateReview.getByRole('button', { name: 'Issue certificate' }).click()
  await expect(page.getByText('issue · succeeded')).toBeVisible()
  await expect(page.getByText('completed — succeeded')).toBeVisible()
  expect(certificateAPI.callsFor('POST', '/api/v1/certificate-order-plans')).toHaveLength(1)
  expect(certificateAPI.callsFor(
    'POST',
    `/api/v1/certificate-order-plans/${certificatePlanID}/executions`,
  )).toHaveLength(1)
  expect(certificateOrderPlan.server_refs).toEqual([certificateServerCandidate.ref])
  await assertOnlyLocalePreferenceStorage(page)
  certificateAPI.assertContract()

  // 5. Logout with the current CSRF token, then prove a protected deep link requires authentication.
  await page.unrouteAll({ behavior: 'wait' })
  const logoutAPI = await installAPIMocks(page, {
    session: anonymousSession,
    logout: {
      status: 204,
      headers: {
        'Set-Cookie': 'nginx_uix_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Strict',
      },
    },
  })
  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page).toHaveURL(`${appOrigin}/login?lang=en-US`)
  const logoutCall = logoutAPI.callsFor('logout')[0]
  expect(logoutCall?.headers.origin).toBe(appOrigin)
  expect(logoutCall?.headers['x-csrf-token']).toBe(csrfToken)
  expect((await context.cookies(appOrigin)).find(({ name }) => name === 'nginx_uix_session')).toBeUndefined()
  await page.goto('/certificates?lang=en-US')
  await expect(page).toHaveURL((url) =>
    url.pathname === '/login' &&
    url.searchParams.get('lang') === 'en-US' &&
    url.searchParams.get('redirect') === '/certificates?lang=en-US',
  )
  logoutAPI.assertContract()
})

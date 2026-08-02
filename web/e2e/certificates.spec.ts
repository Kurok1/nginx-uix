/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */
import { expect, test, type Page } from '@playwright/test'

import {
  assertOnlyLocalePreferenceStorage,
  assertNoAxeViolations,
  csrfToken,
  setAuthenticatedCookie,
} from './support/api'
import {
  certificateID,
  certificateOrderPlan,
  certificatePlanID,
  certificateServerCandidate,
  certificateTaskID,
  dnsCredentialID,
  installCertificateAPIFixture,
  productionAccountID,
  productionRiskPhrase,
  stagingAccountID,
} from './support/certificates'

test.beforeEach(async ({ context }) => {
  await setAuthenticatedCookie(context)
})

test('Cloudflare Token uses the authenticated CSRF boundary and never persists in browser state', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 1000 })
  const certificateAPI = await installCertificateAPIFixture(page)
  const token = 'browser-only-cloudflare-token'

  await page.goto(`/certificates/${certificateID}`)
  await expect(page.getByRole('heading', { level: 1, name: 'Certificates' })).toBeVisible()
  await page.getByRole('button', { name: 'Accounts', exact: true }).click()

  const tokenInput = page.getByRole('textbox', { name: 'Cloudflare API Token' })
  await expect(tokenInput).toHaveAttribute('type', 'password')
  await page.getByLabel('Credential name').fill('Browser restricted zone')
  await tokenInput.fill(token)
  await page.getByRole('button', { name: 'Verify and save Token' }).click()

  await expect(page.getByText(/Cloudflare Token verified and saved/)).toBeVisible()
  await expect(tokenInput).toHaveValue('')
  await expect(page.getByText('fedcba9876543210')).toBeVisible()
  expect(await page.content()).not.toContain(token)

  const writes = certificateAPI.callsFor('POST', '/api/v1/certificate-dns-credentials')
  expect(writes).toHaveLength(1)
  expect(writes[0]).toMatchObject({
    body: { name: 'Browser restricted zone', api_token: token },
    headers: {
      origin: 'http://127.0.0.1:4173',
      'x-csrf-token': csrfToken,
    },
    query: '',
  })

  await assertOnlyLocalePreferenceStorage(page)
  await assertNoAxeViolations(page)
  certificateAPI.assertContract()
})

test('wildcard issuance requires Cloudflare DNS-01, exact review, risk acknowledgement and persisted history', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 1100 })
  const certificateAPI = await installCertificateAPIFixture(page)

  await page.goto('/certificates')
  await page.getByRole('button', { name: 'Request', exact: true }).click()
  await page.getByLabel('Domain 1').fill('*.example.test')
  await page.getByRole('button', { name: 'Review certificate request' }).click()
  await expect(page.getByRole('alert')).toContainText(
    'Wildcard certificates require Cloudflare DNS-01',
  )
  expect(certificateAPI.callsFor('POST', '/api/v1/certificate-order-plans')).toHaveLength(0)

  await page.getByLabel('Validation method').selectOption('cloudflare_dns_01')
  await page.getByLabel('ACME account', { exact: true }).selectOption(productionAccountID)
  await page.getByLabel('Staging preflight account').selectOption(stagingAccountID)
  await page.getByLabel('Cloudflare Token credential').selectOption(dnsCredentialID)
  await page
    .locator('section[aria-labelledby="certificate-request-title"]')
    .getByRole('checkbox', { name: /Editable/ })
    .check()
  await page.getByRole('button', { name: 'Review certificate request' }).click()

  const review = page.locator('.certificate-request__review')
  await expect(review.getByRole('heading', { name: '5. Review' })).toBeVisible()
  await expect(review).toContainText('No certificate or Nginx configuration has been changed.')
  await expect(review).toContainText('A matching staging preflight is required before production')
  await expect(review.getByLabel('Complete certificate binding diff')).toContainText(
    'ssl_certificate_key',
  )
  await expect(review.getByRole('button', { name: 'Issue certificate' })).toBeDisabled()

  const plans = certificateAPI.callsFor('POST', '/api/v1/certificate-order-plans')
  expect(plans).toHaveLength(1)
  expect(plans[0]?.body).toEqual({
    identifiers: ['*.example.test'],
    challenge: 'cloudflare_dns_01',
    account_id: productionAccountID,
    staging_account_id: stagingAccountID,
    dns_credential_id: dnsCredentialID,
    server_refs: [certificateServerCandidate.ref],
  })

  await page.getByLabel('Type “*.example.test” exactly to confirm').fill('*.example.test')
  await page.getByLabel(`Type “${productionRiskPhrase}” to acknowledge the missing staging evidence`).fill(
    productionRiskPhrase,
  )
  await review.getByRole('button', { name: 'Issue certificate' }).click()

  await expect(page.getByRole('button', { name: 'History', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await expect(page.getByText('issue · succeeded')).toBeVisible()
  await expect(page.getByText('completed — succeeded')).toBeVisible()
  const executions = certificateAPI.callsFor(
    'POST',
    `/api/v1/certificate-order-plans/${certificatePlanID}/executions`,
  )
  expect(executions).toHaveLength(1)
  expect(executions[0]?.body).toEqual({
    confirmation: certificateOrderPlan.primary_identifier,
    production_risk_confirmation: productionRiskPhrase,
  })
  expect(certificateTaskID).toHaveLength(32)
  await assertOnlyLocalePreferenceStorage(page)
  certificateAPI.assertContract()
})

test('zh-CN wildcard request wizard keeps domains and risk confirmations exact', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 1100 })
  const certificateAPI = await installCertificateAPIFixture(page)

  await page.goto('/certificates?lang=zh-CN')
  await expect(page.getByRole('heading', { level: 1, name: '证书管理' })).toBeVisible()
  await page.getByRole('button', { name: '申请', exact: true }).click()
  const request = page.locator('section[aria-labelledby="certificate-request-title"]')
  await request.getByLabel('域名 1').fill('*.example.test')
  await request.getByLabel('验证方式').selectOption('cloudflare_dns_01')
  await request.getByLabel('ACME 账户', { exact: true }).selectOption(productionAccountID)
  await request.getByLabel('Staging 预检账户').selectOption(stagingAccountID)
  await request.getByLabel('Cloudflare Token 凭据').selectOption(dnsCredentialID)
  await request
    .getByRole('checkbox', { name: /可编辑/u })
    .check()
  await page.getByRole('button', { name: '审查证书申请' }).click()

  const review = page.locator('.certificate-request__review')
  await expect(review.getByRole('heading', { name: '5. 审查' })).toBeVisible()
  await expect(review).toContainText('*.example.test')
  await expect(review.getByLabel('完整证书绑定差异')).toContainText('ssl_certificate_key')
  const issue = review.getByRole('button', { name: '签发证书' })
  await expect(issue).toBeDisabled()
  await page.getByLabel('准确输入“*.example.test”以确认').fill('*.example.test')
  await page
    .getByLabel(`输入“${productionRiskPhrase}”以确认缺少 staging 证据`)
    .fill(productionRiskPhrase)
  await issue.click()

  await expect(page.getByRole('button', { name: '历史', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  const executions = certificateAPI.callsFor(
    'POST',
    `/api/v1/certificate-order-plans/${certificatePlanID}/executions`,
  )
  expect(executions).toHaveLength(1)
  expect(executions[0]?.body).toEqual({
    confirmation: '*.example.test',
    production_risk_confirmation: productionRiskPhrase,
  })
  await assertOnlyLocalePreferenceStorage(page)
  await assertNoAxeViolations(page)
  certificateAPI.assertContract()
})

test('certificate export and account deactivation dialogs trap focus, escape and restore the trigger', async ({
  page,
}) => {
  const certificateAPI = await installCertificateAPIFixture(page)

  await page.goto(`/certificates/${certificateID}`)
  const exportTrigger = page.getByRole('button', { name: 'Export certificate' })
  await exportTrigger.click()
  let dialog = page.getByRole('dialog', { name: 'Export certificate' })
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
  await dialog.getByLabel('Include private key').check()
  await dialog.getByLabel('Type the full certificate ID to export').fill(certificateID)
  await expect(dialog.getByRole('button', { name: 'Export', exact: true })).toBeDisabled()
  await dialog.getByLabel('Type “EXPORT PRIVATE KEY” as the second confirmation').fill(
    'EXPORT PRIVATE KEY',
  )
  await expect(dialog.getByRole('button', { name: 'Export', exact: true })).toBeEnabled()
  await dialog.press('Escape')
  await expect(exportTrigger).toBeFocused()

  await page.getByRole('button', { name: 'Accounts', exact: true }).click()
  const deactivateTrigger = page.locator(
    `[data-action="deactivate-account"][data-id="${stagingAccountID}"]`,
  )
  await deactivateTrigger.click()
  dialog = page.getByRole('dialog', { name: 'Deactivate ACME account?' })
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
  await expect(dialog).toContainText('renewals using this account stop')
  const deactivationConfirmation = dialog.getByLabel(
    `Type ${stagingAccountID} exactly to confirm`,
  )
  await deactivationConfirmation.fill(stagingAccountID)
  await deactivationConfirmation.press('Shift+Tab')
  await expect(dialog.getByRole('button', { name: 'Deactivate account' })).toBeFocused()
  await dialog.press('Escape')
  await expect(deactivateTrigger).toBeFocused()

  certificateAPI.assertContract()
})

test('certificate screens reflow without horizontal overflow at every documented breakpoint', async ({
  page,
}) => {
  const certificateAPI = await installCertificateAPIFixture(page)

  for (const width of [1069, 833, 734, 640, 480, 320]) {
    await page.setViewportSize({ width, height: 1000 })
    await page.goto(`/certificates/${certificateID}`)
    await expect(page.getByRole('heading', { level: 1, name: 'Certificates' })).toBeVisible()
    if (width <= 640) {
      await page.getByRole('button', { name: 'Request', exact: true }).click()
      await expect(page.getByRole('heading', { level: 2, name: 'Request certificate' })).toBeVisible()
    }
    await assertNoPageOverflow(page)
    await assertMinimumTargets(page)
    await assertHeadingOrder(page)
  }

  await assertOnlyLocalePreferenceStorage(page)
  await assertNoAxeViolations(page)
  certificateAPI.assertContract()
})

test('certificate overview reflows at 200 and 400 percent browser-style content zoom', async ({ page }) => {
  const certificateAPI = await installCertificateAPIFixture(page)

  for (const scenario of [
    { viewportWidth: 640, zoom: 2 },
    { viewportWidth: 1280, zoom: 4 },
  ]) {
    await page.setViewportSize({ width: scenario.viewportWidth, height: 1000 })
    await page.goto(`/certificates/${certificateID}`)
    await page.evaluate((zoom) => {
      document.documentElement.style.zoom = String(zoom)
    }, scenario.zoom)
    await expect(page.getByRole('heading', { level: 1, name: 'Certificates' })).toBeVisible()
    await assertNoPageOverflow(page)
  }
  await assertOnlyLocalePreferenceStorage(page)
  certificateAPI.assertContract()
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

async function assertMinimumTargets(page: Page): Promise<void> {
  const undersized = await page
    .locator('a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])')
    .evaluateAll((elements) => elements.flatMap((element) => {
      if (element.closest('[aria-hidden="true"], [inert]') !== null) return []
      const ownRect = element.getBoundingClientRect()
      const style = getComputedStyle(element)
      if (
        ownRect.width === 0 ||
        ownRect.height === 0 ||
        element.getClientRects().length === 0 ||
        style.display === 'none' ||
        style.visibility === 'hidden'
      ) return []
      const input = element instanceof HTMLInputElement ? element : null
      const label = input?.type === 'checkbox' || input?.type === 'radio'
        ? element.closest('label')
        : null
      const targetRect = label?.getBoundingClientRect() ?? ownRect
      if (targetRect.width >= 43.5 && targetRect.height >= 43.5) return []
      return [{
        element: element.outerHTML.slice(0, 180),
        height: targetRect.height,
        width: targetRect.width,
      }]
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

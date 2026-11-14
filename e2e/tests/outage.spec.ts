import { expect, test } from '@playwright/test'
import { approve, generateContent, setOutage, shot, startFromExample } from '../support/app'

// The SMS provider goes away while a campaign is being sent: the screen says so, nothing is lost, and when the provider is back the
// campaign finishes. The switch is the mock provider's (reachable only inside this test network).

test.afterEach(async ({ request }) => {
  await setOutage(request, false) // whatever happened, the next scenario starts with a provider that works
})

test('an outage of the SMS provider is shown, and the delivery finishes after it', async ({ page, request }) => {
  await startFromExample(page)
  await generateContent(page)

  await setOutage(request, true)
  await approve(page)

  const badge = page.getByText('SMS provider’ına ulaşılamıyor', { exact: false })
  await expect(badge).toBeVisible({ timeout: 60_000 })
  await shot(page, '07-provider-outage')
  await expect(page.getByText('Gönderim tamamlandı')).toHaveCount(0)

  await setOutage(request, false)
  await expect(page.getByText('Gönderim tamamlandı')).toBeVisible({ timeout: 120_000 })
  await expect(badge).toBeHidden()
  await expect(page.locator('[data-channel="sms"] [data-count="failed"]')).toHaveText('0')
  await expect(page.locator('[data-channel="sms"] [data-count="queued"]')).toHaveText('0')
})

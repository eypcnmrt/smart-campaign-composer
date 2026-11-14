import { expect, test } from '@playwright/test'
import { approve, audienceCount, field, FIELDS, shot, startFromBrief } from '../support/app'

// The whole way a marketer goes, in a real browser against the real stack (mock model, mock provider): brief, audience, content that
// is written while one watches, an error in it and its fix, approval, and a delivery that ends with numbers that agree.

const BRIEF = 'İstanbul’da yaşayan ve son 30 günde sepete ürün ekleyip satın almayan müşterilere %10 indirim için SMS ve e-posta gönder.'

test('brief to a finished delivery', async ({ page }) => {
  await startFromBrief(page, BRIEF)
  await shot(page, '01-brief-and-audience')

  // The audience: the rule in words, and counts that make sense together.
  await expect(page.getByRole('heading', { level: 3, name: 'Kural' })).toBeVisible()
  await expect(page.locator('.block').first()).toContainText('İstanbul')
  const matched = await audienceCount(page, 'matched')
  const sms = await audienceCount(page, 'sms')
  const email = await audienceCount(page, 'email')
  const total = await audienceCount(page, 'total')
  expect(matched).toBeGreaterThan(0)
  expect(matched).toBeLessThan(total)
  expect(sms).toBeGreaterThan(0)
  expect(email).toBeGreaterThan(0)
  expect(sms).toBeLessThanOrEqual(matched)
  expect(email).toBeLessThanOrEqual(matched)

  // The content is written while one watches: the SMS field is seen with some of its text before the whole of it.
  await page.getByRole('button', { name: 'İçeriği üret' }).click()
  await expect(page.getByRole('heading', { level: 2, name: 'İçerik' })).toBeVisible()
  const seen = new Set<string>()
  const done = page.getByRole('button', { name: 'Onaya geç' })
  const deadline = Date.now() + 30_000
  while (!(await done.isVisible()) && Date.now() < deadline) {
    const value = await field(page, FIELDS.sms).inputValue()
    if (value !== '') seen.add(value)
    await page.waitForTimeout(60)
  }
  await expect(done).toBeVisible()
  const final = await field(page, FIELDS.sms).inputValue()
  const partial = [...seen].filter((v) => v !== final && final.startsWith(v))
  expect(partial.length, `partly written states of the SMS text that were seen: ${JSON.stringify([...seen])}`).toBeGreaterThanOrEqual(2)
  await shot(page, '02-content-written')

  // An unknown variable is said, with the field marked, and saving is not possible.
  await field(page, FIELDS.sms).fill('Merhaba {{firstname}}, %10 indirim seni bekliyor!')
  await expect(page.getByText('Bilinmeyen değişken {{firstname}}')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Değişiklikleri kaydet' })).toBeDisabled()
  await expect(page.getByText('Önce hataları düzeltin')).toBeVisible()
  await shot(page, '03-unknown-variable')

  // The fix: saving is possible, and afterwards approval is the primary action again.
  await field(page, FIELDS.sms).fill('Merhaba {{first_name}}, %10 indirim seni bekliyor!')
  await expect(page.getByText('Bilinmeyen değişken')).toHaveCount(0)
  await page.getByRole('button', { name: 'Değişiklikleri kaydet' }).click()
  await expect(page.getByText('Kaydedilecek değişiklik yok')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Onaya geç' })).toBeEnabled()

  await approve(page)
  await shot(page, '04-approved')

  // The delivery ends, and what the screen counts is what was meant to be sent.
  await expect(page.getByText('Gönderim tamamlandı')).toBeVisible({ timeout: 60_000 })
  const sent = async (channel: 'sms' | 'email') =>
    Number((await page.locator(`[data-channel="${channel}"] [data-count="sent"]`).innerText()).replace(/\./g, ''))
  expect(await sent('sms')).toBe(sms)
  expect(await sent('email')).toBe(email)
  for (const channel of ['sms', 'email'] as const) {
    await expect(page.locator(`[data-channel="${channel}"] [data-count="failed"]`)).toHaveText('0')
    await expect(page.locator(`[data-channel="${channel}"] [data-count="queued"]`)).toHaveText('0')
  }

  // The evidence: a chart for each channel and the summary line.
  await expect(page.getByRole('img', { name: /SMS: .*mesaj kabul edildi/ })).toBeVisible()
  await expect(page.getByRole('img', { name: /E-posta: .*mesaj kabul edildi/ })).toBeVisible()
  const summary = page.getByText(/^Süre /)
  await expect(summary).toBeVisible()
  // The summary line has no speed (the cap is per channel; a figure of both channels together looks like a broken cap) ...
  await expect(summary).not.toContainText(/mesaj\/sn|ortalama|en yüksek/)
  // ... the speeds are in each channel's card, next to its own cap of 100/s.
  for (const channel of ['sms', 'email']) {
    const caption = (await page.locator(`[data-channel="${channel}"] .chart__caption`).innerText()).replace(/\s+/g, ' ')
    const figure = (re: RegExp) => Number(re.exec(caption)?.[1]?.replace(/\./g, '').replace(',', '.'))
    const average = figure(/Ortalama: ([\d.,]+)\/s/)
    const peak = figure(/En yoğun: ([\d.,]+)\/s/)
    expect(average, `the average of ${channel}`).toBeGreaterThan(0)
    expect(peak, `the peak of ${channel}`).toBeGreaterThan(0)
    // buckets are by the time the answer was recorded, so a little over the cap is not a broken cap; double would be one
    expect(peak, `${channel} is capped at 100/s`).toBeLessThanOrEqual(110)
    expect(average, `${channel} is capped at 100/s`).toBeLessThanOrEqual(110)
  }
  await shot(page, '05-delivery-completed')
})

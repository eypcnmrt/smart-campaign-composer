import { expect, test, type Page } from '@playwright/test'
import { BRIEF_EVERYONE, field, FIELDS, generateContent, shot, startFromBrief, startFromExample } from '../support/app'

// The hint of a segment that was made again after the content (its title "Segment değişti" shares an element with a visually hidden label).
const hint = (page: Page) => page.getByText('Segment değişti; içeriği yeniden üretmek ister misin?')

test('a rule that matches everybody warns, and sending needs the acknowledgement', async ({ page }) => {
  await startFromBrief(page, BRIEF_EVERYONE)
  await expect(page.locator('[data-warning="matches_everyone"]')).toBeVisible()
  await expect(page.getByText('Kural herkesi kapsıyor')).toBeVisible()
  await shot(page, '06-warning-matches-everyone')

  await generateContent(page)
  await page.getByRole('button', { name: 'Onaya geç' }).click()
  await expect(page.getByRole('heading', { level: 2, name: 'Onay' })).toBeVisible()

  const send = page.getByRole('button', { name: 'Onayla ve gönder' })
  const ack = page.getByRole('checkbox')
  await expect(send).toBeDisabled()
  await expect(page.getByText('Göndermek için uyarıları onaylayan kutuyu işaretleyin.')).toBeVisible()
  await ack.check()
  await expect(send).toBeEnabled()
  await ack.uncheck()
  await expect(send).toBeDisabled()
})

test('a regenerated segment says the content is old, and regenerating over hand-edited text asks first', async ({ page }) => {
  await startFromExample(page)
  await generateContent(page)

  // Back to the audience, and the segment is made again.
  await page.getByRole('navigation', { name: 'Kampanya adımları' }).getByRole('link', { name: /^Kitle/ }).click()
  await expect(page.getByRole('heading', { level: 2, name: 'Kitle' })).toBeVisible()
  await expect(hint(page)).toHaveCount(0)
  await page.getByRole('button', { name: 'Segmenti yeniden üret' }).click()
  await expect(hint(page)).toBeVisible({ timeout: 30_000 })

  // The content step carries the same hint; the marketer edits the text by hand and saves.
  await page.getByRole('button', { name: 'İçeriğe geç' }).click()
  await expect(page.getByRole('heading', { level: 2, name: 'İçerik' })).toBeVisible()
  await expect(hint(page)).toBeVisible()
  const edited = 'Merhaba {{first_name}}, bu metni elle yazdım.'
  await field(page, FIELDS.sms).fill(edited)
  await page.getByRole('button', { name: 'Değişiklikleri kaydet' }).click()
  await expect(page.getByText('Kaydedilecek değişiklik yok')).toBeVisible()
  await expect(hint(page)).toHaveCount(0) // saving clears the hint

  // Regenerating asks first and says the text was edited; "Vazgeç" leaves it alone.
  await page.getByRole('button', { name: 'İçeriği yeniden üret' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('Bu metni siz düzenlediniz')
  await dialog.getByRole('button', { name: 'Vazgeç' }).click()
  await expect(dialog).toBeHidden()
  await expect(field(page, FIELDS.sms)).toHaveValue(edited)

  // Confirmed, the new text replaces it.
  await page.getByRole('button', { name: 'İçeriği yeniden üret' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Yeniden üret' }).click()
  await expect(page.getByRole('button', { name: 'Onaya geç' })).toBeVisible({ timeout: 30_000 })
  await expect(field(page, FIELDS.sms)).not.toHaveValue(edited)
  await expect(field(page, FIELDS.sms)).not.toHaveValue('')
})

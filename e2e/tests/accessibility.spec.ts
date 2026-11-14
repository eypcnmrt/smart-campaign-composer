import { expect, test } from '@playwright/test'
import { approve, expectNoSeriousViolations, generateContent, startFromExample, tabTo } from '../support/app'

test('axe finds nothing serious or critical on any step', async ({ page }) => {
  await page.goto('/')
  await expectNoSeriousViolations(page, 'the campaign list')

  await page.goto('/campaigns/new')
  await expectNoSeriousViolations(page, 'the brief step')

  await startFromExample(page)
  await expectNoSeriousViolations(page, 'the audience step')

  await generateContent(page)
  await expectNoSeriousViolations(page, 'the content step')

  await page.getByRole('button', { name: 'Onaya geç' }).click()
  await expect(page.getByRole('heading', { level: 2, name: 'Onay' })).toBeVisible()
  await expectNoSeriousViolations(page, 'the approval step')

  await page.getByRole('button', { name: 'Onayla ve gönder' }).click()
  await expect(page.getByText('Gönderim tamamlandı')).toBeVisible({ timeout: 60_000 })
  await expectNoSeriousViolations(page, 'the delivery')
})

test.describe('on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the brief and audience steps fit the width and have nothing serious for axe', async ({ page }) => {
    const fits = () => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)

    await page.goto('/campaigns/new')
    expect(await fits(), 'the brief step scrolls sideways').toBe(true)
    await expectNoSeriousViolations(page, 'the brief step at 390 px')

    await startFromExample(page)
    expect(await fits(), 'the audience step scrolls sideways').toBe(true)
    await expectNoSeriousViolations(page, 'the audience step at 390 px')
  })
})

test('brief to audience with the keyboard alone', async ({ page }) => {
  await page.goto('/campaigns/new')
  const brief = page.getByLabel('Kampanya brief’i')
  await tabTo(page, brief)
  await page.keyboard.type('İstanbul’da yaşayan ve son 30 günde sepete ürün ekleyip satın almayan müşterilere %10 indirim SMS’i gönder.')
  await tabTo(page, page.getByRole('button', { name: 'Kitleyi oluştur' }))
  await page.keyboard.press('Enter')

  // A new page takes the focus itself (the main area), so the next Tab starts in the page, not at the top of the window.
  await expect(page.getByRole('heading', { level: 2, name: 'Kitle' })).toBeVisible()
  await expect(page.locator('main')).toBeFocused()
  await tabTo(page, page.getByRole('button', { name: 'İçeriği üret' }))
  await expect(page.getByRole('button', { name: 'İçeriği üret' })).toBeFocused()
})

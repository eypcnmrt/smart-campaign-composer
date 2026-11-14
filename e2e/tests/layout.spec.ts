import { expect, test } from '@playwright/test'
import { shot } from '../support/app'

// The browser round found the list page touching the window's edge at about 1170 px. What is measured is what the person sees: the
// distance from the window's edge to the nearest content of the page, left and right.
test.describe('at about 1170 px', () => {
  test.use({ viewport: { width: 1170, height: 800 } })

  test('the list page keeps at least 16 px from both edges of the window, and has one "Yeni kampanya" button', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    const edges = await page.evaluate(() => {
      const width = document.documentElement.clientWidth
      let left = Infinity
      let right = Infinity
      for (const el of document.querySelectorAll('main *')) {
        const r = el.getBoundingClientRect()
        if (r.width === 0 || r.height === 0) continue
        left = Math.min(left, r.left)
        right = Math.min(right, width - r.right)
      }
      return { left, right }
    })
    expect(edges.left).toBeGreaterThanOrEqual(16)
    expect(edges.right).toBeGreaterThanOrEqual(16)
    await expect(page.getByRole('link', { name: 'Yeni kampanya' })).toHaveCount(1)
    await shot(page, '08-campaign-list')
  })
})

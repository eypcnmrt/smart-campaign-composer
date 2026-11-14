import AxeBuilder from '@axe-core/playwright'
import { expect, type APIRequestContext, type Locator, type Page } from '@playwright/test'

// What the scenarios share: the way into a campaign, the provider's outage switch, the accessibility scan and the pictures for
// docs/screenshots. Everything is found the way a person finds it (a role, a label, a visible text), not by a class name.

export const PROVIDER_URL = process.env.E2E_PROVIDER_URL ?? 'http://mock-provider:8081'

/** The three example briefs of the brief step, in the order of the page. The first one names SMS only; the demo model reads that. */
export const EXAMPLE_SMS_ONLY = /İstanbul’da yaşayan ve son 30 günde sepete/
/** A brief that asks for everybody: the demo model answers with a rule that matches all users (the `matches_everyone` warning). */
export const BRIEF_EVERYONE = 'Tüm müşterilere yılbaşı indirimi duyurusu gönder; hem SMS hem e-posta olsun.'

export const FIELDS = { sms: 'SMS metni', subject: 'E-posta konusu', body: 'E-posta gövdesi' } as const

/** Opens a new campaign from an example brief and waits for the audience step. */
export async function startFromExample(page: Page, example: RegExp = EXAMPLE_SMS_ONLY): Promise<void> {
  await page.goto('/campaigns/new')
  await page.getByRole('button', { name: example }).click()
  await createAudience(page)
}

/** Opens a new campaign from a brief typed in, and waits for the audience step. */
export async function startFromBrief(page: Page, brief: string): Promise<void> {
  await page.goto('/campaigns/new')
  await page.getByLabel('Kampanya brief’i').fill(brief)
  await createAudience(page)
}

async function createAudience(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Kitleyi oluştur' }).click()
  await expect(page.getByRole('heading', { level: 2, name: 'Kitle' })).toBeVisible()
}

/** The text of one of the four counts of the audience step, as a number ("1.234" is 1234). */
export async function audienceCount(page: Page, which: 'matched' | 'sms' | 'email' | 'total'): Promise<number> {
  const text = await page.locator(`[data-count="${which}"]`).innerText()
  return Number(text.replace(/\./g, ''))
}

export function field(page: Page, label: string): Locator {
  return page.getByLabel(label, { exact: true })
}

/** From the audience step to a finished content: asks for it, and waits until the model is done and the step can move on. */
export async function generateContent(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'İçeriği üret' }).click()
  await expect(page.getByRole('button', { name: 'Onaya geç' })).toBeVisible({ timeout: 30_000 })
}

/** From the content step to the delivery: opens the approval, ticks the acknowledgement if one is asked for, and sends. */
export async function approve(page: Page, options: { acknowledge?: boolean } = {}): Promise<void> {
  await page.getByRole('button', { name: 'Onaya geç' }).click()
  await expect(page.getByRole('heading', { level: 2, name: 'Onay' })).toBeVisible()
  if (options.acknowledge) await page.getByRole('checkbox').check()
  await page.getByRole('button', { name: 'Onayla ve gönder' }).click()
  await expect(page.getByRole('heading', { level: 2, name: 'Gönderim' })).toBeVisible()
}

/** Turns the provider's outage on or off (the switch exists only in this test stack, see docker-compose.e2e.yml). */
export async function setOutage(request: APIRequestContext, on: boolean): Promise<void> {
  const res = await request.post(`${PROVIDER_URL}/control/outage/${on ? 'on' : 'off'}`)
  expect(res.ok(), `the outage switch answered ${res.status()}`).toBe(true)
}

/**
 * Scans the page with axe (WCAG 2 A and AA, and the best practices) and fails on a violation that is serious or critical. The
 * lesser ones (moderate, minor) are reported in the message of the test only when something serious is also there.
 */
export async function expectNoSeriousViolations(page: Page, where: string): Promise<void> {
  await settled(page)
  const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']).analyze()
  const serious = result.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  const report = serious.map((v) => `${v.impact} ${v.id}: ${v.help} (${v.nodes.length}) e.g. ${v.nodes[0]?.target.join(' ')} ${v.nodes[0]?.html.slice(0, 120)} | ${v.nodes[0]?.any[0]?.message ?? ''}`)
  expect(report, `accessibility violations on ${where}`).toEqual([])
}

/** Saves a picture for docs/screenshots when the run was asked to (make e2e SCREENSHOTS=1). */
export async function shot(page: Page, name: string): Promise<void> {
  if (process.env.E2E_SCREENSHOTS !== '1') return
  await page.screenshot({ path: `/screenshots/${name}.png`, fullPage: true })
}

/** Presses Tab until `target` has the focus, at most `max` times (the page starts at the skip link). */
export async function tabTo(page: Page, target: Locator, max = 60): Promise<number> {
  for (let presses = 1; presses <= max; presses++) {
    await page.keyboard.press('Tab')
    const focused = await target.evaluate((el) => el === document.activeElement).catch(() => false)
    if (focused) return presses
  }
  throw new Error(`Tab did not reach the element in ${max} presses`)
}

/**
 * Waits until nothing on the page is moving: the transitions of a button that has just become enabled are over (axe would read the
 * colour of the middle of one) and two frames have been drawn.
 */
async function settled(page: Page): Promise<void> {
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations().map((a) => a.finished.catch(() => undefined)))
    await new Promise<void>((done) => requestAnimationFrame(() => requestAnimationFrame(() => done())))
  })
}

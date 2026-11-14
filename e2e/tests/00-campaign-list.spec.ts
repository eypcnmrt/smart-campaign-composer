import { expect, test, type APIRequestContext } from '@playwright/test'
import { setThrottleNext, shot } from '../support/app'

// A list with something to read in it. Five campaigns in different states are made through the API (the screens of the other
// scenarios cover the way a person makes one): finished, finished after retries, waiting for approval, with a warning about a
// criterion the system does not know, and with a warning that the rule matches everybody. Then the list is looked at, and the
// delivery that needed retries is opened to see the retries in its summary.
//
// The file name sorts first on purpose: the run starts with an empty database, so the list picture (docs/screenshots) shows these
// five and not the campaigns the other scenarios make.

const BRIEFS = {
  finished: 'İzmir’deki müşterilere hafta sonu %15 indirim için SMS gönder.',
  retried: 'İstanbul’da yaşayan ve son 30 günde sepete ürün ekleyip satın almayan müşterilere %10 indirim için SMS ve e-posta gönder.',
  waiting: 'Bursa ve Antalya’daki müşterilere yaz kampanyası: %25 indirim için SMS ve e-posta gönder.',
  unknownCriterion: 'Ankara’daki kadın müşterilere bahar indirimi için e-posta gönder.',
  everybody: 'Tüm müşterilere yılbaşı indirimi duyurusu gönder; hem SMS hem e-posta olsun.',
}

/** How many of the first attempts of the retried campaign the provider turns away (each one becomes a retried message). */
const THROTTLED = 6

async function create(api: APIRequestContext, brief: string): Promise<number> {
  const res = await api.post('/api/campaigns', { data: { brief } })
  expect(res.status(), `creating "${brief}"`).toBe(201)
  return ((await res.json()) as { id: number }).id
}

/** Has the model write the content (the stream ends with a `done` event once it is saved). */
async function writeContent(api: APIRequestContext, id: number): Promise<void> {
  const res = await api.post(`/api/campaigns/${id}/content/stream`)
  expect(res.status()).toBe(200)
  expect(await res.text(), `the content stream of campaign ${id} ended without done`).toContain('event: done')
}

async function approve(api: APIRequestContext, id: number): Promise<void> {
  const got = await api.get(`/api/campaigns/${id}`)
  const { version } = (await got.json()) as { version: number }
  const res = await api.post(`/api/campaigns/${id}/approve`, { data: { version, acknowledge_warnings: true } })
  expect(res.status(), `approving campaign ${id}`).toBe(200)
}

async function waitUntilCompleted(api: APIRequestContext, id: number): Promise<void> {
  await expect
    .poll(async () => ((await (await api.get(`/api/campaigns/${id}/stats`)).json()) as { status: string }).status, {
      message: `campaign ${id} to complete`,
      timeout: 120_000,
      intervals: [1000],
    })
    .toBe('completed')
}

test.afterEach(async ({ request }) => {
  await setThrottleNext(request, 0) // a number that was not used up must not turn another scenario's messages away
})

test('the list shows campaigns in different states, and a delivery that needed retries says so', async ({ page, request }) => {
  test.setTimeout(240_000)

  // The oldest first, so that the list (newest first) reads: warnings, waiting, retried, finished.
  const finished = await create(request, BRIEFS.finished)
  await writeContent(request, finished)
  await approve(request, finished)
  await waitUntilCompleted(request, finished)

  const retried = await create(request, BRIEFS.retried)
  await writeContent(request, retried)
  await setThrottleNext(request, THROTTLED) // exactly this many of the next messages get a 429 and a retry
  await approve(request, retried)
  await waitUntilCompleted(request, retried)

  const waiting = await create(request, BRIEFS.waiting)
  await writeContent(request, waiting)
  await create(request, BRIEFS.unknownCriterion)
  await create(request, BRIEFS.everybody)

  // The delivery that needed retries: nothing failed, and the summary line counts the retried messages and the attempts.
  await page.goto(`/campaigns/${retried}`)
  await expect(page.getByText('Gönderim tamamlandı')).toBeVisible()
  const sent = (channel: 'sms' | 'email') => page.locator(`[data-channel="${channel}"] [data-count="sent"]`)
  const messages = Number((await sent('sms').innerText()).replace(/\./g, '')) + Number((await sent('email').innerText()).replace(/\./g, ''))
  expect(messages).toBeGreaterThan(THROTTLED)
  for (const channel of ['sms', 'email'] as const) {
    await expect(page.locator(`[data-channel="${channel}"] [data-count="failed"]`)).toHaveText('0')
    await expect(page.locator(`[data-channel="${channel}"] [data-count="retrying"]`)).toHaveText('0')
  }
  const summary = page.getByTestId('send-summary')
  await expect(summary).toContainText(`${THROTTLED} mesaj en az bir kez yeniden denendi`)
  await expect(summary).toContainText(`(toplam ${(messages + THROTTLED).toLocaleString('tr-TR')} deneme)`)
  await shot(page, '09-delivery-with-retries')

  // The list: five rows in their states, the newest first.
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1, name: 'Kampanyalar' })).toBeVisible()
  const row = (brief: string) => page.getByRole('row').filter({ hasText: brief.slice(0, 40) })
  await expect(row(BRIEFS.finished)).toContainText('Tamamlandı')
  await expect(row(BRIEFS.retried)).toContainText('Tamamlandı')
  await expect(row(BRIEFS.waiting)).toContainText('Onay bekliyor')
  await expect(row(BRIEFS.unknownCriterion)).toContainText('Kitle hazır')
  await expect(row(BRIEFS.unknownCriterion)).toContainText('1 uyarı')
  await expect(row(BRIEFS.everybody)).toContainText('Kitle hazır')
  await expect(row(BRIEFS.everybody)).toContainText('uyarı')
  await expect(row(BRIEFS.finished)).not.toContainText('uyarı')
  await expect(row(BRIEFS.waiting)).not.toContainText('uyarı')
  // newest first
  const rows = page.getByRole('table').getByRole('row')
  await expect(rows.nth(1)).toContainText('Tüm müşterilere')
  await expect(rows.nth(5)).toContainText('İzmir’deki müşterilere hafta sonu')
  await shot(page, '08-campaign-list')
})

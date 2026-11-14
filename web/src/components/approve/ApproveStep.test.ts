import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '@/App.vue'
import type { PreviewSample } from '@/api/types'
import { createAppRouter } from '@/router'
import { error, installFakeApi } from '@/test/fakeApi'
import { audience, campaign, config, contentReady, sending } from '@/test/fixtures'

afterEach(() => {
  vi.unstubAllGlobals()
})

const samples: PreviewSample[] = [
  { user: { id: 1, first_name: 'Ayşe', city: 'Ankara' }, sms: 'Merhaba Ayşe,\nAnkara’ya özel indirim!', email_subject: 'Ankara’ya özel indirim', email_body: 'Selam Ayşe,\n\nAnkara için indirim başladı.' },
  { user: { id: 2, first_name: 'Mehmet', city: 'Ankara' }, sms: null, email_subject: 'Ankara’ya özel indirim', email_body: 'Selam Mehmet.' },
  { user: { id: 3, first_name: 'Zeynep', city: '' }, sms: null, email_subject: null, email_body: null },
]

const PREVIEW = 'GET /api/campaigns/7/preview'
const APPROVE = 'POST /api/campaigns/7/approve'

async function open(c = contentReady, extra: Parameters<typeof installFakeApi>[0] = {}) {
  const api = installFakeApi({
    'GET /api/config': { body: config },
    'GET /api/campaigns/7': { body: c },
    [PREVIEW]: { body: { samples } },
    ...extra,
  })
  const router = createAppRouter(createMemoryHistory())
  await router.push('/campaigns/7?step=approve')
  await router.isReady()
  const wrapper = mount(App, { global: { plugins: [router] }, attachTo: document.body })
  await vi.waitFor(() => expect(wrapper.find('#approve-title').exists()).toBe(true))
  await flushPromises()
  return { wrapper, router, api }
}

type Wrapper = Awaited<ReturnType<typeof open>>['wrapper']
const approveButton = (w: Wrapper) => {
  const b = w.findAll('main button').find((x) => x.text().startsWith('Onay'))
  if (!b) throw new Error('no approve button')
  return b
}
const checkbox = (w: Wrapper) => w.find('label.ack input[type="checkbox"]')

const withWarning = campaign({ ...contentReady, warnings: [{ code: 'matches_everyone', message: 'English' }] })
const noRecipients = campaign({ ...contentReady, warnings: [{ code: 'no_recipients', message: 'English' }] })

describe('the approval step, what is shown', () => {
  it('summarises the channels with their recipient counts, and the version that was looked at', async () => {
    const { wrapper } = await open(campaign({ ...contentReady, audience: { ...audience, sms: 800, email: 1200 } }))
    const text = wrapper.get('.summary').text()
    expect(text).toContain('SMS: 800 alıcı')
    expect(text).toContain('E-posta: 1.200 alıcı')
    expect(text).toContain('Toplam 2.000 mesaj')
    expect(text).toContain('sürüm: 3')
    wrapper.unmount()
  })

  it('counts only the channels that are open', async () => {
    const { wrapper } = await open(campaign({ ...contentReady, channels: ['sms'] }))
    const text = wrapper.get('.summary').text()
    expect(text).toContain('SMS: 8 alıcı')
    expect(text).not.toContain('E-posta')
    expect(text).toContain('Toplam 8 mesaj')
    wrapper.unmount()
  })

  it('asks for three samples and shows each user with the texts they would get', async () => {
    const { wrapper, api } = await open()
    expect(api.callsTo(PREVIEW)[0]?.path).toBe('/api/campaigns/7/preview?limit=3')
    const items = wrapper.findAll('.sample')
    expect(items).toHaveLength(3)
    expect(items[0]?.get('.sample__who').text()).toContain('Ayşe')
    expect(items[0]?.get('[data-channel="sms"] .message__text').text()).toBe('Merhaba Ayşe,\nAnkara’ya özel indirim!')
    expect(items[0]?.get('[data-channel="email"] .message__subject').text()).toBe('Ankara’ya özel indirim')
    wrapper.unmount()
  })

  it('shows only the channels a user consented to, and says so for a user with none', async () => {
    const { wrapper } = await open()
    const items = wrapper.findAll('.sample')
    expect(items[1]?.find('[data-channel="sms"]').exists()).toBe(false)
    expect(items[1]?.find('[data-channel="email"]').exists()).toBe(true)
    expect(items[2]?.text()).toContain('hiçbirinde mesaj almayı kabul etmemiş')
    wrapper.unmount()
  })

  it('shows the texts as plain text with their line breaks kept, never as markup', async () => {
    const hostile: PreviewSample = {
      user: { id: 9, first_name: '<b>Ali</b>', city: 'X' },
      sms: '<img src=x onerror=alert(1)>\nikinci satır',
      email_subject: '<script>alert(1)</script>',
      email_body: '<a href="javascript:alert(1)">tıkla</a>',
    }
    const { wrapper } = await open(contentReady, { [PREVIEW]: { body: { samples: [hostile] } } })
    expect(wrapper.find('.samples img').exists()).toBe(false)
    expect(wrapper.find('.samples script').exists()).toBe(false)
    expect(wrapper.find('.samples a').exists()).toBe(false)
    expect(wrapper.get('[data-channel="sms"] .message__text').text()).toBe('<img src=x onerror=alert(1)>\nikinci satır')
    expect(wrapper.get('.sample__who').text()).toContain('<b>Ali</b>')
    expect(wrapper.html()).not.toContain('v-html')
    wrapper.unmount()
  })

  it('says what is happening while the samples load, and shows a failure with a retry', async () => {
    let up = false
    const { wrapper, api } = await open(contentReady, {
      [PREVIEW]: () => (up ? { body: { samples } } : error(503, 'not_ready')),
    })
    expect(wrapper.get('.preview [role="alert"]').text()).toContain('Sunucu henüz hazır değil')
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined() // nothing is approved unseen
    up = true
    await wrapper.get('.preview [role="alert"] button').trigger('click')
    await flushPromises()
    expect(api.callsTo(PREVIEW)).toHaveLength(2)
    expect(wrapper.findAll('.sample')).toHaveLength(3)
    wrapper.unmount()
  })
})

describe('the approval step, warnings', () => {
  it('needs no box and no tick when there are no warnings', async () => {
    const { wrapper } = await open()
    expect(checkbox(wrapper).exists()).toBe(false)
    expect(approveButton(wrapper).attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('shows the warnings and keeps the button off until the box is ticked on purpose', async () => {
    const { wrapper } = await open(withWarning)
    expect(wrapper.find('[data-warning="matches_everyone"]').exists()).toBe(true)
    expect(checkbox(wrapper).exists()).toBe(true)
    expect((checkbox(wrapper).element as HTMLInputElement).checked).toBe(false) // never pre-ticked
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('Göndermek için uyarıları onaylayan kutuyu işaretleyin.')
    await checkbox(wrapper).setValue(true)
    expect(approveButton(wrapper).attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('shows nobody-would-receive-anything as a stop: the button is off, the reason is written, and there is no box to tick', async () => {
    const { wrapper, api } = await open(noRecipients)
    expect(wrapper.find('[data-warning="no_recipients"]').classes()).toContain('note--danger')
    expect(checkbox(wrapper).exists()).toBe(false)
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    expect(approveButton(wrapper).attributes('aria-describedby')).toBe('approve-blocked')
    expect(wrapper.get('#approve-blocked').text()).toContain('onaylanamaz')
    await approveButton(wrapper).trigger('click')
    expect(api.callsTo(APPROVE)).toHaveLength(0)
    wrapper.unmount()
  })

  it('does not offer the box for no_recipients even when another warning is there', async () => {
    const { wrapper } = await open(campaign({ ...contentReady, warnings: [{ code: 'matches_everyone', message: 'English' }, { code: 'no_recipients', message: 'English' }] }))
    expect(checkbox(wrapper).exists()).toBe(false)
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})

describe('the approval step, approving', () => {
  it('sends the version that is on screen, and moves on to the delivery', async () => {
    const { wrapper, router, api } = await open(contentReady, { [APPROVE]: { body: { campaign: sending, enqueued: { sms: 8, email: 12 } } } })
    await approveButton(wrapper).trigger('click')
    await flushPromises()
    expect(api.callsTo(APPROVE)[0]?.body).toEqual({ version: 3, acknowledge_warnings: false })
    await vi.waitFor(() => expect(router.currentRoute.value.query.step).toBe('send'))
    wrapper.unmount()
  })

  it('says that the warnings were acknowledged only when the box was ticked', async () => {
    const { wrapper, api } = await open(withWarning, { [APPROVE]: { body: { campaign: sending, enqueued: {} } } })
    await checkbox(wrapper).setValue(true)
    await approveButton(wrapper).trigger('click')
    await flushPromises()
    expect(api.callsTo(APPROVE)[0]?.body).toEqual({ version: 3, acknowledge_warnings: true })
    wrapper.unmount()
  })

  it('does not send twice while a request runs', async () => {
    let finish: () => void = () => undefined
    const { wrapper, api } = await open(contentReady, {
      [APPROVE]: async () => {
        await new Promise<void>((resolve) => (finish = resolve))
        return { body: { campaign: sending, enqueued: {} } }
      },
    })
    await approveButton(wrapper).trigger('click')
    expect(approveButton(wrapper).text()).toBe('Onaylanıyor…')
    await approveButton(wrapper).trigger('click')
    expect(api.callsTo(APPROVE)).toHaveLength(1)
    finish()
    await flushPromises()
    wrapper.unmount()
  })

  it('reloads and asks for a new look when the campaign changed (version_mismatch), and resets the box', async () => {
    const changed = campaign({ ...contentReady, version: 4, warnings: [{ code: 'matches_everyone', message: 'English' }] })
    const { wrapper, api } = await open(withWarning, { [APPROVE]: error(409, 'version_mismatch', { current_version: 4 }) })
    await checkbox(wrapper).setValue(true)
    api.set('GET /api/campaigns/7', { body: changed })
    await approveButton(wrapper).trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Kampanya değişti, lütfen tekrar gözden geçirin')
    expect(wrapper.get('.summary__version').text()).toContain('4') // the new version is on screen
    expect((checkbox(wrapper).element as HTMLInputElement).checked).toBe(false)
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    expect(api.callsTo(PREVIEW)).toHaveLength(2) // the texts are read again for the new version
    expect(api.callsTo(APPROVE)).toHaveLength(1)
    wrapper.unmount()
  })

  it('then sends the new version when the marketer approves again', async () => {
    const changed = campaign({ ...contentReady, version: 4 })
    const { wrapper, api } = await open(contentReady, { [APPROVE]: error(409, 'version_mismatch', { current_version: 4 }) })
    api.set('GET /api/campaigns/7', { body: changed })
    await approveButton(wrapper).trigger('click')
    await flushPromises()
    api.set(APPROVE, { body: { campaign: { ...sending, version: 5 }, enqueued: {} } })
    await approveButton(wrapper).trigger('click')
    await flushPromises()
    expect(api.callsTo(APPROVE).map((c) => c.body)).toEqual([
      { version: 3, acknowledge_warnings: false },
      { version: 4, acknowledge_warnings: false },
    ])
    wrapper.unmount()
  })

  it('shows the warnings the server wants acknowledged (acknowledgement_required) and reloads', async () => {
    const { wrapper, api } = await open(contentReady, {
      [APPROVE]: error(409, 'acknowledgement_required', { warnings: [{ code: 'matches_everyone', message: 'The rule matches every user' }] }),
    })
    api.set('GET /api/campaigns/7', { body: withWarning })
    await approveButton(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Uyarıları onaylamanız gerekiyor')
    expect(wrapper.get('.server-warnings').text()).toContain('The rule matches every user')
    expect(checkbox(wrapper).exists()).toBe(true) // the reload brought the warning, so the box is there now
    expect(api.callsTo('GET /api/campaigns/7').length).toBeGreaterThanOrEqual(2)
    wrapper.unmount()
  })

  it('reloads when nobody would receive anything (no_recipients), and shows why', async () => {
    const { wrapper, api } = await open(contentReady, { [APPROVE]: error(409, 'no_recipients', { channels: ['sms'] }) })
    api.set('GET /api/campaigns/7', { body: noRecipients })
    await approveButton(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Gönderilecek kimse yok')
    expect(wrapper.find('[data-warning="no_recipients"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('says not to approve again when it is not known whether it worked (approval_unconfirmed), and reloads', async () => {
    const { wrapper, api } = await open(contentReady, { [APPROVE]: error(500, 'approval_unconfirmed') })
    await approveButton(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Tekrar onaylamayın')
    expect(wrapper.get('[role="alert"]').find('button').exists()).toBe(false) // no retry button
    expect(api.callsTo('GET /api/campaigns/7').length).toBeGreaterThanOrEqual(2)
    wrapper.unmount()
  })

  it('shows any other failure and reloads, since the campaign may have changed anyway', async () => {
    const { wrapper, api } = await open(contentReady, { [APPROVE]: () => { throw new TypeError('Failed to fetch') } })
    await approveButton(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Sunucuya ulaşılamıyor')
    expect(api.callsTo('GET /api/campaigns/7').length).toBeGreaterThanOrEqual(2)
    wrapper.unmount()
  })
})

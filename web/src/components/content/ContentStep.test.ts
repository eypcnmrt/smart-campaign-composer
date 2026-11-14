import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '@/App.vue'
import { createAppRouter } from '@/router'
import { error, installFakeApi, openSse, sseFrame, sseResponse } from '@/test/fakeApi'
import { audienceReady, campaign, config, contentReady } from '@/test/fixtures'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const STREAM = 'GET /api/campaigns/7/content/stream'

async function open(path: string, handlers: Parameters<typeof installFakeApi>[0]) {
  const api = installFakeApi({ 'GET /api/config': { body: config }, ...handlers })
  const router = createAppRouter(createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = mount(App, { global: { plugins: [router] }, attachTo: document.body })
  await vi.waitFor(() => expect(wrapper.find('h2').exists()).toBe(true))
  await flushPromises()
  return { wrapper, router, api }
}

type Wrapper = Awaited<ReturnType<typeof open>>['wrapper']
const field = (w: Wrapper, name: string) => w.get(`[data-field="${name}"]`)
const control = (w: Wrapper, name: string) => field(w, name).get('textarea, input').element as HTMLTextAreaElement | HTMLInputElement
const button = (w: Wrapper, label: string) => {
  const found = w.findAll('main button').find((b) => b.text().startsWith(label))
  if (!found) throw new Error(`no button "${label}"`)
  return found
}

/** happy-dom has no window.confirm; this is the browser's native question, answered by the test. */
function stubConfirm(answer: boolean) {
  const confirm = vi.fn(() => answer)
  vi.stubGlobal('confirm', confirm)
  return confirm
}

const attempt = (n: number) => sseFrame('attempt', { attempt: n })
const delta = (f: string, text: string) => sseFrame('delta', { field: f, text })

describe('the content step, before there is content', () => {
  it('asks for the content on a request only: opening the step calls no model', async () => {
    const { wrapper, api } = await open('/campaigns/7?step=content', { 'GET /api/campaigns/7': { body: audienceReady } })
    expect(wrapper.get('h2').text()).toBe('İçerik')
    expect(wrapper.text()).toContain('henüz içerik yok')
    expect(api.callsTo(STREAM)).toHaveLength(0)
    wrapper.unmount()
  })

  it('starts when the marketer presses the button, and shows the text as the model writes it', async () => {
    const stream = openSse()
    const { wrapper } = await open('/campaigns/7?step=content', { 'GET /api/campaigns/7': { body: audienceReady }, [STREAM]: stream.response })
    await button(wrapper, 'İçeriği üret').trigger('click')
    stream.push(attempt(1))
    stream.push(delta('sms', 'Merhaba '))
    stream.push(delta('sms', 'Ankara'))
    await vi.waitFor(() => expect(control(wrapper, 'sms').value).toBe('Merhaba Ankara'))

    expect(wrapper.text()).toContain('Model yazıyor…')
    expect(control(wrapper, 'sms').disabled).toBe(true) // the editor is locked while the model writes
    expect(control(wrapper, 'email_subject').disabled).toBe(true)
    expect(field(wrapper, 'sms').get('.editor__count').text()).toContain('14 / 160') // the counter follows the stream
    expect(wrapper.find('.status').attributes('aria-live')).toBe('polite')
    wrapper.unmount()
  })

  it('shows the saved content and unlocks the editor when the stream is done', async () => {
    const stream = openSse()
    const { wrapper } = await open('/campaigns/7?step=content', { 'GET /api/campaigns/7': { body: audienceReady }, [STREAM]: stream.response })
    await button(wrapper, 'İçeriği üret').trigger('click')
    stream.push(attempt(1))
    stream.push(delta('sms', 'taslak'))
    stream.push(sseFrame('done', contentReady))
    stream.close()
    await vi.waitFor(() => expect(control(wrapper, 'sms').value).toBe(contentReady.content!.sms))
    await flushPromises()
    expect(control(wrapper, 'sms').disabled).toBe(false)
    expect(wrapper.text()).not.toContain('Model yazıyor…')
    expect(button(wrapper, 'Onaya geç').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('says the model is correcting its output and clears the text, when it starts another attempt', async () => {
    const stream = openSse()
    const { wrapper } = await open('/campaigns/7?step=content', { 'GET /api/campaigns/7': { body: audienceReady }, [STREAM]: stream.response })
    await button(wrapper, 'İçeriği üret').trigger('click')
    stream.push(attempt(1))
    stream.push(delta('sms', 'Geçersiz {{x}}'))
    await vi.waitFor(() => expect(control(wrapper, 'sms').value).toBe('Geçersiz {{x}}'))
    stream.push(attempt(2))
    await vi.waitFor(() => expect(wrapper.text()).toContain('Model çıktıyı düzeltiyor (deneme 2)'))
    expect(control(wrapper, 'sms').value).toBe('')
    wrapper.unmount()
  })

  it('shows the raw text when the answer stopped looking like fields', async () => {
    const stream = openSse()
    const { wrapper } = await open('/campaigns/7?step=content', { 'GET /api/campaigns/7': { body: audienceReady }, [STREAM]: stream.response })
    await button(wrapper, 'İçeriği üret').trigger('click')
    stream.push(attempt(1))
    stream.push(sseFrame('raw', { text: '{"sms": [oops' }))
    await vi.waitFor(() => expect(wrapper.find('.raw__text').exists()).toBe(true))
    expect(wrapper.get('.raw__text').text()).toBe('{"sms": [oops')
    wrapper.unmount()
  })

  it('stops the generation on request and leaves the campaign without content', async () => {
    const stream = openSse()
    const { wrapper } = await open('/campaigns/7?step=content', { 'GET /api/campaigns/7': { body: audienceReady }, [STREAM]: stream.response })
    await button(wrapper, 'İçeriği üret').trigger('click')
    stream.push(attempt(1))
    await vi.waitFor(() => expect(wrapper.text()).toContain('Model yazıyor…'))
    await button(wrapper, 'Üretimi durdur').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('henüz içerik yok')
    wrapper.unmount()
  })

  it('cancels the generation when the page is left', async () => {
    let signal: AbortSignal | undefined
    const stream = openSse()
    const { wrapper } = await open('/campaigns/7?step=content', {
      'GET /api/campaigns/7': { body: audienceReady },
      [STREAM]: () => stream.response,
    })
    const real = globalThis.fetch
    vi.stubGlobal('fetch', (input: string, init?: RequestInit) => {
      signal = init?.signal ?? undefined
      return real(input, init)
    })
    await button(wrapper, 'İçeriği üret').trigger('click')
    stream.push(attempt(1))
    await vi.waitFor(() => expect(signal).toBeDefined())
    wrapper.unmount()
    expect(signal?.aborted).toBe(true)
  })

  it('shows a failed generation in the panel with a retry that asks again', async () => {
    const { wrapper, api } = await open('/campaigns/7?step=content', {
      'GET /api/campaigns/7': { body: audienceReady },
      [STREAM]: error(503, 'llm_unavailable'),
    })
    await button(wrapper, 'İçeriği üret').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[role="alert"]').exists()).toBe(true))
    expect(wrapper.get('[role="alert"]').text()).toContain('Model hizmetine şu an ulaşılamıyor')
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(api.callsTo(STREAM)).toHaveLength(2)
    wrapper.unmount()
  })

  it('offers the existing content when it turns out to exist already (content_exists)', async () => {
    const { wrapper, api } = await open('/campaigns/7?step=content', {
      'GET /api/campaigns/7': { body: audienceReady },
      [STREAM]: error(409, 'content_exists'),
    })
    await button(wrapper, 'İçeriği üret').trigger('click')
    await vi.waitFor(() => expect(wrapper.text()).toContain('içeriği zaten var'))
    api.set('GET /api/campaigns/7', { body: contentReady })
    await button(wrapper, 'Mevcut içeriği göster').trigger('click')
    await flushPromises()
    expect(control(wrapper, 'sms').value).toBe(contentReady.content!.sms)
    wrapper.unmount()
  })
})

describe('the content step, entered from the audience step', () => {
  it('starts the generation once when the marketer asked for it there', async () => {
    const stream = openSse()
    const { wrapper, api } = await open('/campaigns/7?step=audience', { 'GET /api/campaigns/7': { body: audienceReady }, [STREAM]: stream.response })
    await button(wrapper, 'İçeriği üret').trigger('click')
    await vi.waitFor(() => expect(api.callsTo(STREAM)).toHaveLength(1))
    stream.push(attempt(1))
    await vi.waitFor(() => expect(wrapper.text()).toContain('Model yazıyor…'))
    wrapper.unmount()
  })

  it('does not start again when the content step is opened later from the rail', async () => {
    const { wrapper, api } = await open('/campaigns/7?step=audience', {
      'GET /api/campaigns/7': { body: audienceReady },
      [STREAM]: error(503, 'llm_busy', null, { 'Retry-After': '5' }),
    })
    await button(wrapper, 'İçeriği üret').trigger('click')
    await vi.waitFor(() => expect(api.callsTo(STREAM)).toHaveLength(1))
    await wrapper.get('a[href="/campaigns/7?step=audience"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.get('h2').text()).toBe('Kitle'))
    await wrapper.get('a[href="/campaigns/7?step=content"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.get('h2').text()).toBe('İçerik'))
    await flushPromises()
    expect(api.callsTo(STREAM)).toHaveLength(1)
    expect(wrapper.text()).toContain('henüz içerik yok')
    wrapper.unmount()
  })
})

describe('the content step, editing', () => {
  const openContent = (extra: Parameters<typeof installFakeApi>[0] = {}, c = contentReady) =>
    open('/campaigns/7?step=content', { 'GET /api/campaigns/7': { body: c }, ...extra })

  it('shows the three texts with their counters, and nothing to save yet', async () => {
    const { wrapper } = await openContent()
    expect(control(wrapper, 'sms').value).toBe(contentReady.content!.sms)
    expect(control(wrapper, 'email_subject').value).toBe(contentReady.content!.email_subject)
    expect(control(wrapper, 'email_body').value).toBe(contentReady.content!.email_body)
    expect(button(wrapper, 'Değişiklikleri kaydet').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('makes "Onaya geç" the primary action while nothing is unsaved, and the save while something is, with the reason of each disabled button', async () => {
    const { wrapper } = await openContent()
    expect(button(wrapper, 'Onaya geç').classes()).toContain('btn--primary')
    expect(button(wrapper, 'Değişiklikleri kaydet').classes()).not.toContain('btn--primary')
    expect(wrapper.get('#content-save-reason').text()).toBe('Kaydedilecek değişiklik yok')
    expect(wrapper.find('#content-approve-reason').exists()).toBe(false)

    await field(wrapper, 'sms').get('textarea').setValue('Yeni SMS {{first_name}}')
    expect(button(wrapper, 'Değişiklikleri kaydet').classes()).toContain('btn--primary')
    expect(button(wrapper, 'Onaya geç').classes()).not.toContain('btn--primary')
    expect(button(wrapper, 'Onaya geç').attributes('disabled')).toBeDefined()
    expect(button(wrapper, 'Onaya geç').attributes('aria-describedby')).toBe('content-approve-reason')
    expect(wrapper.get('#content-approve-reason').text()).toBe('Önce değişiklikleri kaydedin')
    expect(wrapper.find('#content-save-reason').exists()).toBe(false)

    await field(wrapper, 'sms').get('textarea').setValue('Merhaba {{unknown}}')
    expect(wrapper.get('#content-save-reason').text()).toBe('Önce hataları düzeltin')
    expect(button(wrapper, 'Değişiklikleri kaydet').attributes('aria-describedby')).toBe('content-save-reason')
    wrapper.unmount()
  })

  it('saves the edited texts and shows what the server returned', async () => {
    const saved = campaign({
      ...contentReady,
      version: 4,
      content: { ...contentReady.content!, sms: 'Yeni SMS {{first_name}}' },
      content_origin: 'edited',
    })
    const { wrapper, api } = await openContent({ 'PUT /api/campaigns/7/content': { body: saved } })
    await field(wrapper, 'sms').get('textarea').setValue('Yeni SMS {{first_name}}')
    expect(button(wrapper, 'Değişiklikleri kaydet').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('Önce değişiklikleri kaydedin')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(api.callsTo('PUT /api/campaigns/7/content')[0]?.body).toEqual({
      sms: 'Yeni SMS {{first_name}}',
      email_subject: contentReady.content!.email_subject,
      email_body: contentReady.content!.email_body,
    })
    expect(wrapper.text()).not.toContain('Önce değişiklikleri kaydedin')
    expect(wrapper.text()).toContain('İçerik kaydedildi.')
    wrapper.unmount()
  })

  it('does not let a text with an unknown variable be saved, and says so beside the field', async () => {
    const { wrapper, api } = await openContent()
    await field(wrapper, 'sms').get('textarea').setValue('Merhaba {{unknown}}')
    expect(field(wrapper, 'sms').text()).toContain('Bilinmeyen değişken {{unknown}}')
    expect(button(wrapper, 'Değişiklikleri kaydet').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('Önce hataları düzeltin')
    await wrapper.get('form').trigger('submit')
    expect(api.callsTo('PUT /api/campaigns/7/content')).toHaveLength(0)
    wrapper.unmount()
  })

  it('shows what the server refused beside the field, in Turkish, and removes it when the field is edited', async () => {
    const { wrapper } = await openContent({
      'PUT /api/campaigns/7/content': error(422, 'validation_failed', [
        { path: 'email_subject', code: 'brand_new_check', message: 'Server says no' },
      ]),
    })
    await field(wrapper, 'email_subject').get('input').setValue('Yeni konu')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(field(wrapper, 'email_subject').text()).toContain('Server says no') // an unknown code shows the server's message
    expect(field(wrapper, 'email_subject').get('input').attributes('aria-invalid')).toBe('true')

    await field(wrapper, 'email_subject').get('input').setValue('Başka konu')
    expect(field(wrapper, 'email_subject').text()).not.toContain('Server says no')
    wrapper.unmount()
  })

  it('shows another failure of the save in the panel and keeps the edits', async () => {
    const { wrapper } = await openContent({ 'PUT /api/campaigns/7/content': error(500, 'internal', { request_id: 'r1' }) })
    await field(wrapper, 'sms').get('textarea').setValue('Değişti')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Sunucuda bir sorun oluştu')
    expect(control(wrapper, 'sms').value).toBe('Değişti')
    wrapper.unmount()
  })

  it('does not go on to the approval while there are unsaved edits', async () => {
    const { wrapper } = await openContent()
    expect(button(wrapper, 'Onaya geç').attributes('disabled')).toBeUndefined()
    await field(wrapper, 'sms').get('textarea').setValue('Başka')
    expect(button(wrapper, 'Onaya geç').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('goes on to the approval step', async () => {
    const { wrapper, router } = await openContent()
    await button(wrapper, 'Onaya geç').trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.query.step).toBe('approve'))
    wrapper.unmount()
  })

  it('says a closed channel’s text will not be sent', async () => {
    const { wrapper } = await openContent({}, campaign({ ...contentReady, channels: ['sms'] }))
    expect(field(wrapper, 'email_subject').text()).toContain('Bu kanal kapalı')
    expect(field(wrapper, 'sms').text()).not.toContain('Bu kanal kapalı')
    wrapper.unmount()
  })

  it('suggests making the content again when the segment moved on after it was written', async () => {
    const { wrapper } = await openContent({}, campaign({ ...contentReady, segment_changed_after_content: true }))
    expect(wrapper.text()).toContain('Segment değişti; içeriği yeniden üretmek ister misin?')
    wrapper.unmount()
  })

  it('asks before leaving the step with unsaved edits, and stays when the answer is no', async () => {
    const confirm = stubConfirm(false)
    const { wrapper, router } = await openContent()
    await field(wrapper, 'sms').get('textarea').setValue('Başka')
    await wrapper.get('a[href="/campaigns/7?step=audience"]').trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.query.step).toBe('content')
    expect(control(wrapper, 'sms').value).toBe('Başka')
    wrapper.unmount()
  })

  it('leaves when the answer is yes', async () => {
    stubConfirm(true)
    const { wrapper, router } = await openContent()
    await field(wrapper, 'sms').get('textarea').setValue('Başka')
    await wrapper.get('a[href="/campaigns/7?step=audience"]').trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.query.step).toBe('audience'))
    wrapper.unmount()
  })

  it('does not ask when there is nothing unsaved', async () => {
    const confirm = stubConfirm(false)
    const { wrapper, router } = await openContent()
    await wrapper.get('a[href="/campaigns/7?step=audience"]').trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.query.step).toBe('audience'))
    expect(confirm).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

describe('the content step, making the content again', () => {
  const regenerated = campaign({ ...contentReady, version: 5, content: { sms: 'Yeni', email_subject: 'Yeni konu', email_body: 'Yeni gövde' } })
  const openContent = (c = contentReady) =>
    open('/campaigns/7?step=content', {
      'GET /api/campaigns/7': { body: c },
      [STREAM]: () => sseResponse([attempt(1), delta('sms', 'Yeni'), sseFrame('done', regenerated)]),
    })
  const dialog = (w: Wrapper) => w.get('dialog').element as HTMLDialogElement

  it('replaces the model’s own untouched text without asking', async () => {
    const { wrapper, api } = await openContent()
    await button(wrapper, 'İçeriği yeniden üret').trigger('click')
    await flushPromises()
    expect(dialog(wrapper).open).toBe(false)
    expect(api.callsTo(STREAM)[0]?.path).toBe('/api/campaigns/7/content/stream?regenerate=true')
    await vi.waitFor(() => expect(control(wrapper, 'sms').value).toBe('Yeni'))
    wrapper.unmount()
  })

  it('asks first when the text was edited by hand, and does nothing on cancel', async () => {
    const { wrapper, api } = await openContent(campaign({ ...contentReady, content_origin: 'edited' }))
    await button(wrapper, 'İçeriği yeniden üret').trigger('click')
    await flushPromises()
    expect(dialog(wrapper).open).toBe(true)
    expect(wrapper.get('dialog').text()).toContain('Bu metni siz düzenlediniz')
    await wrapper.get('dialog button').trigger('click') // Vazgeç
    await flushPromises()
    expect(dialog(wrapper).open).toBe(false)
    expect(api.callsTo(STREAM)).toHaveLength(0)
    wrapper.unmount()
  })

  it('asks first when there are unsaved edits, and replaces on confirm', async () => {
    const { wrapper, api } = await openContent()
    await field(wrapper, 'sms').get('textarea').setValue('Elle yazdım')
    await button(wrapper, 'İçeriği yeniden üret').trigger('click')
    await flushPromises()
    expect(dialog(wrapper).open).toBe(true)
    expect(wrapper.get('dialog').text()).toContain('Kaydedilmemiş değişiklikleriniz var')
    await wrapper.findAll('dialog button')[1]?.trigger('click') // Yeniden üret
    await flushPromises()
    expect(api.callsTo(STREAM)[0]?.path).toBe('/api/campaigns/7/content/stream?regenerate=true')
    await vi.waitFor(() => expect(control(wrapper, 'sms').value).toBe('Yeni'))
    expect(dialog(wrapper).open).toBe(false)
    wrapper.unmount()
  })
})

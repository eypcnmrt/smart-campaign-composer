import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Campaign } from '@/api/types'
import { error, installFakeApi } from '@/test/fakeApi'
import { audience, audienceReady, campaign, contentReady } from '@/test/fixtures'
import AudienceCard from './AudienceCard.vue'

afterEach(() => {
  vi.unstubAllGlobals()
})

function render(c: Campaign = audienceReady) {
  return mount(AudienceCard, { props: { campaign: c } })
}

const noRecipients = { code: 'no_recipients', message: 'English' }

describe('AudienceCard, what the audience is', () => {
  it('shows the rule in Turkish with the reason the model gave', () => {
    const wrapper = render()
    expect(wrapper.get('h2').text()).toBe('Kitle')
    expect(wrapper.get('.rule').text()).toContain('Şehir: Ankara')
    expect(wrapper.text()).toContain('Ankara’da yaşayan müşteriler')
  })

  it('shows the four counts as numbers in the tabular face', () => {
    const wrapper = render(campaign({ audience: { ...audience, matched: 1234, sms: 800, email: 1200, total_users: 5000 } }))
    const value = (key: string) => wrapper.get(`[data-count="${key}"]`)
    expect(value('matched').text()).toBe('1.234')
    expect(value('sms').text()).toBe('800')
    expect(value('email').text()).toBe('1.200')
    expect(value('total').text()).toBe('5.000')
    expect(value('matched').classes()).toContain('num')
  })

  it('keeps the raw JSON in a closed details element, in the monospace face', () => {
    const wrapper = render()
    const details = wrapper.get('details.raw')
    expect(details.attributes('open')).toBeUndefined()
    expect(details.get('summary').text()).toBe('Ham kural (JSON)')
    const pre = details.get('pre')
    expect(pre.text()).toContain('"field": "city"')
    expect(pre.text()).toContain('"explanation"')
  })

  it('shows a condition it does not know as raw text and says so', () => {
    const wrapper = render(
      campaign({ segment: { ...audienceReady.segment!, conditions: [{ type: 'attribute', field: 'shoe_size', op: 'eq', value: 42 }] } }),
    )
    expect(wrapper.get('.rule code').text()).toContain('shoe_size')
    expect(wrapper.get('.rule').text()).toContain('tanınmayan koşul')
  })

  it('says a rule without a condition means every user, next to the matches_everyone warning', () => {
    const wrapper = render(
      campaign({
        segment: { ...audienceReady.segment!, conditions: [], unsupported_criteria: ['Doğum günü kriteri'] },
        audience: { ...audience, matched: 24, total_users: 24 },
        warnings: [
          { code: 'unsupported_criteria', message: 'English', details: ['Doğum günü kriteri'] },
          { code: 'matches_everyone', message: 'English' },
        ],
      }),
    )
    expect(wrapper.get('.rule').text()).toBe('Koşul yok: tüm kullanıcılar')
    expect(wrapper.find('.rule code').exists()).toBe(false)
    expect(wrapper.find('.rule__unknown').exists()).toBe(false)
    expect(wrapper.find('[data-warning="matches_everyone"]').exists()).toBe(true)
    expect(wrapper.find('[data-warning="unsupported_criteria"]').exists()).toBe(true)
  })

  it('says there is no audience yet when the campaign has no segment', () => {
    const wrapper = render(campaign({ segment: null, audience: null }))
    expect(wrapper.text()).toContain('Kitle henüz yok')
  })
})

describe('AudienceCard, warnings', () => {
  it('says a rule that matches everyone is a warning, with the counts', () => {
    const wrapper = render(
      campaign({
        audience: { ...audience, matched: 24, total_users: 24 },
        warnings: [{ code: 'matches_everyone', message: 'English' }],
      }),
    )
    const note = wrapper.get('[data-warning="matches_everyone"]')
    expect(note.classes()).toContain('note--warning')
    expect(note.text()).toContain('Kural herkesi kapsıyor')
    expect(note.text()).toContain('24 / 24')
    expect(note.text()).toContain('Uyarı:') // the hidden word, so the state is not only colour
  })

  it('lists the parts of the brief that were not understood', () => {
    const wrapper = render(
      campaign({
        warnings: [{ code: 'unsupported_criteria', message: 'English', details: ['doğum günü bu ay', 'VIP müşteri'] }],
      }),
    )
    const items = wrapper.findAll('[data-warning="unsupported_criteria"] li')
    expect(items.map((li) => li.text())).toEqual(['doğum günü bu ay', 'VIP müşteri'])
  })

  it('shows no recipients as a danger that stops the approval, before the other warnings', () => {
    const wrapper = render(
      campaign({
        warnings: [{ code: 'matches_everyone', message: 'English' }, noRecipients],
      }),
    )
    const notes = wrapper.findAll('[data-warning]')
    expect(notes.map((n) => n.attributes('data-warning'))).toEqual(['no_recipients', 'matches_everyone'])
    expect(notes[0]?.classes()).toContain('note--danger')
    expect(notes[0]?.text()).toContain('onaylanamaz')
  })

  it('shows a warning code it does not know with the server’s message, never nothing', () => {
    const wrapper = render(campaign({ warnings: [{ code: 'brand_new', message: 'Something new' }] }))
    expect(wrapper.get('[data-warning="brand_new"]').text()).toContain('Something new')
  })

  it('shows nothing when there are no warnings', () => {
    expect(render().find('[data-warning]').exists()).toBe(false)
  })
})

describe('AudienceCard, channels', () => {
  it('shows both channels with the open ones checked', () => {
    const wrapper = render(campaign({ channels: ['sms'] }))
    const boxes = wrapper.findAll('input[type="checkbox"]')
    expect(boxes.map((b) => (b.element as HTMLInputElement).checked)).toEqual([true, false])
    expect(wrapper.get('fieldset legend').text()).toBe('Kanallar')
  })

  it('saves the new list when a channel is closed, and passes the answer on', async () => {
    const closed = campaign({ channels: ['sms'] })
    const api = installFakeApi({ 'PUT /api/campaigns/7/channels': { body: closed } })
    const wrapper = render()
    await wrapper.findAll('input[type="checkbox"]')[1]?.setValue(false)
    await flushPromises()
    expect(api.callsTo('PUT /api/campaigns/7/channels')[0]?.body).toEqual({ channels: ['sms'] })
    expect(wrapper.emitted('updated')).toEqual([[closed]])
  })

  it('keeps the order sms, email when a channel is opened again', async () => {
    const api = installFakeApi({ 'PUT /api/campaigns/7/channels': { body: audienceReady } })
    const wrapper = render(campaign({ channels: ['email'] }))
    await wrapper.findAll('input[type="checkbox"]')[0]?.setValue(true)
    await flushPromises()
    expect(api.callsTo('PUT /api/campaigns/7/channels')[0]?.body).toEqual({ channels: ['sms', 'email'] })
  })

  it('does not let the last open channel be closed, and says why', async () => {
    const api = installFakeApi({})
    const wrapper = render(campaign({ channels: ['email'] }))
    const [sms, email] = wrapper.findAll('input[type="checkbox"]')
    expect(email?.attributes('disabled')).toBeDefined()
    expect(sms?.attributes('disabled')).toBeUndefined()
    expect(email?.attributes('aria-describedby')).toBe('channels-last')
    expect(wrapper.get('#channels-last').text()).toContain('En az bir kanal')
    await email?.trigger('change')
    expect(api.callsTo('PUT /api/campaigns/7/channels')).toHaveLength(0)
  })

  it('shows a refusal of the server in the panel and leaves the campaign as it was', async () => {
    installFakeApi({ 'PUT /api/campaigns/7/channels': error(422, 'validation_failed', [{ path: 'channels', code: 'empty', message: 'English' }]) })
    const wrapper = render()
    await wrapper.findAll('input[type="checkbox"]')[1]?.setValue(false)
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('En az bir kanal seçilmeli.')
    expect(wrapper.emitted('updated')).toBeUndefined()
  })

  it('locks the controls while a change is being saved', async () => {
    let release: (() => void) | undefined
    installFakeApi({
      'PUT /api/campaigns/7/channels': () =>
        new Promise((resolve) => {
          release = () => resolve({ body: audienceReady })
        }),
    })
    const wrapper = render()
    await wrapper.findAll('input[type="checkbox"]')[1]?.setValue(false)
    expect(wrapper.findAll('input[type="checkbox"]').every((b) => b.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.get('[role="status"]').text()).toBe('Kanallar kaydediliyor.')
    release?.()
    await flushPromises()
    expect(wrapper.get('[role="status"]').text()).toBe('Kanallar kaydedildi.')
    expect(wrapper.find('input[type="checkbox"]:disabled').exists()).toBe(false)
  })
})

describe('AudienceCard, new segment and content', () => {
  it('asks for a new segment and passes it on', async () => {
    const next = campaign({ version: 5 })
    const api = installFakeApi({ 'POST /api/campaigns/7/segment': { body: next } })
    const wrapper = render()
    await wrapper.findAll('button').find((b) => b.text() === 'Segmenti yeniden üret')?.trigger('click')
    await flushPromises()
    expect(api.callsTo('POST /api/campaigns/7/segment')).toHaveLength(1)
    expect(wrapper.emitted('updated')).toEqual([[next]])
  })

  it('shows a failed new segment in the panel', async () => {
    installFakeApi({ 'POST /api/campaigns/7/segment': error(504, 'llm_timeout') })
    const wrapper = render()
    await wrapper.findAll('button').find((b) => b.text() === 'Segmenti yeniden üret')?.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Model zamanında yanıt vermedi')
  })

  it('does not start two requests at once', async () => {
    const api = installFakeApi({ 'POST /api/campaigns/7/segment': () => new Promise(() => undefined) })
    const wrapper = render()
    const button = wrapper.findAll('button').find((b) => b.text() === 'Segmenti yeniden üret')
    await button?.trigger('click')
    await button?.trigger('click')
    expect(api.callsTo('POST /api/campaigns/7/segment')).toHaveLength(1)
    expect(wrapper.text()).toContain('Segment üretiliyor…')
  })

  it('does not report an answer that arrives after the card is gone', async () => {
    let release: (() => void) | undefined
    installFakeApi({
      'POST /api/campaigns/7/segment': () =>
        new Promise((resolve) => {
          release = () => resolve({ body: audienceReady })
        }),
    })
    const wrapper = render()
    await wrapper.findAll('button').find((b) => b.text() === 'Segmenti yeniden üret')?.trigger('click')
    wrapper.unmount()
    release?.()
    await flushPromises()
    expect(wrapper.emitted('updated')).toBeUndefined()
  })

  it('starts the content step only when asked: the button is the way in, nothing is generated by itself', async () => {
    const api = installFakeApi({})
    const wrapper = render()
    expect(api.calls).toHaveLength(0)
    const button = wrapper.get('.btn--primary')
    expect(button.text()).toBe('İçeriği üret')
    await button.trigger('click')
    expect(wrapper.emitted('content')).toHaveLength(1)
  })

  it('offers to go on to the content that already exists, instead of generating', () => {
    expect(render(contentReady).get('.btn--primary').text()).toBe('İçeriğe geç')
  })

  it('does not offer to generate content nobody could receive, and says why', () => {
    const wrapper = render(campaign({ warnings: [noRecipients] }))
    expect(wrapper.get('.btn--primary').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('Kimse mesaj alamayacağı için')
  })

  it('suggests making the content again when the segment moved on after it was written', () => {
    const moved = render(campaign({ ...contentReady, segment_changed_after_content: true }))
    expect(moved.text()).toContain('Segment değişti; içeriği yeniden üretmek ister misin?')
    expect(render(contentReady).text()).not.toContain('Segment değişti')
  })
})

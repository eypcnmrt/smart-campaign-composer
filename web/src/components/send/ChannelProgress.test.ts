import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { ChannelCounts, ProviderInfo, Stats } from '@/api/types'
import ChannelProgress from './ChannelProgress.vue'
import ProviderBadge from './ProviderBadge.vue'
import RateMeter from './RateMeter.vue'
import ResultBanner from './ResultBanner.vue'

const ok: ProviderInfo = { state: 'ok', circuit: 'closed', last_error: '', updated_at: null, age_seconds: null }
const counts = (c: Partial<ChannelCounts> = {}): ChannelCounts => ({
  queued: 0,
  sending: 0,
  retrying: 0,
  sent: 0,
  failed: 0,
  skipped: 0,
  total: 0,
  provider: ok,
  ...c,
})

describe('ChannelProgress', () => {
  const render = (c: Partial<ChannelCounts>, rate: number | null = null) =>
    mount(ChannelProgress, { props: { channel: 'sms', label: 'SMS', counts: counts(c), rate } })

  it('shows the progress as (sent + failed + skipped) / total, in words and as a progress bar', () => {
    const wrapper = render({ sent: 600, failed: 100, skipped: 50, queued: 250, total: 1000 })
    const bar = wrapper.get('[role="progressbar"]')
    expect(bar.attributes('aria-valuenow')).toBe('75')
    expect(bar.attributes('aria-valuemin')).toBe('0')
    expect(bar.attributes('aria-valuemax')).toBe('100')
    expect(bar.attributes('aria-label')).toBe('SMS gönderim ilerlemesi')
    expect(bar.attributes('aria-valuetext')).toBe('75%: 750 / 1.000')
    expect(wrapper.get('.progress__text').text()).toContain('%75')
    expect(wrapper.get('.progress__text').text()).toContain('750 / 1.000')
  })

  it('draws the bar with attributes, not an inline style (the page’s content security policy forbids style attributes)', () => {
    const wrapper = render({ sent: 50, queued: 50, total: 100 })
    expect(wrapper.get('.progress__done').attributes('width')).toBe('50')
    expect(wrapper.html()).not.toMatch(/\sstyle=/)
  })

  it('shows all six counts with their names, as numbers in the tabular face', () => {
    const wrapper = render({ queued: 1, sending: 2, retrying: 3, sent: 4, failed: 5, skipped: 6, total: 21 })
    const labels = wrapper.findAll('.count dt').map((n) => n.text())
    expect(labels).toEqual(['Sırada', 'Gönderiliyor', 'Yeniden denenecek', 'Gönderildi', 'Başarısız', 'Atlandı'])
    const values = wrapper.findAll('.count dd').map((n) => n.text())
    expect(values).toEqual(['1', '2', '3', '4', '5', '6'])
    expect(wrapper.findAll('.count dd').every((n) => n.classes().includes('num'))).toBe(true)
  })

  it('formats large counts the Turkish way', () => {
    const wrapper = render({ sent: 3412, queued: 1588, total: 5000 })
    expect(wrapper.get('[data-count="sent"]').text()).toBe('3.412')
  })

  it('marks failures with a tint only when there are some', () => {
    expect(render({ failed: 0, total: 1 }).find('.count--failed').exists()).toBe(false)
    expect(render({ failed: 2, total: 5 }).find('.count--failed').exists()).toBe(true)
  })

  it('says a channel has nobody to send to, and calls it complete', () => {
    const wrapper = render({ total: 0 })
    expect(wrapper.text()).toContain('Bu kanalda alıcı yok')
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('100')
  })

  it('has its own rate meter and provider state', () => {
    const wrapper = render({ sent: 1, total: 10 }, 42)
    expect(wrapper.get('.meter__value').text()).toBe('42')
    expect(wrapper.text()).toContain('SMS provider’ı çalışıyor')
  })
})

describe('RateMeter', () => {
  const render = (rate: number | null) => mount(RateMeter, { props: { rate, label: 'SMS' } })

  it('labels the figure as the messages the provider accepted per second', () => {
    expect(render(87.4).get('.meter__figure').text()).toBe('87 kabul edilen mesaj/s')
  })

  it('says it is measuring while there is not enough data, not zero', () => {
    const wrapper = render(null)
    expect(wrapper.get('.meter__value').text()).toBe('…')
    expect(wrapper.text()).toContain('ölçülüyor')
    expect(wrapper.get('.meter__fill').attributes('width')).toBe('0')
  })

  it('draws the limit of the channel at 100 on a scale that runs a little past it', () => {
    const wrapper = render(50)
    expect(wrapper.get('.meter__limit').attributes('x1')).toBe('100')
    expect(wrapper.get('.meter__bar').attributes('viewBox')).toBe('0 0 120 10')
    expect(wrapper.get('.meter__fill').attributes('width')).toBe('50')
    expect(wrapper.get('.meter__caption').text()).toBe('100/s: kanal başına sınır')
  })

  it('keeps a rate over the limit inside the bar and marks it', () => {
    const wrapper = render(500)
    expect(wrapper.get('.meter__fill').attributes('width')).toBe('120')
    expect(wrapper.get('.meter__fill').classes()).toContain('meter__fill--over')
  })

  it('describes itself for screen readers with the figure and the limit', () => {
    expect(render(30).get('svg').attributes('aria-label')).toBe('SMS hızı: 30 mesaj/s; sınır 100 mesaj/s')
    expect(render(null).get('svg').attributes('aria-label')).toBe('SMS hızı: ölçülüyor; sınır 100 mesaj/s')
  })
})

describe('ProviderBadge', () => {
  const render = (provider: Partial<ProviderInfo>, label = 'SMS') =>
    mount(ProviderBadge, { props: { label, provider: { ...ok, ...provider } } })

  it('says the provider works, with a badge', () => {
    const wrapper = render({ state: 'ok' })
    expect(wrapper.text()).toBe('SMS provider’ı çalışıyor')
    expect(wrapper.classes()).toContain('badge--success')
  })

  it('says the provider cannot be reached and that the messages wait, as a warning', () => {
    const wrapper = render({ state: 'provider_unreachable', circuit: 'open', last_error: 'dial tcp: connection refused' })
    expect(wrapper.text()).toContain('SMS provider’ına ulaşılamıyor, mesajlar bekletiliyor')
    expect(wrapper.classes()).toContain('note--warning')
    expect(wrapper.attributes('role')).toBe('status')
    expect(wrapper.text()).toContain('Hiçbir mesaj kaybolmaz')
  })

  it('keeps the provider’s English error in a closed technical panel', () => {
    const wrapper = render({ state: 'provider_unreachable', last_error: 'dial tcp: connection refused' })
    const details = wrapper.get('details')
    expect(details.attributes('open')).toBeUndefined()
    expect(details.get('p').attributes('lang')).toBe('en')
    expect(details.text()).toContain('dial tcp: connection refused')
    expect(render({ state: 'provider_unreachable' }).find('details').exists()).toBe(false)
  })

  it('names the channel it is about', () => {
    expect(render({ state: 'provider_unreachable' }, 'E-posta').text()).toContain('E-posta provider’ına ulaşılamıyor')
  })

  it('says nobody is serving the channel when the worker is down, as a danger', () => {
    const wrapper = render({ state: 'worker_down' })
    expect(wrapper.text()).toContain('SMS gönderim işçisi çalışmıyor')
    expect(wrapper.classes()).toContain('note--danger')
    expect(wrapper.attributes('role')).toBe('alert')
  })
})

describe('ResultBanner', () => {
  const stats = (status: Stats['status'], sms: Partial<ChannelCounts>, email: Partial<ChannelCounts> = {}): Stats => ({
    campaign_id: 7,
    status,
    channels: { sms: counts(sms), email: counts(email) },
  })

  it('says a clean delivery is complete, as a success, with the total', () => {
    const wrapper = mount(ResultBanner, { props: { stats: stats('completed', { sent: 800, total: 800 }, { sent: 1200, total: 1200 }) } })
    expect(wrapper.classes()).toContain('note--success')
    expect(wrapper.text()).toContain('Gönderim tamamlandı')
    expect(wrapper.text()).toContain('2.000 mesaj gönderildi')
  })

  it('does not call a delivery with failures a success, and says how many failed', () => {
    const wrapper = mount(ResultBanner, {
      props: { stats: stats('completed_with_failures', { sent: 790, failed: 10, total: 800 }, { sent: 1195, failed: 3, skipped: 2, total: 1200 }) },
    })
    expect(wrapper.classes()).toContain('note--warning')
    expect(wrapper.classes()).not.toContain('note--success')
    expect(wrapper.text()).toContain('Gönderim hatalarla tamamlandı')
    expect(wrapper.text()).toContain('1.985 mesaj gönderildi')
    expect(wrapper.text()).toContain('13 mesaj gönderilemedi')
    expect(wrapper.text()).toContain('2 mesaj, alıcı izni geri çektiği için atlandı')
  })

  it('shows nothing while the delivery runs', () => {
    expect(mount(ResultBanner, { props: { stats: stats('sending', { sent: 1, total: 5 }) } }).html()).toBe('<!--v-if-->')
  })
})

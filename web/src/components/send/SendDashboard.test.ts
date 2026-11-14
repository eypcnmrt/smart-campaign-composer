import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ChannelCounts, Stats } from '@/api/types'
import { error, installFakeApi } from '@/test/fakeApi'
import { sending } from '@/test/fixtures'
import SendDashboard from './SendDashboard.vue'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

const KEY = 'GET /api/campaigns/7/stats'
const provider = { state: 'ok' as const, circuit: 'closed', last_error: '', updated_at: null, age_seconds: null }
const ch = (c: Partial<ChannelCounts>): ChannelCounts => ({ queued: 0, sending: 0, retrying: 0, sent: 0, failed: 0, skipped: 0, total: 0, provider, rate_limit: 100, ...c })
const stats = (status: Stats['status'], sms: Partial<ChannelCounts>, email: Partial<ChannelCounts> = {}): Stats => ({
  campaign_id: 7,
  status,
  channels: { email: ch(email), sms: ch(sms) }, // the order of the server's map is not the order of the screen
})

const settle = () => vi.advanceTimersByTimeAsync(0)

function render(handlers: Parameters<typeof installFakeApi>[0]) {
  const api = installFakeApi(handlers)
  const wrapper = mount(SendDashboard, { props: { campaign: sending }, attachTo: document.body })
  return { wrapper, api }
}

describe('SendDashboard', () => {
  it('says it is reading the numbers, then shows each channel with SMS first', async () => {
    const { wrapper } = render({ [KEY]: { body: stats('sending', { sent: 100, queued: 900, total: 1000 }, { sent: 10, queued: 90, total: 100 }) } })
    expect(wrapper.get('[role="status"]').text()).toContain('Gönderim durumu okunuyor')
    await settle()
    expect(wrapper.findAll('.channel h3').map((h) => h.text())).toEqual(['SMS', 'E-posta'])
    expect(wrapper.get('[data-channel="sms"] [role="progressbar"]').attributes('aria-valuenow')).toBe('10')
    expect(wrapper.get('[data-channel="email"] [role="progressbar"]').attributes('aria-valuenow')).toBe('10')
    expect(wrapper.get('h2').text()).toBe('Gönderim')
    expect(wrapper.get('.badge').text()).toBe('Gönderiliyor')
    wrapper.unmount()
  })

  it('refreshes the numbers every second while sending', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('sending', { sent: 100, queued: 900, total: 1000 }) } })
    const wrapper = mount(SendDashboard, { props: { campaign: sending }, attachTo: document.body })
    await settle()
    api.set(KEY, { body: stats('sending', { sent: 400, queued: 600, total: 1000 }) })
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('[data-channel="sms"] [data-count="sent"]').text()).toBe('400')
    expect(wrapper.get('[data-channel="sms"] [role="progressbar"]').attributes('aria-valuenow')).toBe('40')
    wrapper.unmount()
  })

  it('shows the rate of each channel once there is a window to measure, against the channel’s own limit', async () => {
    let n = 0
    const { wrapper } = render({
      [KEY]: () => {
        n += 1
        return { body: stats('sending', { sent: n * 100, total: 10_000 }, { sent: n * 20, total: 10_000 }) }
      },
    })
    await settle()
    expect(wrapper.get('[data-channel="sms"] .meter__value').text()).toBe('…')
    await vi.advanceTimersByTimeAsync(3000)
    expect(wrapper.get('[data-channel="sms"] .meter__value').text()).toBe('100')
    expect(wrapper.get('[data-channel="email"] .meter__value').text()).toBe('20')
    expect(wrapper.findAll('.meter__limit')).toHaveLength(2) // one line per channel, none on a total
    wrapper.unmount()
  })

  it('draws the limit each channel reported, not a number of its own', async () => {
    const { wrapper } = render({ [KEY]: { body: stats('sending', { sent: 10, total: 100, rate_limit: 50 }, { sent: 10, total: 100, rate_limit: null }) } })
    await settle()
    expect(wrapper.get('[data-channel="sms"] .meter__caption').text()).toBe('50/s: kanal başına sınır')
    expect(wrapper.find('[data-channel="email"] .meter__limit').exists()).toBe(false)
    wrapper.unmount()
  })

  it('draws the per-second chart while sending, keeps it after the end and adds the summary line under the result', async () => {
    const timeline = { start: '2026-11-14T12:00:00Z', bucket_seconds: 1, accepted: [0, 60, 100] }
    const live = { ...stats('sending', { sent: 160, queued: 40, total: 200, timeline }), summary: null }
    const api = installFakeApi({ [KEY]: { body: live } })
    const wrapper = mount(SendDashboard, { props: { campaign: sending }, attachTo: document.body })
    await settle()
    expect(wrapper.findAll('.chart')).toHaveLength(1) // the e-mail channel has no timeline
    expect(wrapper.find('[data-testid="send-summary"]').exists()).toBe(false)

    const done = {
      ...stats('completed', { sent: 200, total: 200, timeline: { ...timeline, accepted: [0, 60, 100, 40] } }),
      summary: { duration_seconds: 3.2, average_per_second: 66.7, peak_per_second: 100, retried_messages: 3, total_attempts: 203 },
    }
    api.set(KEY, { body: done })
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.findAll('.chart__bar')).toHaveLength(4)
    expect(wrapper.find('.meter').exists()).toBe(false) // the live figure goes, the chart stays
    const text = wrapper.get('[data-testid="send-summary"]').text()
    expect(text).toContain('Süre 3,2 sn')
    expect(text).toContain('3 mesaj en az bir kez yeniden denendi (toplam 203 deneme)')
    wrapper.unmount()
  })

  it('shows each channel’s provider state', async () => {
    const down = { ...ch({ queued: 10, total: 10 }), provider: { ...provider, state: 'provider_unreachable' as const, last_error: 'timeout' } }
    const { wrapper } = render({ [KEY]: { body: { campaign_id: 7, status: 'sending', channels: { sms: down, email: ch({ queued: 5, total: 5 }) } } } })
    await settle()
    expect(wrapper.get('[data-channel="sms"]').text()).toContain('SMS provider’ına ulaşılamıyor, mesajlar bekletiliyor')
    expect(wrapper.get('[data-channel="email"]').text()).toContain('E-posta provider’ı çalışıyor')
    wrapper.unmount()
  })

  it('says it finished and stops asking, when the campaign is complete', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('sending', { sent: 90, queued: 10, total: 100 }) } })
    const wrapper = mount(SendDashboard, { props: { campaign: sending }, attachTo: document.body })
    await settle()
    expect(wrapper.find('.note--success').exists()).toBe(false)
    api.set(KEY, { body: stats('completed', { sent: 100, total: 100 }) })
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('.note--success').text()).toContain('Gönderim tamamlandı')
    expect(wrapper.get('.badge').text()).toBe('Tamamlandı')
    expect(wrapper.text()).toContain('Gönderim bitti; sayılar kesindir.')
    expect(wrapper.find('.meter').exists()).toBe(false) // the rate of a finished delivery is not shown
    const calls = api.callsTo(KEY).length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(api.callsTo(KEY)).toHaveLength(calls)
    wrapper.unmount()
  })

  it('tells the page to read the campaign again when its status moved on', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('sending', { sent: 90, queued: 10, total: 100 }) } })
    const wrapper = mount(SendDashboard, { props: { campaign: sending }, attachTo: document.body })
    await settle()
    expect(wrapper.emitted('reload')).toBeUndefined()
    api.set(KEY, { body: stats('completed', { sent: 100, total: 100 }) })
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.emitted('reload')).toHaveLength(1)
    wrapper.unmount()
  })

  it('does not call a delivery with failures a success', async () => {
    const { wrapper } = render({ [KEY]: { body: stats('completed_with_failures', { sent: 90, failed: 10, total: 100 }) } })
    await settle()
    expect(wrapper.find('.note--success').exists()).toBe(false)
    expect(wrapper.get('.note--warning').text()).toContain('10 mesaj gönderilemedi')
    expect(wrapper.get('.badge').text()).toBe('Hatalarla tamamlandı')
    wrapper.unmount()
  })

  it('shows a failed request in a line, keeps the last numbers and keeps asking', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('sending', { sent: 100, queued: 900, total: 1000 }) } })
    const wrapper = mount(SendDashboard, { props: { campaign: sending }, attachTo: document.body })
    await settle()
    api.set(KEY, error(503, 'not_ready'))
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('[role="status"].note--warning').text()).toContain('Güncel durum alınamıyor')
    expect(wrapper.get('[data-channel="sms"] [data-count="sent"]').text()).toBe('100')

    api.set(KEY, { body: stats('sending', { sent: 250, queued: 750, total: 1000 }) })
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.find('.note--warning').exists()).toBe(false)
    expect(wrapper.get('[data-channel="sms"] [data-count="sent"]').text()).toBe('250')
    wrapper.unmount()
  })

  it('shows a failure of the very first request as a panel with a retry', async () => {
    const api = installFakeApi({ [KEY]: error(500, 'internal', { request_id: 'r1' }) })
    const wrapper = mount(SendDashboard, { props: { campaign: sending }, attachTo: document.body })
    await settle()
    expect(wrapper.get('[role="alert"]').text()).toContain('Sunucuda bir sorun oluştu')
    api.set(KEY, { body: stats('sending', { queued: 5, total: 5 }) })
    await wrapper.get('[role="alert"] button').trigger('click')
    await settle()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('[data-channel="sms"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('stops asking when it is left', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('sending', { queued: 5, total: 5 }) } })
    const wrapper = mount(SendDashboard, { props: { campaign: sending }, attachTo: document.body })
    await settle()
    wrapper.unmount()
    const calls = api.callsTo(KEY).length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(api.callsTo(KEY)).toHaveLength(calls)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does nothing visible but the loading line before the first answer', async () => {
    const { wrapper } = render({ [KEY]: () => new Promise(() => undefined) })
    await flushPromises()
    expect(wrapper.find('[data-channel]').exists()).toBe(false)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })
})

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { SendSummary } from '@/api/types'
import RateChart from './RateChart.vue'
import SummaryLine from './SummaryLine.vue'

describe('RateChart', () => {
  const timeline = (accepted: number[], bucket_seconds = 1) => ({ start: '2026-11-14T12:00:00Z', bucket_seconds, accepted })
  const render = (accepted: number[], limit: number | null = 100, bucket_seconds = 1) =>
    mount(RateChart, { props: { timeline: timeline(accepted, bucket_seconds), limit, label: 'SMS' } })

  it('draws one bar per second, scaled so the limit line and the highest bar both fit', () => {
    const wrapper = render([0, 40, 100, 100, 20])
    const bars = wrapper.findAll('.chart__bar')
    expect(bars).toHaveLength(5)
    const heights = bars.map((b) => Number(b.attributes('height')))
    expect(heights[0]).toBe(0)
    expect(heights[2]).toBeCloseTo(heights[3] ?? 0)
    expect(heights[1]).toBeCloseTo((heights[2] ?? 0) * 0.4)
    // the limit line is at the height of a bar of 100 and inside the drawing
    const line = wrapper.get('.chart__limit')
    expect(Number(line.attributes('y1'))).toBeCloseTo(60 - (heights[2] ?? 0))
    expect(Number(line.attributes('y1'))).toBeGreaterThan(0)
    // the page's content security policy forbids style attributes: everything is drawn with attributes
    expect(wrapper.html()).not.toMatch(/\sstyle=/)
  })

  it('keeps a few bars narrow, and counts a wider bucket as messages per second', () => {
    const wide = render([200, 100], 100, 2) // two-second buckets: 100/s and 50/s
    const [a, b] = wide.findAll('.chart__bar').map((x) => Number(x.attributes('height')))
    expect(b).toBeCloseTo((a ?? 0) / 2)
    expect(Number(wide.findAll('.chart__bar')[0]?.attributes('width'))).toBeLessThan(240 / 2)
  })

  it('marks a bar above the limit and draws no line without one', () => {
    const over = render([150, 50])
    expect(over.findAll('.chart__bar')[0]?.classes()).toContain('chart__bar--over')
    expect(over.findAll('.chart__bar')[1]?.classes()).not.toContain('chart__bar--over')
    const unknown = render([50, 50], null)
    expect(unknown.find('.chart__limit').exists()).toBe(false)
    expect(unknown.text()).not.toContain('Sınır')
  })

  it('has a text summary for a screen reader and the figures in words next to it', () => {
    const wrapper = render([0, 40, 100, 100, 20])
    expect(wrapper.get('svg').attributes('role')).toBe('img')
    expect(wrapper.get('svg').attributes('aria-label')).toBe(
      'SMS: 5 saniyede 260 mesaj kabul edildi; en yoğun saniyede 100 mesaj. Sınır: saniyede 100 mesaj.',
    )
    expect(wrapper.get('.chart__caption').text()).toContain('Sınır: 100/s')
    expect(wrapper.get('.chart__caption').text()).toContain('En yoğun: 100/s')
    expect(wrapper.get('.chart__axis').text()).toContain('5 sn')
  })

  it('says nothing was accepted yet instead of drawing an empty scale', () => {
    expect(render([0, 0]).get('svg').attributes('aria-label')).toContain('henüz kabul edilen mesaj yok')
  })
})

describe('SummaryLine', () => {
  const render = (s: Partial<SendSummary> = {}) =>
    mount(SummaryLine, {
      props: { summary: { duration_seconds: 4.8, average_per_second: 98.4, peak_per_second: 100, retried_messages: 12, total_attempts: 5012, ...s } },
    })

  it('says how long it took, how fast it went and what was tried again', () => {
    expect(render().text()).toBe(
      'Süre 4,8 sn · ortalama 98,4 mesaj/sn · en yüksek 100 mesaj/sn · 12 mesaj en az bir kez yeniden denendi (toplam 5.012 deneme)',
    )
  })

  it('says plainly that nothing had to be retried, and leaves the duration out while it is unknown', () => {
    const wrapper = render({ retried_messages: 0, total_attempts: 40, duration_seconds: null })
    expect(wrapper.text()).toContain('Yeniden deneme gerekmedi (toplam 40 deneme)')
    expect(wrapper.text()).not.toContain('Süre')
  })
})

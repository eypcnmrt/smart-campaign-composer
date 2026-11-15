import { describe, expect, it } from 'vitest'
import { chartOf, chartText, createRateWindow, isInFlight, pendingOf, progressOf, STATUS_LABELS } from './delivery'

const counts = (c: Partial<Record<'queued' | 'sending' | 'retrying' | 'sent' | 'failed' | 'skipped' | 'total', number>>) => ({
  queued: 0,
  sending: 0,
  retrying: 0,
  sent: 0,
  failed: 0,
  skipped: 0,
  total: 0,
  ...c,
})

describe('progressOf', () => {
  it('counts sent, failed and skipped as settled', () => {
    expect(progressOf(counts({ sent: 50, failed: 10, skipped: 15, total: 100 }))).toEqual({ done: 75, total: 100, percent: 75 })
  })

  it('does not count what is still queued, being sent or waiting for a retry', () => {
    expect(progressOf(counts({ sent: 10, queued: 80, sending: 5, retrying: 5, total: 100 })).percent).toBe(10)
  })

  it('rounds down, so 100 means everything and not 99.6 percent', () => {
    expect(progressOf(counts({ sent: 996, queued: 4, total: 1000 })).percent).toBe(99)
    expect(progressOf(counts({ sent: 1000, total: 1000 })).percent).toBe(100)
  })

  it('treats a channel with nobody to send to as complete, not as zero percent', () => {
    expect(progressOf(counts({}))).toEqual({ done: 0, total: 0, percent: 100 })
  })

  it('never goes over 100', () => {
    expect(progressOf(counts({ sent: 12, total: 10 })).percent).toBe(100)
  })
})

describe('pendingOf', () => {
  it('adds the three states that are not settled', () => {
    expect(pendingOf(counts({ queued: 5, sending: 3, retrying: 2, sent: 90 }))).toBe(10)
  })
})

describe('createRateWindow', () => {
  it('says nothing before there is enough of a window', () => {
    const w = createRateWindow()
    expect(w.rate()).toBeNull()
    w.push(0, 0)
    expect(w.rate()).toBeNull() // one measurement
    w.push(1000, 100)
    expect(w.rate()).toBeNull() // one second is too little to call it a rate
  })

  it('is the change over the window in messages per second', () => {
    const w = createRateWindow()
    for (let i = 0; i <= 4; i++) w.push(i * 1000, i * 100)
    expect(w.rate()).toBeCloseTo(100, 5)
  })

  it('uses the clock of the measurements, not their number: uneven polls give the same rate', () => {
    const w = createRateWindow()
    w.push(0, 0)
    w.push(700, 70)
    w.push(2500, 250)
    w.push(3000, 300)
    expect(w.rate()).toBeCloseTo(100, 5)
  })

  it('is moved only by its share when one poll shows a burst', () => {
    const w = createRateWindow()
    for (let i = 0; i <= 4; i++) w.push(i * 1000, i * 50) // 50 per second
    w.push(5000, 4 * 50 + 500) // one poll in which 500 were sent: the last second alone would say 500 per second
    const rate = w.rate()
    expect(rate).not.toBeNull()
    expect(rate!).toBeLessThan(200) // the window dilutes it: (700 - 50) / 4 s = 162.5
    expect(rate!).toBeGreaterThan(50)
  })

  it('forgets measurements older than the window', () => {
    const w = createRateWindow(5000, 2000)
    w.push(0, 0)
    w.push(1000, 1000) // fast at first: 1000 per second
    for (let t = 2000; t <= 12000; t += 1000) w.push(t, 1000 + (t - 1000) / 10) // then 100 per second
    expect(w.rate()).toBeCloseTo(100, 5)
  })

  it('shows zero, not nothing, when a channel has stopped', () => {
    const w = createRateWindow()
    for (let i = 0; i <= 4; i++) w.push(i * 1000, 300)
    expect(w.rate()).toBe(0)
  })

  it('starts again when the count goes down', () => {
    const w = createRateWindow()
    for (let i = 0; i <= 4; i++) w.push(i * 1000, i * 100)
    w.push(5000, 5)
    expect(w.rate()).toBeNull()
  })

  it('can be reset', () => {
    const w = createRateWindow()
    for (let i = 0; i <= 4; i++) w.push(i * 1000, i * 100)
    w.reset()
    expect(w.rate()).toBeNull()
  })
})

describe('statuses', () => {
  it('has a Turkish label and a tone for every status', () => {
    for (const [status, { label }] of Object.entries(STATUS_LABELS)) expect(label, status).not.toBe('')
    expect(Object.keys(STATUS_LABELS)).toHaveLength(7)
  })

  it('is in flight only from the approval until the last delivery is settled', () => {
    expect(['draft', 'audience_ready', 'content_ready', 'completed', 'completed_with_failures'].map((s) => isInFlight(s as never))).toEqual([false, false, false, false, false])
    expect(isInFlight('approved')).toBe(true)
    expect(isInFlight('sending')).toBe(true)
  })
})

describe('chartOf', () => {
  it('turns buckets into rates per second and scales above the limit and the highest bar', () => {
    const chart = chartOf({ bucket_seconds: 2, accepted: [200, 100, 0] }, 100)
    expect(chart.bars.map((b) => b.rate)).toEqual([100, 50, 0])
    expect(chart.peak).toBe(100)
    // no figures from the server (an older one): none is made up
    expect(chart.firstAttemptRate).toBeNull()
    expect(chart.duration).toBeNull()
    const sent = chartOf({ bucket_seconds: 1, accepted: [10], first_attempt_per_second: 8, duration_seconds: 20 }, 100)
    expect([sent.firstAttemptRate, sent.duration]).toEqual([8, 20])
    // nothing sent: nothing to say about its speed either
    const none = chartOf({ bucket_seconds: 1, accepted: [0], first_attempt_per_second: 0, duration_seconds: 0 }, 100)
    expect([none.firstAttemptRate, none.duration]).toEqual([null, null])
    expect(chart.total).toBe(300)
    expect(chart.seconds).toBe(6)
    expect(chart.max).toBeCloseTo(115)
    expect(chartOf({ bucket_seconds: 1, accepted: [300] }, 100).max).toBeCloseTo(345) // a bar over the limit still fits
  })

  it('keeps at least twelve slots, and never a zero scale', () => {
    expect(chartOf({ bucket_seconds: 1, accepted: [1, 2] }, null).slots).toBe(12)
    expect(chartOf({ bucket_seconds: 1, accepted: new Array(30).fill(1) }, null).slots).toBe(30)
    expect(chartOf({ bucket_seconds: 1, accepted: [0] }, null).max).toBeGreaterThan(0)
  })

  it('describes itself in words, with and without a limit', () => {
    expect(chartText('SMS', chartOf({ bucket_seconds: 1, accepted: [50, 100] }, 100))).toBe(
      'SMS: 2 saniyede 150 mesaj kabul edildi; en yoğun saniyede 100 mesaj. Sınır: saniyede 100 mesaj.',
    )
    expect(chartText('SMS', chartOf({ bucket_seconds: 1, accepted: [0] }, null))).toBe('SMS: henüz kabul edilen mesaj yok.')
  })
})

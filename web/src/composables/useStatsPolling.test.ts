import { effectScope, nextTick, shallowRef, type EffectScope } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ChannelCounts, Stats } from '@/api/types'
import { error, installFakeApi } from '@/test/fakeApi'
import { useStatsPolling } from './useStatsPolling'

let scope: EffectScope

beforeEach(() => {
  vi.useFakeTimers()
  scope = effectScope()
})

afterEach(() => {
  scope.stop()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

const provider = { state: 'ok' as const, circuit: 'closed', last_error: '', updated_at: null, age_seconds: null }
const channel = (sent: number, total = 1000): ChannelCounts => ({
  queued: total - sent,
  sending: 0,
  retrying: 0,
  sent,
  failed: 0,
  skipped: 0,
  total,
  provider,
})
const stats = (status: Stats['status'], sent: number): Stats => ({ campaign_id: 7, status, channels: { sms: channel(sent), email: channel(sent) } })

const KEY = 'GET /api/campaigns/7/stats'

function polling(id: number | (() => number) = 7) {
  const p = scope.run(() => useStatsPolling(id))
  if (!p) throw new Error('no polling')
  return p
}

/** Lets pending promises (the request and its answer) settle without moving the clock. */
const settle = () => vi.advanceTimersByTimeAsync(0)

describe('useStatsPolling', () => {
  it('asks at once, then again every second while the campaign is sending', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('sending', 0) } })
    const p = polling()
    await settle()
    expect(api.callsTo(KEY)).toHaveLength(1)
    expect(p.stats.value?.status).toBe('sending')

    await vi.advanceTimersByTimeAsync(1000)
    expect(api.callsTo(KEY)).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.callsTo(KEY)).toHaveLength(3)
    expect(p.polling.value).toBe(true)
  })

  it('does not start the next request before the last one has ended', async () => {
    let finish: () => void = () => undefined
    const api = installFakeApi({
      [KEY]: async () => {
        await new Promise<void>((resolve) => (finish = resolve))
        return { body: stats('sending', 0) }
      },
    })
    polling()
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.callsTo(KEY)).toHaveLength(1) // a slow server is not buried in requests
    finish()
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.callsTo(KEY)).toHaveLength(2)
  })

  it('stops when the campaign is no longer sending, and keeps the last numbers', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('sending', 400) } })
    const p = polling()
    await settle()
    api.set(KEY, { body: stats('completed', 1000) })
    await vi.advanceTimersByTimeAsync(1000)
    expect(p.stats.value?.status).toBe('completed')
    expect(p.polling.value).toBe(false)
    const calls = api.callsTo(KEY).length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(api.callsTo(KEY)).toHaveLength(calls)
    expect(p.stats.value?.channels.sms?.sent).toBe(1000)
  })

  it('asks once and stops for a campaign that was already finished when the page opened', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('completed_with_failures', 1000) } })
    const p = polling()
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.callsTo(KEY)).toHaveLength(1)
    expect(p.polling.value).toBe(false)
  })

  it('keeps polling after the campaign was approved but has not started sending', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('approved', 0) } })
    polling()
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.callsTo(KEY).length).toBeGreaterThanOrEqual(3)
  })

  it('stops when its scope ends: no timer is left and no request is made', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('sending', 0) } })
    polling()
    await vi.advanceTimersByTimeAsync(1000)
    const calls = api.callsTo(KEY).length
    scope.stop()
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(api.callsTo(KEY)).toHaveLength(calls)
  })

  it('aborts the request that is in flight when its scope ends', async () => {
    let signal: AbortSignal | undefined
    vi.stubGlobal('fetch', (_input: string, init?: RequestInit) => {
      signal = init?.signal ?? undefined
      return new Promise<Response>(() => undefined) // never answers
    })
    polling()
    await settle()
    expect(signal?.aborted).toBe(false)
    scope.stop()
    expect(signal?.aborted).toBe(true)
  })

  it('does not apply an answer that arrives after the scope ended', async () => {
    let finish: () => void = () => undefined
    installFakeApi({
      [KEY]: async () => {
        await new Promise<void>((resolve) => (finish = resolve))
        return { body: stats('sending', 5) }
      },
    })
    const p = polling()
    await settle()
    scope.stop()
    finish()
    await settle()
    expect(p.stats.value).toBeNull()
  })

  it('shows a failure and goes on asking; the last numbers stay, and the failure clears with the next answer', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('sending', 100) } })
    const p = polling()
    await settle()
    api.set(KEY, error(503, 'not_ready'))
    await vi.advanceTimersByTimeAsync(1000)
    expect(p.error.value).not.toBeNull()
    expect(p.stats.value?.channels.sms?.sent).toBe(100)
    expect(p.polling.value).toBe(true)

    await vi.advanceTimersByTimeAsync(1000)
    expect(api.callsTo(KEY)).toHaveLength(3)
    api.set(KEY, { body: stats('sending', 200) })
    await vi.advanceTimersByTimeAsync(2000) // the second failure in a row doubled the wait
    expect(p.error.value).toBeNull()
    expect(p.stats.value?.channels.sms?.sent).toBe(200)
  })

  it('backs off after each failure in a row (1, 2, 4, 8, then 10 seconds) and returns to one second after an answer', async () => {
    const api = installFakeApi({ [KEY]: error(503, 'not_ready') })
    const p = polling()
    const waits: (number | null)[] = []
    await settle()
    waits.push(p.retryInSeconds.value)
    let calls = api.callsTo(KEY).length
    for (const wait of [1, 2, 4, 8, 10, 10]) {
      // one millisecond short of the wait: no request yet; at the wait: exactly one more
      await vi.advanceTimersByTimeAsync(wait * 1000 - 1)
      expect(api.callsTo(KEY)).toHaveLength(calls)
      await vi.advanceTimersByTimeAsync(1)
      expect(api.callsTo(KEY)).toHaveLength(++calls)
      waits.push(p.retryInSeconds.value)
    }
    expect(waits).toEqual([1, 2, 4, 8, 10, 10, 10])

    api.set(KEY, { body: stats('sending', 5) })
    await vi.advanceTimersByTimeAsync(10_000)
    expect(p.error.value).toBeNull()
    expect(p.retryInSeconds.value).toBeNull()
    calls = api.callsTo(KEY).length
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.callsTo(KEY)).toHaveLength(calls + 1) // back to every second
    // and the next failure starts from one second again
    api.set(KEY, error(503, 'not_ready'))
    await vi.advanceTimersByTimeAsync(1000)
    expect(p.retryInSeconds.value).toBe(1)
  })

  it('asks at once, with the first wait again, when the marketer presses retry', async () => {
    const api = installFakeApi({ [KEY]: error(503, 'not_ready') })
    const p = polling()
    await settle()
    await vi.advanceTimersByTimeAsync(3000) // failures at 0, 1 and 3 s: the wait is 4 s now
    expect(p.retryInSeconds.value).toBe(4)
    const calls = api.callsTo(KEY).length
    p.refresh()
    await settle()
    expect(api.callsTo(KEY)).toHaveLength(calls + 1)
    expect(p.retryInSeconds.value).toBe(1)
  })

  it('stops for a campaign that does not exist: asking again every second cannot help', async () => {
    const api = installFakeApi({ [KEY]: error(404, 'not_found') })
    const p = polling()
    await settle()
    expect(p.error.value).not.toBeNull()
    expect(p.polling.value).toBe(false)
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.callsTo(KEY)).toHaveLength(1)
  })

  it('waits out a Retry-After before asking again', async () => {
    const api = installFakeApi({ [KEY]: error(429, 'llm_rate_limited', null, { 'Retry-After': '4' }) })
    polling()
    await settle()
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.callsTo(KEY)).toHaveLength(1) // longer than the backoff of one second
    await vi.advanceTimersByTimeAsync(1100)
    expect(api.callsTo(KEY)).toHaveLength(2)
  })

  it('goes on after a first request that failed', async () => {
    const api = installFakeApi({ [KEY]: () => { throw new TypeError('Failed to fetch') } })
    const p = polling()
    await settle()
    expect(p.error.value).not.toBeNull()
    expect(p.stats.value).toBeNull()
    api.set(KEY, { body: stats('sending', 0) })
    await vi.advanceTimersByTimeAsync(1000)
    expect(p.stats.value).not.toBeNull()
  })

  it('measures the rate per channel over a window of the page’s own clock', async () => {
    let sent = 0
    installFakeApi({ [KEY]: () => ({ body: stats('sending', (sent += 100)) }) })
    const p = polling()
    await settle()
    expect(p.rates.value.sms).toBeNull() // one measurement
    await vi.advanceTimersByTimeAsync(1000)
    expect(p.rates.value.sms).toBeNull() // still too little
    await vi.advanceTimersByTimeAsync(2000)
    expect(p.rates.value.sms).toBeCloseTo(100, 0)
    expect(p.rates.value.email).toBeCloseTo(100, 0)
  })

  it('starts from nothing for another campaign', async () => {
    const api = installFakeApi({
      [KEY]: { body: stats('sending', 300) },
      'GET /api/campaigns/8/stats': { body: { ...stats('sending', 5), campaign_id: 8 } },
    })
    const id = shallowRef(7)
    const p = polling(() => id.value)
    await settle()
    expect(p.stats.value?.campaign_id).toBe(7)
    id.value = 8
    await nextTick()
    expect(p.stats.value).toBeNull()
    await settle()
    expect(p.stats.value?.campaign_id).toBe(8)
    expect(api.callsTo('GET /api/campaigns/8/stats')).toHaveLength(1)
  })

  it('can be asked for at once, and starts again if it had stopped', async () => {
    const api = installFakeApi({ [KEY]: { body: stats('completed', 1000) } })
    const p = polling()
    await settle()
    expect(p.polling.value).toBe(false)
    api.set(KEY, { body: stats('sending', 0) })
    p.refresh()
    await settle()
    expect(p.polling.value).toBe(true)
    expect(p.stats.value?.status).toBe('sending')
  })
})

import { onScopeDispose, shallowRef, toValue, watch, type MaybeRefOrGetter, type ShallowRef } from 'vue'
import { getStats } from '@/api/campaigns'
import { ApiError } from '@/api/client'
import type { Stats } from '@/api/types'
import { createRateWindow, isInFlight, type RateWindow } from '@/domain/delivery'

export interface StatsPolling {
  /** The latest answer. It stays when a later request fails. */
  stats: Readonly<ShallowRef<Stats | null>>
  /** The failure of the last request; null after a request that worked. Polling goes on. */
  error: Readonly<ShallowRef<unknown>>
  /** Messages per second per channel (null while it cannot be told yet). */
  rates: Readonly<ShallowRef<Record<string, number | null>>>
  /** When the latest answer arrived (Date.now()), or null. */
  updatedAt: Readonly<ShallowRef<number | null>>
  /** Whether more requests are going to be made. False once the campaign is no longer sending. */
  polling: Readonly<ShallowRef<boolean>>
  /** Asks at once (and starts polling again if it had stopped). */
  refresh: () => void
}

export interface PollingOptions {
  /** Pause between the end of one request and the start of the next. */
  intervalMs?: number
}

/**
 * The delivery numbers of one campaign, asked for again every second while it is being sent and not after it is done.
 *  - One request at a time: the next one is scheduled when the last has ended, so a slow server is not buried in requests.
 *  - A failure is kept for the screen to show, and the polling goes on: a server that restarts comes back by itself. Two
 *    exceptions: a campaign that is not found stops it (asking again every second cannot help), and a Retry-After from the
 *    server is waited out. A request that gets no answer within 10 s fails as `timeout` (see api/campaigns.ts) and is retried.
 *  - It stops when the answer says the campaign is no longer sending, when the scope ends (the page is left), and the request
 *    in flight is aborted then.
 *  - The rate is measured over a window of several seconds from the clock of the page (performance.now), not from times that
 *    the server reports, and only from answers that arrived.
 */
export function useStatsPolling(id: MaybeRefOrGetter<number>, options: PollingOptions = {}): StatsPolling {
  const intervalMs = options.intervalMs ?? 1000
  const stats = shallowRef<Stats | null>(null)
  const error = shallowRef<unknown>(null)
  const rates = shallowRef<Record<string, number | null>>({})
  const updatedAt = shallowRef<number | null>(null)
  const polling = shallowRef(true)

  let timer: ReturnType<typeof setTimeout> | null = null
  let request: AbortController | null = null
  let windows = new Map<string, RateWindow>()
  let generation = 0

  function clear(): void {
    if (timer !== null) clearTimeout(timer)
    timer = null
    request?.abort()
    request = null
  }

  function measure(next: Stats): void {
    const now = performance.now()
    const out: Record<string, number | null> = {}
    for (const [channel, counts] of Object.entries(next.channels)) {
      let w = windows.get(channel)
      if (!w) {
        w = createRateWindow()
        windows.set(channel, w)
      }
      w.push(now, counts.sent)
      out[channel] = w.rate()
    }
    rates.value = out
  }

  async function poll(mine: number): Promise<void> {
    timer = null
    const controller = new AbortController()
    request = controller
    let wait = intervalMs
    try {
      const next = await getStats(toValue(id), controller.signal)
      if (controller.signal.aborted || mine !== generation) return
      stats.value = next
      error.value = null
      updatedAt.value = Date.now()
      measure(next)
      if (!isInFlight(next.status)) {
        polling.value = false
        return
      }
    } catch (err) {
      if (controller.signal.aborted || mine !== generation) return
      error.value = err
      if (err instanceof ApiError && err.code === 'not_found') {
        polling.value = false
        return
      }
      wait = err instanceof ApiError && err.retryAfterSeconds !== null ? Math.max(intervalMs, err.retryAfterSeconds * 1000) : intervalMs
    } finally {
      if (request === controller) request = null
    }
    if (mine === generation) timer = setTimeout(() => void poll(mine), wait)
  }

  function start(): void {
    clear()
    generation++
    polling.value = true
    void poll(generation)
  }

  function refresh(): void {
    start()
  }

  // A different campaign starts from nothing.
  watch(
    () => toValue(id),
    () => {
      stats.value = null
      error.value = null
      rates.value = {}
      updatedAt.value = null
      windows = new Map()
      start()
    },
    { immediate: true },
  )

  onScopeDispose(() => {
    generation++
    clear()
  })

  return { stats, error, rates, updatedAt, polling, refresh }
}

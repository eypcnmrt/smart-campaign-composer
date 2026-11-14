import type { CampaignStatus, ChannelCounts, Timeline } from '@/api/types'
import { formatNumber, formatRate } from './format'
import type { Tone } from '@/components/ui/StatusIcon.vue'

// Figures of the delivery dashboard that are not about drawing: how far a channel is, what its rate is, and how a campaign
// status is called. Pure, so every rule is a table test.

export interface Progress {
  /** Deliveries that are settled: sent, failed for good, or skipped. */
  done: number
  total: number
  /** 0 to 100, whole numbers, rounded down so that 100 means really everything. */
  percent: number
}

/** (sent + failed + skipped) / total. A channel with nobody to send to has nothing left to do: it is complete. */
export function progressOf(c: Pick<ChannelCounts, 'sent' | 'failed' | 'skipped' | 'total'>): Progress {
  const done = c.sent + c.failed + c.skipped
  if (c.total <= 0) return { done: 0, total: 0, percent: 100 }
  return { done, total: c.total, percent: Math.min(100, Math.floor((done / c.total) * 100)) }
}

/** What is still to be done on a channel: waiting, being sent, or waiting for a retry. */
export function pendingOf(c: Pick<ChannelCounts, 'queued' | 'sending' | 'retrying'>): number {
  return c.queued + c.sending + c.retrying
}

export interface RateWindow {
  /** Records the number of messages sent so far at a moment (milliseconds from a monotonic clock). */
  push: (nowMs: number, sent: number) => void
  /** Messages per second over the window, or null while there is not enough of it to say. */
  rate: () => number | null
  reset: () => void
}

/**
 * The rate is the change of the sent count between the oldest and the newest measurement inside the window, not the change since
 * the last poll: one slow answer or one burst moves it only by its share of the whole window. Nothing is shown until the
 * measurements span `minSpanMs`, so a single pair of measurements cannot produce a number. A count that goes down (another
 * campaign, a reset) starts the window again.
 */
export function createRateWindow(windowMs = 5000, minSpanMs = 2000): RateWindow {
  let samples: { t: number; sent: number }[] = []

  function push(nowMs: number, sent: number): void {
    const last = samples[samples.length - 1]
    if (last && (sent < last.sent || nowMs < last.t)) samples = []
    samples.push({ t: nowMs, sent })
    const oldest = nowMs - windowMs
    // Keep one measurement older than the window so that the span covers the whole of it.
    while (samples.length > 2 && (samples[1]?.t ?? Infinity) <= oldest) samples.shift()
  }

  function rate(): number | null {
    const first = samples[0]
    const last = samples[samples.length - 1]
    if (!first || !last || first === last) return null
    const span = last.t - first.t
    if (span < minSpanMs) return null
    return ((last.sent - first.sent) / span) * 1000
  }

  return {
    push,
    rate,
    reset: () => {
      samples = []
    },
  }
}

export const STATUS_LABELS: Record<CampaignStatus, { label: string; tone: Tone }> = {
  draft: { label: 'Taslak', tone: 'neutral' },
  audience_ready: { label: 'Kitle hazır', tone: 'info' },
  content_ready: { label: 'Onay bekliyor', tone: 'info' },
  approved: { label: 'Onaylandı', tone: 'info' },
  sending: { label: 'Gönderiliyor', tone: 'info' },
  completed: { label: 'Tamamlandı', tone: 'success' },
  completed_with_failures: { label: 'Hatalarla tamamlandı', tone: 'warning' },
}

/** Whether deliveries of a campaign in this status are still moving, so the numbers are worth asking for again. */
export function isInFlight(status: CampaignStatus): boolean {
  return status === 'approved' || status === 'sending'
}

export interface Bar {
  /** Position in bucket slots from the left, 0-based. */
  slot: number
  /** Messages per second in this bucket. */
  rate: number
}

export interface ChartModel {
  /** Number of bucket slots the width holds: at least `MIN_SLOTS`, so a send of three seconds is not three huge slabs. */
  slots: number
  /** The top of the scale in messages per second: above the limit and above the highest bar. */
  max: number
  bars: Bar[]
  /** The limit in messages per second, or null when the worker never reported one. */
  limit: number | null
  peak: number
  total: number
  /** Seconds the chart spans. */
  seconds: number
}

export const MIN_SLOTS = 12

/** The bars of a channel's timeline against its limit: pure numbers, so the drawing does no arithmetic. */
export function chartOf(timeline: Pick<Timeline, 'bucket_seconds' | 'accepted'>, limit: number | null): ChartModel {
  const width = timeline.bucket_seconds
  const bars = timeline.accepted.map((n, slot) => ({ slot, rate: n / width }))
  const peak = bars.reduce((m, b) => Math.max(m, b.rate), 0)
  const total = timeline.accepted.reduce((a, n) => a + n, 0)
  return {
    slots: Math.max(MIN_SLOTS, bars.length),
    max: Math.max(peak, limit ?? 0, 1) * 1.15,
    bars,
    limit,
    peak,
    total,
    seconds: bars.length * width,
  }
}

/** The chart in words, for a screen reader (the drawing itself is not read). */
export function chartText(label: string, chart: ChartModel): string {
  const limit = chart.limit === null ? '' : ` Sınır: saniyede ${formatRate(chart.limit)} mesaj.`
  if (chart.total === 0) return `${label}: henüz kabul edilen mesaj yok.${limit}`
  return `${label}: ${formatNumber(chart.seconds)} saniyede ${formatNumber(chart.total)} mesaj kabul edildi; en yoğun saniyede ${formatRate(chart.peak)} mesaj.${limit}`
}

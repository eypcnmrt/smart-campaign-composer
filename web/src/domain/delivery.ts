import type { CampaignStatus, ChannelCounts } from '@/api/types'
import type { Tone } from '@/components/ui/StatusIcon.vue'

// Figures of the delivery dashboard that are not about drawing: how far a channel is, what its rate is, and how a campaign
// status is called. Pure, so every rule is a table test.

/**
 * The rate limit of one channel, in messages per second. The worker applies it per channel (each channel has its own limiter,
 * backend/internal/worker/worker.go), so the dashboard draws one line on each channel, not one on the total. It is the worker's
 * default: the API does not report the configured value, so a deployment with another RATE_PER_SECOND would show a wrong line.
 */
export const RATE_LIMIT_PER_CHANNEL = 100

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

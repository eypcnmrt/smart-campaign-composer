<script setup lang="ts">
import { computed } from 'vue'
import type { ChannelCounts } from '@/api/types'
import { pendingOf, progressOf } from '@/domain/delivery'
import { formatNumber } from '@/domain/format'
import RateChart from './RateChart.vue'
import ProviderBadge from './ProviderBadge.vue'
import RateMeter from './RateMeter.vue'

// One channel of the delivery: how far it is, the six counts, the rate against the channel's limit, the per-second history of
// what the provider accepted, and the provider's health.
// `live` is false once the delivery is over: a rate of a finished delivery is not a figure to show.
const props = withDefaults(defineProps<{ channel: string; label: string; counts: ChannelCounts; rate: number | null; live?: boolean }>(), { live: true })

const progress = computed(() => progressOf(props.counts))
const pending = computed(() => pendingOf(props.counts))

const ROWS: readonly { key: 'queued' | 'sending' | 'retrying' | 'sent' | 'failed' | 'skipped'; label: string }[] = [
  { key: 'queued', label: 'Sırada' },
  { key: 'sending', label: 'Gönderiliyor' },
  { key: 'retrying', label: 'Yeniden denenecek' },
  { key: 'sent', label: 'Gönderildi' },
  { key: 'failed', label: 'Başarısız' },
  { key: 'skipped', label: 'Atlandı' },
]
</script>

<template>
  <section class="channel" :aria-labelledby="`ch-${channel}`" :data-channel="channel">
    <div class="channel__head">
      <h3 :id="`ch-${channel}`">{{ label }}</h3>
      <!-- The health is the worker's, for the channel as a whole: on a finished campaign (nothing waits) it says nothing about this one. -->
      <ProviderBadge v-if="live || pending > 0" :label="label" :provider="counts.provider" />
    </div>

    <div class="progress">
      <div
        class="progress__track"
        role="progressbar"
        :aria-label="`${label} gönderim ilerlemesi`"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-valuenow="progress.percent"
        :aria-valuetext="progress.total === 0 ? 'Bu kanalda alıcı yok' : `${progress.percent}%: ${formatNumber(progress.done)} / ${formatNumber(progress.total)}`"
      >
        <svg class="progress__bar" viewBox="0 0 100 10" preserveAspectRatio="none" aria-hidden="true" focusable="false">
          <rect class="progress__rest" x="0" y="0" width="100" height="10" />
          <rect class="progress__done" x="0" y="0" :width="progress.percent" height="10" />
        </svg>
      </div>
      <p class="progress__text">
        <template v-if="progress.total === 0">Bu kanalda alıcı yok.</template>
        <template v-else>
          <span class="num progress__percent">%{{ progress.percent }}</span>
          <span class="num"> {{ formatNumber(progress.done) }} / {{ formatNumber(progress.total) }}</span> mesaj tamamlandı
        </template>
      </p>
    </div>

    <dl class="counts">
      <div v-for="r in ROWS" :key="r.key" class="count" :class="{ 'count--failed': r.key === 'failed' && counts.failed > 0 }">
        <dt>{{ r.label }}</dt>
        <dd class="num" :data-count="r.key">{{ formatNumber(counts[r.key]) }}</dd>
      </div>
    </dl>

    <RateMeter v-if="live" :rate="rate" :label="label" :limit="counts.rate_limit ?? null" />
    <!-- While sending the chart grows with every answer; afterwards it stays as the record of how fast it went. -->
    <RateChart v-if="counts.timeline" :timeline="counts.timeline" :limit="counts.rate_limit ?? null" :label="label" />
  </section>
</template>

<style scoped>
.channel {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
  padding: var(--space-4);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.channel__head {
  display: grid;
  gap: var(--space-2);
  justify-items: start;
}

.progress {
  display: grid;
  gap: var(--space-2);
}

.progress__track {
  overflow: hidden;
  border-radius: var(--radius-pill);
}

.progress__bar {
  display: block;
  width: 100%;
  height: 0.875rem;
}

.progress__rest {
  fill: var(--bg-sunken);
}

.progress__done {
  fill: var(--action);
}

.progress__percent {
  margin-inline-end: var(--space-2);
  font-size: var(--text-xl);
  font-weight: 650;
}

.progress__text {
  color: var(--text-soft);
}

.counts {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-2);
  margin: 0;
}

.count {
  display: grid;
  /* The name may take two lines; the number stays at the bottom, so the numbers of a row line up. */
  grid-template-rows: 1fr auto;
  gap: 0.125rem;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--bg-sunken);
}

.count dt {
  color: var(--text-soft);
  font-size: var(--text-xs);
}

.count dd {
  margin: 0;
  font-size: var(--text-lg);
  font-weight: 650;
}

/* Failures are the one count that asks to be looked at; the colour comes with the word "Başarısız" above it. */
.count--failed {
  background: var(--danger-bg);
}

.count--failed dd {
  color: var(--danger);
}
</style>

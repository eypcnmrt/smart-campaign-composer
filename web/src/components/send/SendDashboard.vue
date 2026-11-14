<script setup lang="ts">
import { computed, watch } from 'vue'
import { describeError } from '@/api/errors'
import type { Campaign } from '@/api/types'
import ErrorPanel from '@/components/ui/ErrorPanel.vue'
import LoadingState from '@/components/ui/LoadingState.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import StatusNote from '@/components/ui/StatusNote.vue'
import { useStatsPolling } from '@/composables/useStatsPolling'
import { STATUS_LABELS } from '@/domain/delivery'
import { formatDateTime } from '@/domain/format'
import ChannelProgress from './ChannelProgress.vue'
import ResultBanner from './ResultBanner.vue'
import SummaryLine from './SummaryLine.vue'

// The fifth step: the delivery, live. The numbers are asked for every second while the campaign is being sent and no longer
// after it is done. A failed request is said in a line and the asking goes on, a little slower each time (1, 2, 4, 8, at most 10
// seconds; the line says how long it waits): the numbers on screen are the last ones that came, and they are labelled with when
// that was.
const props = defineProps<{ campaign: Campaign }>()
const emit = defineEmits<{
  /** The campaign's status moved on (it finished): read the campaign again. */
  reload: []
}>()

const { stats, error, rates, updatedAt, retryInSeconds, refresh } = useStatsPolling(() => props.campaign.id)

const LABELS: Record<string, string> = { sms: 'SMS', email: 'E-posta' }
const ORDER = ['sms', 'email']

const channels = computed(() => {
  const all = Object.entries(stats.value?.channels ?? {})
  return all.sort(([a], [b]) => (ORDER.indexOf(a) + 1 || 99) - (ORDER.indexOf(b) + 1 || 99))
})

const status = computed(() => stats.value?.status ?? props.campaign.status)
const label = computed(() => STATUS_LABELS[status.value])
const finished = computed(() => status.value === 'completed' || status.value === 'completed_with_failures')

// The rail and the header should not keep saying "sending" once it is over.
watch(
  () => stats.value?.status,
  (now) => {
    if (now !== undefined && now !== props.campaign.status) emit('reload')
  },
)

const failure = computed(() => (error.value === null ? null : describeError(error.value)))
const updated = computed(() => (updatedAt.value === null ? '' : formatDateTime(new Date(updatedAt.value).toISOString())))
</script>

<template>
  <section class="send" aria-labelledby="send-title">
    <div class="send__head">
      <h2 id="send-title" tabindex="-1">Gönderim</h2>
      <StatusBadge :tone="label.tone" :label="label.label" />
    </div>

    <LoadingState v-if="stats === null && error === null" label="Gönderim durumu okunuyor…" />
    <ErrorPanel v-else-if="stats === null" :error="error" @retry="refresh">
      <p v-if="retryInSeconds !== null" class="retrying" role="status">Yeniden deneniyor ({{ retryInSeconds }} sn)</p>
    </ErrorPanel>

    <template v-else>
      <ResultBanner v-if="finished" :stats="stats" />
      <SummaryLine v-if="finished && stats.summary" :summary="stats.summary" />

      <StatusNote v-if="failure" tone="warning" title="Güncel durum alınamıyor" announce>
        <p>{{ failure.title }}. Sayılar son alınan değerleri gösteriyor.</p>
        <p v-if="retryInSeconds !== null" class="retrying"><strong>Yeniden deneniyor ({{ retryInSeconds }} sn)</strong></p>
      </StatusNote>

      <div class="channels">
        <ChannelProgress
          v-for="[channel, counts] in channels"
          :key="channel"
          :channel="channel"
          :label="LABELS[channel] ?? channel"
          :counts="counts"
          :rate="rates[channel] ?? null"
          :live="!finished"
        />
      </div>

      <p class="updated">
        <template v-if="finished">Gönderim bitti; sayılar kesindir.</template>
        <template v-else>Sayılar her saniye yenilenir.</template>
        <template v-if="updated"> Son güncelleme: <span class="num">{{ updated }}</span>.</template>
      </p>
    </template>
  </section>
</template>

<style scoped>
.send {
  display: grid;
  gap: var(--space-4);
}

.send h2:focus {
  outline: none;
}

.send__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.channels {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: var(--space-4);
}

.updated {
  color: var(--text-soft);
  font-size: var(--text-sm);
}

@media (min-width: 48rem) {
  .channels {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>

<script setup lang="ts">
import { computed } from 'vue'
import type { SendSummary } from '@/api/types'
import { formatDuration, formatNumber, formatRate } from '@/domain/format'

// The one line under the result: how long the delivery took, how fast it went and whether anything had to be tried again. It is
// the evidence that the rate limit was kept and that the retries really happened, and it stays after the live view is gone.
const props = defineProps<{ summary: SendSummary }>()

const text = computed(() => {
  const s = props.summary
  const retries =
    s.retried_messages === 0
      ? 'Yeniden deneme gerekmedi'
      : `${formatNumber(s.retried_messages)} mesaj en az bir kez yeniden denendi`
  return [
    s.duration_seconds === null ? null : `Süre ${formatDuration(s.duration_seconds)}`,
    `ortalama ${formatRate(s.average_per_second)} mesaj/sn`,
    `en yüksek ${formatRate(s.peak_per_second)} mesaj/sn`,
    `${retries} (toplam ${formatNumber(s.total_attempts)} deneme)`,
  ]
    .filter((part) => part !== null)
    .join(' · ')
})
</script>

<template>
  <p class="summary num" data-testid="send-summary">{{ text }}</p>
</template>

<style scoped>
.summary {
  color: var(--text-soft);
  font-size: var(--text-base);
}
</style>

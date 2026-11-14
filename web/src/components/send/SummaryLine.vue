<script setup lang="ts">
import { computed } from 'vue'
import type { SendSummary } from '@/api/types'
import { formatDuration, formatNumber } from '@/domain/format'

// The one line under the result: how long the delivery took and whether anything had to be tried again. It says nothing about
// speed on purpose: the cap is per channel, so a rate of both channels together (176/s against two caps of 100/s) reads as a
// broken limit. The speeds are in each channel's own card, next to its cap. The line stays after the live view is gone.
const props = defineProps<{ summary: SendSummary }>()

const text = computed(() => {
  const s = props.summary
  const retries =
    s.retried_messages === 0
      ? 'Yeniden deneme gerekmedi'
      : `${formatNumber(s.retried_messages)} mesaj en az bir kez yeniden denendi`
  return [
    s.duration_seconds === null ? null : `Süre ${formatDuration(s.duration_seconds)}`,
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

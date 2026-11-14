<script setup lang="ts">
import { computed } from 'vue'
import { formatNumber } from '@/domain/format'
import { RATE_LIMIT_PER_CHANNEL } from '@/domain/delivery'

// How fast one channel is going, against that channel's own limit. The scale runs a little past the limit so that the line
// is inside the bar and a rate above it is visible. The figure is the number of messages the provider accepted per second,
// averaged over the last few seconds: it does not count attempts that failed or are waiting for a retry.
const props = defineProps<{ rate: number | null; label: string }>()

const SCALE = RATE_LIMIT_PER_CHANNEL * 1.2

const shown = computed(() => (props.rate === null ? null : Math.round(props.rate)))
const width = computed(() => (props.rate === null ? 0 : Math.min(SCALE, Math.max(0, props.rate))))
const over = computed(() => props.rate !== null && props.rate > RATE_LIMIT_PER_CHANNEL)
</script>

<template>
  <div class="meter">
    <p class="meter__figure">
      <span class="num meter__value">{{ shown === null ? '…' : formatNumber(shown) }}</span>
      <span class="meter__unit"> kabul edilen mesaj/s</span>
      <span v-if="shown === null" class="visually-hidden"> (ölçülüyor)</span>
    </p>
    <svg class="meter__bar" viewBox="0 0 120 10" preserveAspectRatio="none" role="img" :aria-label="`${label} hızı: ${shown === null ? 'ölçülüyor' : formatNumber(shown) + ' mesaj/s'}; sınır ${RATE_LIMIT_PER_CHANNEL} mesaj/s`">
      <rect class="meter__track" x="0" y="0" width="120" height="10" />
      <rect class="meter__fill" :class="{ 'meter__fill--over': over }" x="0" y="0" :width="width" height="10" />
      <line class="meter__limit" :x1="RATE_LIMIT_PER_CHANNEL" :x2="RATE_LIMIT_PER_CHANNEL" y1="0" y2="10" />
    </svg>
    <p class="meter__caption"><span class="num">{{ RATE_LIMIT_PER_CHANNEL }}</span>/s: kanal başına sınır</p>
  </div>
</template>

<style scoped>
.meter {
  display: grid;
  gap: var(--space-1);
}

.meter__figure {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1);
}

.meter__value {
  font-size: var(--text-xl);
  font-weight: 650;
}

.meter__unit,
.meter__caption {
  color: var(--text-soft);
  font-size: var(--text-sm);
}

.meter__bar {
  display: block;
  width: 100%;
  height: 0.75rem;
}

.meter__track {
  fill: var(--bg-sunken);
}

.meter__fill {
  fill: var(--action);
}

.meter__fill--over {
  fill: var(--warning-border);
}

/* The limit is a line with its own caption, in the text colour so it reads on the track and on the fill. */
.meter__limit {
  stroke: var(--text);
  stroke-width: 1.5;
  vector-effect: non-scaling-stroke;
}
</style>

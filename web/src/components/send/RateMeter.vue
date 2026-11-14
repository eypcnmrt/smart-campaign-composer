<script setup lang="ts">
import { computed } from 'vue'
import { formatNumber } from '@/domain/format'

// How fast one channel is going, against that channel's own limit. The scale runs a little past the limit so that the line
// is inside the bar and a rate above it is visible. The limit is the one the worker reported for the channel; without one
// (the worker never said) the bar has no line and no caption. The figure is the number of messages the provider accepted per second,
// averaged over the last few seconds: it does not count attempts that failed or are waiting for a retry.
const props = defineProps<{ rate: number | null; label: string; limit?: number | null }>()

const limit = computed(() => props.limit ?? null)
// Without a limit the scale follows the rate itself.
const SCALE = computed(() => (limit.value ?? Math.max(10, props.rate ?? 0)) * 1.2)

const shown = computed(() => (props.rate === null ? null : Math.round(props.rate)))
const width = computed(() => (props.rate === null ? 0 : Math.min(SCALE.value, Math.max(0, props.rate))))
const over = computed(() => limit.value !== null && props.rate !== null && props.rate > limit.value)
// The drawing is 120 units wide whatever the scale: the bar and the line are positioned as a share of it.
const fill = computed(() => (width.value / SCALE.value) * 120)
const limitX = computed(() => (limit.value === null ? null : (limit.value / SCALE.value) * 120))
const described = computed(() => `${props.label} hızı: ${shown.value === null ? 'ölçülüyor' : formatNumber(shown.value) + ' mesaj/s'}${limit.value === null ? '' : `; sınır ${formatNumber(limit.value)} mesaj/s`}`)
</script>

<template>
  <div class="meter">
    <p class="meter__figure">
      <span class="num meter__value">{{ shown === null ? '…' : formatNumber(shown) }}</span>
      <span class="meter__unit"> kabul edilen mesaj/s</span>
      <span v-if="shown === null" class="visually-hidden"> (ölçülüyor)</span>
    </p>
    <svg class="meter__bar" viewBox="0 0 120 10" preserveAspectRatio="none" role="img" :aria-label="described">
      <rect class="meter__track" x="0" y="0" width="120" height="10" />
      <rect class="meter__fill" :class="{ 'meter__fill--over': over }" x="0" y="0" :width="fill" height="10" />
      <line v-if="limitX !== null" class="meter__limit" :x1="limitX" :x2="limitX" y1="0" y2="10" />
    </svg>
    <p v-if="limit !== null" class="meter__caption"><span class="num">{{ formatNumber(limit) }}</span>/s: kanal başına sınır</p>
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

<script setup lang="ts">
import { computed, useId } from 'vue'
import type { Timeline } from '@/api/types'
import { chartOf, chartText } from '@/domain/delivery'
import { formatDuration, formatRate } from '@/domain/format'

// The messages the provider accepted, second by second, as bars, with the channel's rate limit as a line. A small SVG without a
// chart library: each bar is one bucket of the server's timeline. While the delivery runs the chart grows to the right with
// every answer; once it is over it stays, so the speed and the cap can still be seen afterwards. The drawing is for the eye; a
// screen reader gets the same facts as a sentence (the image's label), and the figures are also in the summary line.
const props = defineProps<{ timeline: Timeline; limit: number | null; label: string }>()

const id = useId()
const chart = computed(() => chartOf(props.timeline, props.limit))
const text = computed(() => chartText(props.label, chart.value))

// The drawing is 240 units wide and 60 high, stretched to the width of the card; a bar is a bucket slot wide minus a hairline.
const W = 240
const H = 60
const slot = computed(() => W / chart.value.slots)
const y = (rate: number) => H - (rate / chart.value.max) * H
const limitY = computed(() => (chart.value.limit === null ? null : y(chart.value.limit)))
const over = (rate: number) => chart.value.limit !== null && rate > chart.value.limit

const span = computed(() => formatDuration(chart.value.seconds))
</script>

<template>
  <figure class="chart">
    <figcaption :id="`${id}-caption`" class="chart__title">Saniyede kabul edilen mesaj</figcaption>
    <svg class="chart__svg" :viewBox="`0 0 ${W} ${H}`" preserveAspectRatio="none" role="img" :aria-label="text" focusable="false">
      <rect class="chart__plot" x="0" y="0" :width="W" :height="H" />
      <rect
        v-for="b in chart.bars"
        :key="b.slot"
        class="chart__bar"
        :class="{ 'chart__bar--over': over(b.rate) }"
        :x="b.slot * slot + 0.3"
        :y="y(b.rate)"
        :width="Math.max(slot - 0.6, 0.3)"
        :height="H - y(b.rate)"
      />
      <line v-if="limitY !== null" class="chart__limit" x1="0" :x2="W" :y1="limitY" :y2="limitY" />
    </svg>
    <div class="chart__axis" aria-hidden="true">
      <span class="num">0</span>
      <span class="num">{{ span }}</span>
    </div>
    <p class="chart__caption">
      <template v-if="limit !== null"><span class="chart__key" aria-hidden="true" />Sınır: <span class="num">{{ formatRate(limit) }}</span>/s · </template>
      En yoğun: <span class="num">{{ formatRate(chart.peak) }}</span>/s
    </p>
  </figure>
</template>

<style scoped>
.chart {
  display: grid;
  gap: var(--space-1);
  margin: 0;
}

.chart__title {
  font-size: var(--text-sm);
  font-weight: 600;
}

.chart__svg {
  display: block;
  width: 100%;
  height: 4.5rem;
}

.chart__plot {
  fill: var(--bg-sunken);
}

.chart__bar {
  fill: var(--action);
}

.chart__bar--over {
  fill: var(--warning-border);
}

/* The limit is a dashed line in the text colour; its own caption says what it is, so it is not a legend one has to match by colour. */
.chart__limit {
  stroke: var(--text);
  stroke-width: 1.5;
  stroke-dasharray: 4 3;
  vector-effect: non-scaling-stroke;
}

.chart__axis {
  display: flex;
  justify-content: space-between;
  color: var(--text-soft);
  font-size: var(--text-xs);
}

.chart__caption {
  color: var(--text-soft);
  font-size: var(--text-sm);
}

.chart__key {
  display: inline-block;
  width: 1.25rem;
  margin-inline-end: var(--space-1);
  vertical-align: middle;
  border-block-start: 2px dashed var(--text);
}
</style>

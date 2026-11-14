<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, shallowRef, useId, watch } from 'vue'

// The marketer's own brief, read-only, above the audience, content and approval steps: the rule, the texts and the counts are
// all judged against it. Two lines when it is long, with a button for the rest. Whether it is cut is measured (a brief of
// three short lines is cut too; one long line on a wide screen is not), and measured again when the width changes.
const props = defineProps<{ brief: string }>()

const id = useId()
const text = shallowRef<HTMLElement | null>(null)
const open = shallowRef(false)
const overflowing = shallowRef(false)

function measure(): void {
  const el = text.value
  // While it is open nothing is cut, so the button must stay (it is how the marketer folds it again).
  if (!el || open.value) return
  overflowing.value = el.scrollHeight > el.clientHeight + 1
}

let observer: ResizeObserver | null = null
onMounted(() => {
  measure()
  if (typeof ResizeObserver !== 'undefined' && text.value) {
    observer = new ResizeObserver(() => measure())
    observer.observe(text.value)
  }
})
onBeforeUnmount(() => observer?.disconnect())
watch(
  () => props.brief,
  async () => {
    open.value = false
    await nextTick()
    measure()
  },
)
</script>

<template>
  <section class="brief-summary" aria-labelledby="brief-summary-title">
    <!-- Not a heading: the step's own h2 is where focus goes after a step change. -->
    <p id="brief-summary-title" class="brief-summary__title">Brief</p>
    <p :id="id" ref="text" class="brief-summary__text" :class="{ 'brief-summary__text--clamped': !open }">{{ brief }}</p>
    <button
      v-if="overflowing || open"
      type="button"
      class="link-button brief-summary__toggle"
      :aria-expanded="open"
      :aria-controls="id"
      @click="open = !open"
    >
      {{ open ? 'Daha az göster' : 'Tamamını göster' }}
    </button>
  </section>
</template>

<style scoped>
.brief-summary {
  display: grid;
  gap: var(--space-1);
  justify-items: start;
  margin-block-end: var(--space-5);
  padding: var(--space-3) var(--space-4);
  border-inline-start: 4px solid var(--border-control);
  border-radius: var(--radius-sm);
  background: var(--bg-sunken);
}

.brief-summary__title {
  font-family: var(--font-body);
  font-size: var(--text-xs);
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-soft);
}

.brief-summary__text {
  margin: 0;
  max-width: 100%;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.brief-summary__text--clamped {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
}

.brief-summary__toggle {
  font-size: var(--text-sm);
}
</style>

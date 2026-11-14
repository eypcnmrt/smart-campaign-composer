<script setup lang="ts">
import { RouterLink } from 'vue-router'
import type { StepItem, StepState } from '@/domain/steps'

// The five steps of a campaign. A vertical rail beside the content on wide screens, a horizontal strip above it on narrow
// ones. A step is its number or a check, its name and, for a screen reader, its state: nothing is told by colour alone.
const props = defineProps<{ items: StepItem[]; campaignId: number | null }>()

const STATE_TEXT: Record<StepState, string> = {
  done: 'tamamlandı',
  current: 'şu anki adım',
  available: 'açılabilir',
  locked: 'henüz açık değil',
}

/** A step is a link when it can be opened and the campaign exists; otherwise it is plain text (the current step among them). */
function isLink(item: StepItem): boolean {
  return item.link && props.campaignId !== null
}

function attrs(item: StepItem): Record<string, unknown> {
  if (isLink(item)) return { to: { name: 'campaign', params: { id: props.campaignId }, query: { step: item.id } } }
  return item.state === 'current' ? { 'aria-current': 'step' } : {}
}
</script>

<template>
  <nav class="steps" aria-label="Kampanya adımları">
    <ol class="steps__list">
      <li v-for="item in items" :key="item.id" class="step" :class="`step--${item.state}`">
        <component :is="isLink(item) ? RouterLink : 'span'" class="step__body" v-bind="attrs(item)">
          <span class="step__mark" aria-hidden="true">
            <svg v-if="item.state === 'done'" viewBox="0 0 16 16" focusable="false"><path d="M3.5 8.5l3 3 6-7" /></svg>
            <span v-else class="num">{{ item.number }}</span>
          </span>
          <span class="step__label">{{ item.label }}</span>
          <span class="visually-hidden">, {{ STATE_TEXT[item.state] }}</span>
        </component>
      </li>
    </ol>
  </nav>
</template>

<style scoped>
.steps__list {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}

.step__body {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 2.75rem;
  color: var(--text);
  font-weight: 600;
  text-decoration: none;
}

a.step__body:hover .step__label {
  text-decoration: underline;
  text-underline-offset: 0.18em;
}

.step__mark {
  display: inline-grid;
  place-items: center;
  flex: none;
  width: 1.75rem;
  height: 1.75rem;
  border: 2px solid var(--border-control);
  border-radius: 50%;
  background: var(--bg-surface);
  color: var(--text-soft);
  font-size: var(--text-sm);
  font-weight: 700;
}

.step__mark svg {
  width: 1rem;
  height: 1rem;
  fill: none;
  stroke: currentcolor;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}

/* Done: a check in ink. Success green is for delivered messages and never marks a control. */
.step--done .step__mark {
  border-color: var(--text);
  color: var(--text);
}

.step--available .step__mark {
  border-color: var(--action);
  color: var(--action);
}

.step--current .step__mark {
  border-color: var(--action);
  background: var(--action);
  color: var(--on-action);
}

.step--locked .step__body {
  color: var(--text-soft);
  font-weight: 400;
}

.step--locked .step__mark {
  border-color: var(--border);
}

@media (min-width: 60rem) {
  .steps__list {
    flex-direction: column;
    flex-wrap: nowrap;
    gap: 0;
  }

  .step {
    position: relative;
  }

  /* The line that joins the marks, under each but the last. */
  .step:not(:last-child)::after {
    content: '';
    position: absolute;
    inset-block: calc(50% + 0.875rem) calc(-50% + 0.875rem);
    inset-inline-start: calc(0.875rem - 1px);
    width: 2px;
    background: var(--border);
  }
}
</style>

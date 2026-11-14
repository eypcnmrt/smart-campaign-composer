<script setup lang="ts">
import { computed } from 'vue'
import StatusIcon, { type Tone } from './StatusIcon.vue'

// A block message (a warning on the audience card, an error after a request). Icon, a word for screen readers and the text
// itself carry the state; the tint is only a help. `announce` makes it a live region: use it for messages that appear
// after an action, not for ones that are there when the page opens.
const props = defineProps<{ tone: Tone; title?: string; announce?: boolean }>()

const WORD: Record<Tone, string> = { success: 'Başarılı', warning: 'Uyarı', danger: 'Hata', info: 'Bilgi', neutral: 'Durum' }

const role = computed(() => (props.announce ? (props.tone === 'danger' ? 'alert' : 'status') : undefined))
</script>

<template>
  <div class="note" :class="`note--${tone}`" :role="role">
    <StatusIcon class="note__icon" :tone="tone" />
    <div class="note__body">
      <p v-if="title" class="note__title">
        <span class="visually-hidden">{{ WORD[tone] }}: </span>{{ title }}
      </p>
      <span v-else class="visually-hidden">{{ WORD[tone] }}: </span>
      <slot />
    </div>
  </div>
</template>

<style scoped>
.note {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--tone-border);
  border-inline-start-width: 4px;
  border-radius: var(--radius-md);
  background: var(--tone-bg);
  color: var(--tone);
  font-size: var(--text-base);
}

.note__icon {
  margin-block-start: 0.15em;
  font-size: var(--text-lg);
}

.note__body {
  min-width: 0;
}

.note__title {
  font-weight: 700;
}

.note--success {
  --tone: var(--success);
  --tone-bg: var(--success-bg);
  --tone-border: var(--success-border);
}

.note--warning {
  --tone: var(--warning);
  --tone-bg: var(--warning-bg);
  --tone-border: var(--warning-border);
}

.note--danger {
  --tone: var(--danger);
  --tone-bg: var(--danger-bg);
  --tone-border: var(--danger-border);
}

.note--info {
  --tone: var(--action);
  --tone-bg: var(--action-tint);
  --tone-border: var(--action);
}

.note--neutral {
  --tone: var(--text-soft);
  --tone-bg: var(--bg-sunken);
  --tone-border: var(--border-control);
}
</style>

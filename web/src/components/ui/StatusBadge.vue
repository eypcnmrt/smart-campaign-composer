<script setup lang="ts">
import StatusIcon, { type Tone } from './StatusIcon.vue'

// A state is always an icon plus a word. The label is required, so colour is never the only carrier of meaning.
defineProps<{ tone: Tone; label: string }>()
</script>

<template>
  <span class="badge" :class="`badge--${tone}`">
    <StatusIcon :tone="tone" />
    <span>{{ label }}</span>
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 0.125rem var(--space-3) 0.125rem var(--space-2);
  border: 1px solid var(--tone-border);
  border-radius: var(--radius-pill);
  background: var(--tone-bg);
  color: var(--tone);
  font-size: var(--text-sm);
  font-weight: 600;
  line-height: 1.4;
  white-space: nowrap;
}

.badge--success {
  --tone: var(--success);
  --tone-bg: var(--success-bg);
  --tone-border: var(--success-border);
}

.badge--warning {
  --tone: var(--warning);
  --tone-bg: var(--warning-bg);
  --tone-border: var(--warning-border);
}

.badge--danger {
  --tone: var(--danger);
  --tone-bg: var(--danger-bg);
  --tone-border: var(--danger-border);
}

.badge--info {
  --tone: var(--action);
  --tone-bg: var(--action-tint);
  --tone-border: var(--action);
}

.badge--neutral {
  --tone: var(--text-soft);
  --tone-bg: var(--bg-sunken);
  --tone-border: var(--border-control);
}
</style>

<script setup lang="ts">
// Something is being read or made. A status region, so a screen reader hears it; the dots stop under prefers-reduced-motion
// (base.css), leaving the words.
defineProps<{ label: string }>()
</script>

<template>
  <p class="loading" role="status">
    <span class="loading__dots" aria-hidden="true"><i /><i /><i /></span>
    <span>{{ label }}</span>
  </p>
</template>

<style scoped>
.loading {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding-block: var(--space-4);
  color: var(--text-soft);
}

.loading__dots {
  display: inline-flex;
  gap: 0.25rem;
}

.loading__dots i {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  background: var(--action);
  animation: pulse 1.2s var(--ease) infinite;
}

.loading__dots i:nth-child(2) {
  animation-delay: 0.15s;
}

.loading__dots i:nth-child(3) {
  animation-delay: 0.3s;
}

@keyframes pulse {
  0%,
  80%,
  100% {
    opacity: 0.25;
  }
  40% {
    opacity: 1;
  }
}
</style>

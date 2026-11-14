<script setup lang="ts">
import type { LlmMode } from '@/api/types'

defineProps<{ mode: LlmMode; model: string }>()
</script>

<template>
  <div class="llm" :data-mode="mode">
    <span v-if="mode === 'mock'" class="llm__flag" title="Yanıtları gerçek bir model değil, örnek (mock) model üretiyor.">Mock LLM</span>
    <span class="llm__model"><span class="llm__label">Model:</span> {{ model }}</span>
  </div>
</template>

<style scoped>
.llm {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-sm);
}

/* The highlighter: this answer is not from a real model. */
.llm__flag {
  padding: 0.125rem var(--space-3);
  border-radius: var(--radius-pill);
  background: var(--mark);
  color: var(--on-mark);
  font-weight: 700;
  white-space: nowrap;
}

.llm__model {
  color: var(--text-soft);
  white-space: nowrap;
}

.llm__label {
  color: var(--text-soft);
}
</style>

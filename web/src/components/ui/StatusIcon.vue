<script setup lang="ts">
export type Tone = 'success' | 'warning' | 'danger' | 'info' | 'neutral'

// Each tone has its own shape, so a state is told apart without colour: circle with a check (success), triangle (warning),
// octagon with a cross (danger), circle with an i (info), clock (neutral: waiting). The text next to it says what it means.
// (No comment inside the template: it would make the component a fragment and class attributes could not fall through.)
defineProps<{ tone: Tone }>()
</script>

<template>
  <svg class="icon" :data-tone="tone" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
    <template v-if="tone === 'success'">
      <circle cx="12" cy="12" r="9" />
      <path d="M8 12.5l2.7 2.7L16 9.5" />
    </template>
    <template v-else-if="tone === 'warning'">
      <path d="M12 3.8L21.2 19.5H2.8L12 3.8z" />
      <path d="M12 10v4" />
      <path d="M12 16.9v.1" />
    </template>
    <template v-else-if="tone === 'danger'">
      <path d="M8.6 3h6.8L21 8.6v6.8L15.4 21H8.6L3 15.4V8.6L8.6 3z" />
      <path d="M9 9l6 6M15 9l-6 6" />
    </template>
    <template v-else-if="tone === 'info'">
      <circle cx="12" cy="12" r="9" />
      <path d="M12 11v5" />
      <path d="M12 8v.1" />
    </template>
    <template v-else>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </template>
  </svg>
</template>

<style scoped>
.icon {
  width: 1.2em;
  height: 1.2em;
  flex: none;
}
</style>

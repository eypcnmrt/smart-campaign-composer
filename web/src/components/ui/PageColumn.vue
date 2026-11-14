<script setup lang="ts">
// The two measures of the app. Reading and forms (brief, content, approval) use the narrow column; screens with a table or
// two channels side by side (the campaign list, the delivery dashboard) use the wide one. Content stays left-aligned.
defineProps<{ wide?: boolean }>()
</script>

<template>
  <div class="column" :class="{ 'column--wide': wide }">
    <slot />
  </div>
</template>

<style scoped>
.column {
  max-width: var(--column-narrow);
}

.column--wide {
  max-width: var(--column-wide);
}
</style>

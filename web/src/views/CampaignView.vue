<script setup lang="ts">
import PageColumn from '@/components/ui/PageColumn.vue'

defineProps<{ id: number }>()
</script>

<template>
  <!-- Brief, audience, content and approval use the narrow column; the sending step switches to the wide one. -->
  <PageColumn>
    <h1>Kampanya {{ id }}</h1>
    <p>Kampanya adımları sonraki aşamada eklenecek.</p>
  </PageColumn>
</template>

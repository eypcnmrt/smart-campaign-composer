<script setup lang="ts">
import PageColumn from '@/components/ui/PageColumn.vue'
</script>

<template>
  <PageColumn>
    <h1>Sayfa bulunamadı</h1>
    <p><RouterLink :to="{ name: 'campaigns' }">Kampanyalara dön</RouterLink></p>
  </PageColumn>
</template>

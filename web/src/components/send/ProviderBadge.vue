<script setup lang="ts">
import type { ProviderInfo } from '@/api/types'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import StatusNote from '@/components/ui/StatusNote.vue'

// The health of the provider of one channel, as the worker sees it. Three states, each in words:
//   ok                    the provider answers
//   provider_unreachable  the worker cannot reach it; the circuit is open and the messages wait in the queue (nothing is lost)
//   worker_down           nobody is serving the channel (no fresh heartbeat), so nothing moves
defineProps<{ label: string; provider: ProviderInfo }>()
</script>

<template>
  <StatusBadge v-if="provider.state === 'ok'" tone="success" :label="`${label} provider’ı çalışıyor`" />
  <StatusNote v-else-if="provider.state === 'provider_unreachable'" tone="warning" :title="`${label} provider’ına ulaşılamıyor, mesajlar bekletiliyor`" announce>
    <p>Hiçbir mesaj kaybolmaz: provider yanıt verince gönderim kaldığı yerden sürer.</p>
    <details v-if="provider.last_error" class="technical">
      <summary>Teknik ayrıntılar</summary>
      <p lang="en"><code>{{ provider.last_error }}</code></p>
    </details>
  </StatusNote>
  <StatusNote v-else tone="danger" :title="`${label} gönderim işçisi çalışmıyor`" announce>
    <p>Mesajlar kuyrukta bekliyor. İşçi yeniden başladığında gönderim kaldığı yerden sürer.</p>
  </StatusNote>
</template>

<style scoped>
.technical {
  margin-block-start: var(--space-2);
}

.technical summary {
  cursor: pointer;
  font-weight: 600;
}
</style>

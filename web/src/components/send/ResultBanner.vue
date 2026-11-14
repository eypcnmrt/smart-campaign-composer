<script setup lang="ts">
import { computed } from 'vue'
import type { Stats } from '@/api/types'
import StatusNote from '@/components/ui/StatusNote.vue'
import { formatNumber } from '@/domain/format'

// How a finished delivery ended, in words and with its numbers. A delivery with failures is not shown as a success.
const props = defineProps<{ stats: Stats }>()

const totals = computed(() => {
  const t = { sent: 0, failed: 0, skipped: 0, total: 0 }
  for (const c of Object.values(props.stats.channels)) {
    t.sent += c.sent
    t.failed += c.failed
    t.skipped += c.skipped
    t.total += c.total
  }
  return t
})
</script>

<template>
  <StatusNote v-if="stats.status === 'completed'" tone="success" title="Gönderim tamamlandı" announce>
    <p>
      <span class="num">{{ formatNumber(totals.sent) }}</span> mesaj gönderildi<template v-if="totals.skipped > 0">
        (<span class="num">{{ formatNumber(totals.skipped) }}</span> mesaj, alıcı izni geri çektiği için atlandı)</template
      >.
    </p>
  </StatusNote>
  <StatusNote v-else-if="stats.status === 'completed_with_failures'" tone="warning" title="Gönderim hatalarla tamamlandı" announce>
    <p>
      <span class="num">{{ formatNumber(totals.sent) }}</span> mesaj gönderildi,
      <strong><span class="num">{{ formatNumber(totals.failed) }}</span> mesaj gönderilemedi</strong><template v-if="totals.skipped > 0">,
        <span class="num">{{ formatNumber(totals.skipped) }}</span> mesaj, alıcı izni geri çektiği için atlandı</template
      >. Başarısız olanlar tüm yeniden denemelere rağmen gönderilemedi.
    </p>
  </StatusNote>
</template>

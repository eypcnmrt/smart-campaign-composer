<script setup lang="ts">
import { computed } from 'vue'
import type { Audience, Warning } from '@/api/types'
import StatusNote from '@/components/ui/StatusNote.vue'
import { formatNumber } from '@/domain/format'

// The warnings of the segment, each in its own words (chosen by the code, not by the server's English message). A warning
// code this file does not know is shown with the server's message, so a new server-side warning is never silent.
// no_recipients is a fact that stops the approval, so it is the danger note and comes first; the others can be acknowledged.
const props = defineProps<{ warnings: Warning[]; audience: Audience | null }>()

const ordered = computed(() => [...props.warnings].sort((a, b) => Number(b.code === 'no_recipients') - Number(a.code === 'no_recipients')))
</script>

<template>
  <div v-if="warnings.length > 0" class="warnings">
    <template v-for="w in ordered" :key="w.code">
      <StatusNote v-if="w.code === 'no_recipients'" tone="danger" title="Gönderilecek kimse yok" :data-warning="w.code">
        <p>Seçili kanallarda mesaj almayı kabul etmiş kimse yok; bu hâliyle kampanya onaylanamaz. Başka bir kanal açın ya da segmenti yeniden üretin.</p>
      </StatusNote>
      <StatusNote v-else-if="w.code === 'matches_everyone'" tone="warning" title="Kural herkesi kapsıyor" :data-warning="w.code">
        <p>
          Kural tüm kullanıcılara uyuyor<span v-if="audience" class="num"> ({{ formatNumber(audience.matched) }} / {{ formatNumber(audience.total_users) }})</span>.
          Kampanya, kanala izin vermiş herkese gider. Brief’in gerçekten bunu istediğinden emin olun.
        </p>
      </StatusNote>
      <StatusNote v-else-if="w.code === 'unsupported_criteria'" tone="warning" title="Brief’in bir kısmı kurala çevrilemedi" :data-warning="w.code">
        <p>Aşağıdaki ifadeler uygulanmıyor; kitle bunlara göre daraltılmadı:</p>
        <ul class="criteria">
          <li v-for="(c, i) in w.details ?? []" :key="i">{{ c }}</li>
        </ul>
      </StatusNote>
      <StatusNote v-else tone="warning" title="Uyarı" :data-warning="w.code">
        <p lang="en">{{ w.message }}</p>
      </StatusNote>
    </template>
  </div>
</template>

<style scoped>
.warnings {
  display: grid;
  gap: var(--space-3);
}

.criteria {
  margin: var(--space-2) 0 0;
  padding-inline-start: var(--space-5);
}
</style>

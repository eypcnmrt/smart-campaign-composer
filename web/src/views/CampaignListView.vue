<script setup lang="ts">
import { onBeforeUnmount, shallowRef } from 'vue'
import { listCampaigns } from '@/api/campaigns'
import type { CampaignSummary } from '@/api/types'
import ErrorPanel from '@/components/ui/ErrorPanel.vue'
import LoadingState from '@/components/ui/LoadingState.vue'
import PageColumn from '@/components/ui/PageColumn.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { STATUS_LABELS } from '@/domain/delivery'
import { formatDateTime, formatNumber, truncate } from '@/domain/format'

// The campaigns, newest first: where each one stands, who it reaches, whether it has warnings. A row opens the campaign at the
// step its status allows (the campaign page decides that), so a draft opens at the brief and a sent one at the delivery.
const campaigns = shallowRef<CampaignSummary[] | null>(null)
const error = shallowRef<unknown>(null)
let request: AbortController | null = null

async function load(): Promise<void> {
  request?.abort()
  const mine = new AbortController()
  request = mine
  error.value = null
  try {
    const list = await listCampaigns(50, mine.signal)
    if (mine.signal.aborted) return
    campaigns.value = list
  } catch (err) {
    if (mine.signal.aborted) return
    error.value = err
  }
}

void load()
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <PageColumn wide>
    <div class="head">
      <h1>Kampanyalar</h1>
    </div>

    <LoadingState v-if="campaigns === null && error === null" label="Kampanyalar yükleniyor…" />
    <ErrorPanel v-else-if="campaigns === null" :error="error" @retry="load" />

    <div v-else-if="campaigns.length === 0" class="empty">
      <h2>Henüz kampanya yok</h2>
      <p>Bir brief yazın; model kitleyi ve mesajları hazırlasın, siz onaylayın. Başlamak için üstteki “Yeni kampanya” düğmesini kullanın.</p>
    </div>

    <template v-else>
      <ErrorPanel v-if="error !== null" :error="error" retry-label="Listeyi yenile" @retry="load" />
      <div class="scroll" role="region" aria-label="Kampanya listesi" tabindex="0">
        <table class="table">
          <caption class="visually-hidden">
            Kampanyalar, en yeniden en eskiye
          </caption>
          <thead>
            <tr>
              <th scope="col">Brief</th>
              <th scope="col">Durum</th>
              <th scope="col" class="right">Kitle</th>
              <th scope="col">Uyarı</th>
              <th scope="col">Oluşturulma</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="c in campaigns" :key="c.id">
              <th scope="row" class="brief">
                <RouterLink :to="{ name: 'campaign', params: { id: c.id } }">{{ truncate(c.brief, 110) }}</RouterLink>
              </th>
              <td><StatusBadge :tone="STATUS_LABELS[c.status].tone" :label="STATUS_LABELS[c.status].label" /></td>
              <td class="right num" data-col="audience">
                <template v-if="c.audience">{{ formatNumber(c.audience.matched) }}<span class="visually-hidden"> kullanıcı</span></template>
                <template v-else>
                  <span aria-hidden="true">–</span><span class="visually-hidden">Henüz yok</span>
                </template>
              </td>
              <td data-col="warnings">
                <StatusBadge v-if="c.warning_count > 0" tone="warning" :label="`${formatNumber(c.warning_count)} uyarı`" />
                <span v-else class="none">Yok</span>
              </td>
              <td class="num" data-col="created">{{ formatDateTime(c.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </PageColumn>
</template>

<style scoped>
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-block-end: var(--space-4);
}

.empty {
  display: grid;
  gap: var(--space-3);
  justify-items: start;
  padding: var(--space-5);
  border: 1px dashed var(--border-control);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.scroll {
  overflow-x: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.table {
  width: 100%;
  min-width: 40rem;
  border-collapse: collapse;
}

.table th,
.table td {
  padding: var(--space-3) var(--space-4);
  border-block-end: 1px solid var(--border);
  text-align: start;
  vertical-align: middle;
}

.table thead th {
  background: var(--bg-sunken);
  font-size: var(--text-sm);
  white-space: nowrap;
}

.table tbody tr:last-child > * {
  border-block-end: 0;
}

.table .right {
  text-align: end;
}

.brief {
  max-width: 32rem;
  font-weight: 400;
  overflow-wrap: anywhere;
}

.none {
  color: var(--text-soft);
}

.num {
  white-space: nowrap;
}
</style>

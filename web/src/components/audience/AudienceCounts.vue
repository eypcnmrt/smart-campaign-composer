<script setup lang="ts">
import type { Audience } from '@/api/types'
import { formatDateTime, formatNumber } from '@/domain/format'

// The four numbers of the audience, as counted when the segment was made. They are not live.
defineProps<{ audience: Audience }>()
</script>

<template>
  <div>
    <dl class="counts">
      <div class="count">
        <dt>Kurala uyan kullanıcı</dt>
        <dd class="num count__value" data-count="matched">{{ formatNumber(audience.matched) }}</dd>
      </div>
      <div class="count">
        <dt>SMS izni olan</dt>
        <dd class="num count__value" data-count="sms">{{ formatNumber(audience.sms) }}</dd>
      </div>
      <div class="count">
        <dt>E-posta izni olan</dt>
        <dd class="num count__value" data-count="email">{{ formatNumber(audience.email) }}</dd>
      </div>
      <div class="count">
        <dt>Toplam kullanıcı</dt>
        <dd class="num count__value" data-count="total">{{ formatNumber(audience.total_users) }}</dd>
      </div>
    </dl>
    <p class="when">Sayım zamanı: <span class="num">{{ formatDateTime(audience.counted_at) }}</span>. Sayılar canlı değildir.</p>
  </div>
</template>

<style scoped>
.counts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.count {
  display: grid;
  gap: var(--space-1);
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.count dt {
  color: var(--text-soft);
  font-size: var(--text-sm);
}

.count__value {
  margin: 0;
  font-size: var(--text-2xl);
  font-weight: 650;
  line-height: var(--leading-tight);
}

.when {
  margin-block-start: var(--space-2);
  color: var(--text-soft);
  font-size: var(--text-sm);
}

@media (min-width: 40rem) {
  .counts {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}
</style>

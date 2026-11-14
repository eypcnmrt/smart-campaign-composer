<script setup lang="ts">
import type { ConfigState } from '@/composables/useConfig'
import LlmBadge from './LlmBadge.vue'

defineProps<{ configState: ConfigState }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <header class="header">
    <div class="header__inner">
      <RouterLink class="brand" :to="{ name: 'campaigns' }">
        <svg class="brand__mark" viewBox="0 0 32 32" aria-hidden="true" focusable="false">
          <rect class="mark-bg" width="32" height="32" rx="7" />
          <rect class="mark-line" x="7" y="8" width="13" height="3" rx="1.5" />
          <rect class="mark-line" x="7" y="14" width="18" height="3" rx="1.5" />
          <rect class="mark-hl" x="7" y="20" width="12" height="5" rx="1.5" />
        </svg>
        <span class="brand__name">Smart Campaign Composer</span>
      </RouterLink>

      <nav class="nav" aria-label="Ana menü">
        <RouterLink class="nav__link" :to="{ name: 'campaigns' }">Kampanyalar</RouterLink>
        <RouterLink class="btn btn--primary btn--sm" :to="{ name: 'campaign-new' }">Yeni kampanya</RouterLink>
      </nav>

      <!-- The region exists before its content, so a screen reader announces the change when the configuration arrives. -->
      <div class="model" aria-live="polite">
        <LlmBadge v-if="configState.status === 'ready'" :mode="configState.config.llm_mode" :model="configState.config.model" />
        <span v-else-if="configState.status === 'error'" class="model__error">
          Sunucuya ulaşılamıyor.
          <button type="button" class="link-button" @click="$emit('retry')">Yeniden dene</button>
        </span>
        <span v-else class="model__status">Model bilgisi yükleniyor…</span>
      </div>
    </div>
  </header>
</template>

<style scoped>
.header {
  background: var(--bg-surface);
  border-bottom: 1px solid var(--border);
}

.header__inner {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-5);
  max-width: var(--page-max);
  min-height: var(--header-h);
  margin-inline: auto;
  padding: var(--space-2) var(--gutter);
}

.brand {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text);
  text-decoration: none;
}

.brand:hover {
  color: var(--text);
}

.brand__mark {
  width: 1.75rem;
  height: 1.75rem;
  flex: none;
}

/* Colours come from tokens through classes: a CSS variable does not work in an SVG attribute, and the page policy
   forbids inline styles. */
.mark-bg {
  fill: var(--action);
}

.mark-line {
  fill: var(--on-action);
}

.mark-hl {
  fill: var(--mark);
}

.brand__name {
  font-family: var(--font-display);
  font-size: var(--text-lg);
  font-weight: 700;
  letter-spacing: -0.01em;
}

.nav {
  display: flex;
  align-items: center;
  gap: var(--space-4);
}

.nav__link {
  padding: var(--space-1) 0;
  border-bottom: 2px solid transparent;
  color: var(--text-soft);
  font-weight: 600;
  text-decoration: none;
}

.nav__link:hover {
  color: var(--text);
}

.nav__link[aria-current='page'] {
  border-bottom-color: var(--action);
  color: var(--text);
}

.model {
  margin-inline-start: auto;
  font-size: var(--text-sm);
}

.model__status {
  color: var(--text-soft);
}

.model__error {
  color: var(--danger);
  font-weight: 600;
}

@media (max-width: 40rem) {
  .model {
    flex-basis: 100%;
    margin-inline-start: 0;
  }
}
</style>

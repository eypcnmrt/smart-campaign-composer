<script setup lang="ts">
import { nextTick, onMounted, useTemplateRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppHeader from '@/components/AppHeader.vue'
import { useConfig } from '@/composables/useConfig'

const { state, load } = useConfig()
const route = useRoute()
const main = useTemplateRef<HTMLElement>('main')

// The configuration (limits, variables, the model) is read once when the app opens.
onMounted(() => {
  void load()
})

function focusMain() {
  main.value?.focus({ preventScroll: true })
}

// After a navigation the keyboard and screen reader user continues at the new page, not at the link they pressed.
watch(
  () => route.path,
  async () => {
    await nextTick()
    focusMain()
  },
)
</script>

<template>
  <div class="app">
    <a class="skip-link" href="#main" @click.prevent="focusMain">Ana içeriğe geç</a>
    <AppHeader :config-state="state" @retry="load" />
    <main id="main" ref="main" class="page" tabindex="-1">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.app {
  display: flex;
  flex-direction: column;
  min-height: 100dvh;
}

.page {
  flex: 1;
  width: 100%;
  max-width: var(--page-max);
  margin-inline: auto;
  padding: var(--space-5) var(--gutter) var(--space-7);
}

/* The container takes focus after navigation; a ring around the whole page would only be noise. */
.page:focus {
  outline: none;
}

.skip-link {
  position: absolute;
  inset-block-start: var(--space-2);
  inset-inline-start: var(--space-2);
  z-index: 10;
  padding: var(--space-2) var(--space-4);
  border-radius: var(--radius-sm);
  background: var(--text);
  color: var(--bg-surface);
  font-weight: 600;
  transform: translateY(-200%);
}

.skip-link:focus {
  transform: none;
}
</style>

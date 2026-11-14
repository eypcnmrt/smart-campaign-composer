<script setup lang="ts">
import { nextTick, onErrorCaptured, onMounted, shallowRef, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppHeader from '@/components/AppHeader.vue'
import StatusNote from '@/components/ui/StatusNote.vue'
import { useConfig } from '@/composables/useConfig'

const { state, load } = useConfig()
const route = useRoute()
const router = useRouter()
const main = useTemplateRef<HTMLElement>('main')

// The configuration (limits, variables, the model) is read once when the app opens.
onMounted(() => {
  void load()
})

// Two failures that would otherwise leave the page blank or a link doing nothing, with the cause only in the console:
//  * a page that could not be loaded (the app was updated while this tab was open, so the file it asks for is gone, or the
//    connection dropped): the navigation fails and the link seems dead;
//  * an error while a component renders or reacts, which stops that part of the page.
// Both say so and offer a reload. The error is still passed on, so it is also logged.
const fault = shallowRef<'load' | 'render' | null>(null)
router.onError(() => {
  fault.value = 'load'
})
router.afterEach((_to, _from, failure) => {
  if (!failure && fault.value === 'load') fault.value = null
})
onErrorCaptured(() => {
  fault.value = 'render'
})

function reload(): void {
  window.location.reload()
}

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
      <StatusNote v-if="fault" tone="danger" :title="fault === 'load' ? 'Sayfa yüklenemedi' : 'Sayfada bir sorun oluştu'" announce>
        <p>
          {{
            fault === 'load'
              ? 'Uygulama güncellenmiş ya da bağlantı kopmuş olabilir.'
              : 'Bu bölüm beklenmedik bir hata verdi. Sunucuya kaydedilenler yerinde; sayfayı yenileyince kampanya kaldığı adımdan açılır.'
          }}
        </p>
        <p><button type="button" class="btn btn--sm" @click="reload">Sayfayı yenile</button></p>
      </StatusNote>
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

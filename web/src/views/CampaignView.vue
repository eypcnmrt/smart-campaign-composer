<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { generateSegment } from '@/api/campaigns'
import AudienceCard from '@/components/audience/AudienceCard.vue'
import BriefForm from '@/components/brief/BriefForm.vue'
import StepNav from '@/components/StepNav.vue'
import ErrorPanel from '@/components/ui/ErrorPanel.vue'
import LoadingState from '@/components/ui/LoadingState.vue'
import { useCampaign } from '@/composables/useCampaign'
import { useConfig } from '@/composables/useConfig'
import { resolveStep, stepItems } from '@/domain/steps'

// One campaign. The step is derived from the campaign's status on the server; the URL (?step=) only chooses among the steps
// that status allows, and is corrected when it names one that is not open. So a reload or a shared link opens where the
// campaign stands.
const props = defineProps<{ id: number }>()
const route = useRoute()
const router = useRouter()
const { state: configState, load: loadConfig } = useConfig()
const { campaign, status, error, load, replace } = useCampaign(() => props.id)

void loadConfig()

const step = computed(() => (campaign.value ? resolveStep(route.query.step, campaign.value.status) : null))
const items = computed(() => (campaign.value && step.value ? stepItems(step.value, campaign.value.status) : []))
// The delivery dashboard has a table and two channels side by side; the other steps are forms and reading.
const wide = computed(() => step.value === 'send')

// Keep the URL honest: a step that is not open (an old link, a typed address) is replaced by the one that is shown.
watch(
  [step, () => route.query.step],
  ([resolved, requested]) => {
    if (resolved !== null && requested !== resolved) {
      void router.replace({ name: 'campaign', params: { id: props.id }, query: { ...route.query, step: resolved } })
    }
  },
  { immediate: true },
)

// After a step change the keyboard and screen reader user continues at the new step's heading, not at the link they pressed.
const body = shallowRef<HTMLElement | null>(null)
let shown: string | null = null
watch(step, async (now) => {
  const first = shown === null
  shown = now
  if (first || now === null) return
  await nextTick()
  body.value?.querySelector<HTMLElement>('h2')?.focus({ preventScroll: false })
})

function goTo(next: string): Promise<unknown> {
  return router.push({ name: 'campaign', params: { id: props.id }, query: { step: next } })
}

// The audience of a draft (its generation failed) is made again from here.
const retrying = shallowRef(false)
const retryError = shallowRef<unknown>(null)
let retryRequest: AbortController | null = null
onBeforeUnmount(() => retryRequest?.abort())

async function retrySegment(): Promise<void> {
  if (retrying.value) return
  retryRequest?.abort()
  const mine = new AbortController()
  retryRequest = mine
  retrying.value = true
  retryError.value = null
  try {
    const next = await generateSegment(props.id, mine.signal)
    if (mine.signal.aborted) return
    replace(next)
    await router.replace({ name: 'campaign', params: { id: props.id }, query: { step: 'audience' } })
  } catch (err) {
    if (mine.signal.aborted) return
    retryError.value = err
  } finally {
    if (retryRequest === mine) retrying.value = false
  }
}
</script>

<template>
  <div class="layout" :class="{ 'layout--wide': wide }">
    <template v-if="campaign && step">
      <StepNav :items="items" :campaign-id="campaign.id" />
      <div ref="body" class="body">
        <h1 class="visually-hidden">Kampanya {{ campaign.id }}</h1>
        <BriefForm
          v-if="step === 'brief' && configState.status === 'ready'"
          :mode="campaign.status === 'draft' ? 'draft' : 'view'"
          :limit="configState.config.limits.brief"
          :brief="campaign.brief"
          :busy="retrying"
          :error="retryError"
          @retry="retrySegment"
        />
        <AudienceCard
          v-else-if="step === 'audience'"
          :campaign="campaign"
          @updated="replace"
          @content="goTo('content')"
        />
        <p v-else-if="step !== 'brief'">Bu adım hazırlanıyor.</p>
        <LoadingState v-else label="Ayarlar yükleniyor…" />
      </div>
    </template>
    <div v-else class="body">
      <ErrorPanel v-if="status === 'error'" :error="error" @retry="load">
        <p>
          <RouterLink :to="{ name: 'campaigns' }">Kampanyalara dön</RouterLink>
        </p>
      </ErrorPanel>
      <LoadingState v-else label="Kampanya yükleniyor…" />
    </div>
  </div>
</template>

<style scoped>
.layout {
  display: grid;
  /* One column that may shrink; without minmax(0, ...) a wide child would stretch the whole page past the screen. */
  grid-template-columns: minmax(0, 1fr);
  gap: var(--space-4);
  max-width: var(--column-wide);
}

.body {
  min-width: 0;
  max-width: var(--column-narrow);
}

.layout--wide .body {
  max-width: none;
}

@media (min-width: 60rem) {
  .layout {
    grid-template-columns: var(--rail-w) minmax(0, 1fr);
    gap: var(--space-5);
  }
}
</style>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, shallowRef } from 'vue'
import { useRouter } from 'vue-router'
import { createCampaign } from '@/api/campaigns'
import { describeError } from '@/api/errors'
import BriefForm from '@/components/brief/BriefForm.vue'
import StepNav from '@/components/StepNav.vue'
import ErrorPanel from '@/components/ui/ErrorPanel.vue'
import LoadingState from '@/components/ui/LoadingState.vue'
import { useConfig } from '@/composables/useConfig'
import { newCampaignItems } from '@/domain/steps'

// A campaign that does not exist yet: the brief is written here, and the server makes the draft and its audience in one
// request. From there the campaign page takes over, at the audience step.
const router = useRouter()
const { state, load } = useConfig()

const busy = shallowRef(false)
const error = shallowRef<unknown>(null)
let request: AbortController | null = null

// The draft a failed creation left behind (the brief is not lost), if the server says there is one.
const draftId = computed(() => (error.value === null ? null : describeError(error.value).campaignId))

onMounted(() => {
  void load()
})
onBeforeUnmount(() => request?.abort())

async function submit(brief: string): Promise<void> {
  if (busy.value) return
  request?.abort()
  const mine = new AbortController()
  request = mine
  busy.value = true
  error.value = null
  try {
    const campaign = await createCampaign(brief, mine.signal)
    if (mine.signal.aborted) return
    await router.replace({ name: 'campaign', params: { id: campaign.id }, query: { step: 'audience' } })
  } catch (err) {
    if (mine.signal.aborted) return
    error.value = err
  } finally {
    if (request === mine) busy.value = false
  }
}
</script>

<template>
  <div class="layout">
    <StepNav :items="newCampaignItems()" :campaign-id="null" />
    <div class="body">
      <h1 class="visually-hidden">Yeni kampanya</h1>
      <BriefForm
        v-if="state.status === 'ready'"
        mode="create"
        :limit="state.config.limits.brief"
        :busy="busy"
        :error="error"
        :draft-id="draftId"
        @submit="submit"
      />
      <ErrorPanel v-else-if="state.status === 'error'" :error="state.error" @retry="load" />
      <LoadingState v-else label="Ayarlar yükleniyor…" />
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

@media (min-width: 60rem) {
  .layout {
    grid-template-columns: var(--rail-w) minmax(0, 1fr);
    gap: var(--space-5);
  }
}
</style>

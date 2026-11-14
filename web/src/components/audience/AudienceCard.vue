<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, useTemplateRef } from 'vue'
import { generateSegment, setChannels } from '@/api/campaigns'
import { mayBeOutOfDate } from '@/api/errors'
import type { Campaign } from '@/api/types'
import ErrorPanel from '@/components/ui/ErrorPanel.vue'
import StatusNote from '@/components/ui/StatusNote.vue'
import { useConfig } from '@/composables/useConfig'
import AudienceCounts from './AudienceCounts.vue'
import ChannelToggles from './ChannelToggles.vue'
import RuleConditions from './RuleConditions.vue'
import WarningList from './WarningList.vue'

// The second step: who the campaign goes to. The rule in words, why the model chose it, how many users it reaches, what is
// wrong with it, and the two things the marketer can change here: the channels, and the whole segment (made again from the
// brief). Content generation never starts by itself: "İçeriği üret" is the only way in, so opening the page costs no model call.
const props = defineProps<{ campaign: Campaign }>()
const emit = defineEmits<{
  /** An action returned a new campaign. */
  updated: [campaign: Campaign]
  /** The marketer wants the content step. */
  content: []
  /** An action failed in a way that may have left this screen out of date: read the campaign again. */
  reload: []
}>()

const { state: configState } = useConfig()
const available = computed(() => (configState.value.status === 'ready' ? configState.value.config.channels : ['sms', 'email']))

const segment = computed(() => props.campaign.segment)
const hasContent = computed(() => props.campaign.content !== null)
const noRecipients = computed(() => props.campaign.warnings.some((w) => w.code === 'no_recipients'))

const heading = useTemplateRef<HTMLElement>('heading')
defineExpose({ focusHeading: () => heading.value?.focus() })

// One request at a time: a channel change and a new segment both rewrite the audience, so they never overlap.
const busy = shallowRef<'channels' | 'segment' | null>(null)
const error = shallowRef<unknown>(null)
// Said once an action is over, in the live region.
const done = shallowRef('')
let request: AbortController | null = null
onBeforeUnmount(() => request?.abort())

async function run(kind: 'channels' | 'segment', action: (signal: AbortSignal) => Promise<Campaign>): Promise<void> {
  if (busy.value !== null) return
  const mine = new AbortController()
  request = mine
  busy.value = kind
  error.value = null
  done.value = ''
  try {
    const next = await action(mine.signal)
    if (mine.signal.aborted) return
    emit('updated', next)
    done.value = kind === 'segment' ? 'Segment yeniden üretildi.' : 'Kanallar kaydedildi.'
  } catch (err) {
    if (mine.signal.aborted) return
    error.value = err
    if (mayBeOutOfDate(err)) emit('reload')
  } finally {
    if (request === mine) {
      request = null
      busy.value = null
    }
  }
}

const changeChannels = (channels: string[]) => run('channels', (signal) => setChannels(props.campaign.id, channels, signal))
const regenerate = () => run('segment', (signal) => generateSegment(props.campaign.id, signal))
</script>

<template>
  <section class="audience" aria-labelledby="audience-title">
    <h2 id="audience-title" ref="heading" tabindex="-1">Kitle</h2>

    <template v-if="segment && campaign.audience">
      <div class="block">
        <h3>Kural</h3>
        <RuleConditions :conditions="segment.conditions" />
        <p class="why">{{ segment.explanation }}</p>
      </div>

      <AudienceCounts :audience="campaign.audience" />

      <WarningList :warnings="campaign.warnings" :audience="campaign.audience" />

      <StatusNote v-if="campaign.segment_changed_after_content" tone="info" title="Segment değişti">
        <p>Segment değişti; içeriği yeniden üretmek ister misin? Mevcut metin eski kitle için yazılmıştı.</p>
      </StatusNote>

      <ChannelToggles
        :available="available"
        :selected="campaign.channels"
        :busy="busy !== null"
        @change="changeChannels"
      />

      <ErrorPanel v-if="error" :error="error" :show-retry="false" />
      <p class="visually-hidden" role="status">{{ busy === 'segment' ? 'Segment yeniden üretiliyor.' : busy === 'channels' ? 'Kanallar kaydediliyor.' : done }}</p>

      <details class="raw">
        <summary>Ham kural (JSON)</summary>
        <pre class="raw__json" tabindex="0">{{ JSON.stringify(segment, null, 2) }}</pre>
      </details>

      <div class="actions">
        <button type="button" class="btn btn--primary" :disabled="busy !== null || (!hasContent && noRecipients)" @click="emit('content')">
          {{ hasContent ? 'İçeriğe geç' : 'İçeriği üret' }}
        </button>
        <button type="button" class="btn" :disabled="busy !== null" @click="regenerate">
          {{ busy === 'segment' ? 'Segment üretiliyor…' : 'Segmenti yeniden üret' }}
        </button>
      </div>
      <p v-if="!hasContent && noRecipients" class="reason">Kimse mesaj alamayacağı için içerik üretilmiyor. Önce bir kanal açın ya da segmenti yeniden üretin.</p>
    </template>
    <StatusNote v-else tone="warning" title="Kitle henüz yok">
      <p>Bu kampanyanın kitlesi oluşturulmamış.</p>
    </StatusNote>
  </section>
</template>

<style scoped>
.audience {
  display: grid;
  gap: var(--space-4);
}

.audience h2:focus {
  outline: none;
}

.block {
  display: grid;
  gap: var(--space-3);
}

.why {
  color: var(--text-soft);
}

.raw summary {
  cursor: pointer;
  font-weight: 600;
}

.raw__json {
  max-height: 20rem;
  margin: var(--space-2) 0 0;
  padding: var(--space-3);
  overflow: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-sunken);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.reason {
  color: var(--text-soft);
  font-size: var(--text-sm);
}
</style>

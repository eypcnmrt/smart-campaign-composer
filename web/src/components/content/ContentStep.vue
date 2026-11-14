<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { saveContent } from '@/api/campaigns'
import { ApiError } from '@/api/client'
import { describeError, mayBeOutOfDate } from '@/api/errors'
import type { Campaign, Content, ContentField } from '@/api/types'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import ErrorPanel from '@/components/ui/ErrorPanel.vue'
import LoadingState from '@/components/ui/LoadingState.vue'
import StatusNote from '@/components/ui/StatusNote.vue'
import { useConfig } from '@/composables/useConfig'
import { useContentStream } from '@/composables/useContentStream'
import { validateContent } from '@/domain/template'
import FieldEditor from './FieldEditor.vue'

// The third step: the SMS and the e-mail the model wrote for this audience, shown as they are written, then edited by hand.
//  * Generation starts only on a request: `autostart` is set by the audience step's "İçeriği üret" button, never by opening
//    the page, so a reload or a shared link costs no model call.
//  * While the model writes, the same fields show its text, read-only and with live counters; the editor is locked.
//  * A new generation replaces the text. When that would throw away the marketer's work (an edited text, or edits not saved
//    yet) the replacement waits for a confirmation.
const props = defineProps<{ campaign: Campaign; autostart?: boolean }>()
const emit = defineEmits<{
  updated: [campaign: Campaign]
  /** Read the campaign again (the content exists already, or the screen is out of date). */
  reload: []
  approve: []
}>()

const { state: configState, load: loadConfig } = useConfig()

const FIELDS: readonly { field: ContentField; label: string; channel: 'sms' | 'email'; multiline: boolean }[] = [
  { field: 'sms', label: 'SMS metni', channel: 'sms', multiline: true },
  { field: 'email_subject', label: 'E-posta konusu', channel: 'email', multiline: false },
  { field: 'email_body', label: 'E-posta gövdesi', channel: 'email', multiline: true },
]

const stream = useContentStream()
const streaming = computed(() => stream.status.value === 'streaming')

// The marketer's working copy. It follows the campaign whenever the campaign's content changes (generated, saved, reloaded).
const EMPTY: Content = { sms: '', email_subject: '', email_body: '' }
const local = shallowRef<Content>({ ...(props.campaign.content ?? EMPTY) })
watch(
  () => props.campaign.content,
  (content) => {
    local.value = { ...(content ?? EMPTY) }
    serverProblems.value = {}
  },
)

const dirty = computed(() => {
  const saved = props.campaign.content
  return saved !== null && FIELDS.some((f) => local.value[f.field] !== saved[f.field])
})

const shown = computed<Content>(() => (streaming.value ? stream.fields.value : local.value))

function edit(field: ContentField, value: string): void {
  local.value = { ...local.value, [field]: value }
  // The sentence the server gave for this field was about the old text.
  if (serverProblems.value[field]) {
    const { [field]: _gone, ...rest } = serverProblems.value
    serverProblems.value = rest
  }
  saved.value = false
}

// ---- Generation ----

const confirmOpen = shallowRef(false)
let lastRegenerate = false

async function run(regenerate: boolean): Promise<void> {
  lastRegenerate = regenerate
  saved.value = false
  const next = await stream.start(props.campaign.id, { regenerate })
  if (next) emit('updated', next)
  // The text may have been saved before the connection broke: show what the server has, not an empty step.
  else if (mayBeOutOfDate(stream.error.value)) emit('reload')
}

/** Replacing text the marketer wrote or changed asks first; replacing the model's own untouched text does not. */
function generate(regenerate: boolean): void {
  if (regenerate && (props.campaign.content_origin === 'edited' || dirty.value)) {
    confirmOpen.value = true
    return
  }
  void run(regenerate)
}

function confirmed(): void {
  confirmOpen.value = false
  void run(true)
}

onMounted(() => {
  if (props.autostart && props.campaign.content === null) void run(false)
})

const hasContent = computed(() => props.campaign.content !== null)
// The note is about a page that is out of date; once the campaign has been read again (and has its content) it no longer applies.
const contentExists = computed(
  () => !hasContent.value && stream.error.value instanceof ApiError && stream.error.value.code === 'content_exists',
)

// ---- Save ----

const saving = shallowRef(false)
const saved = shallowRef(false)
const saveError = shallowRef<unknown>(null)
const serverProblems = shallowRef<Partial<Record<ContentField, string[]>>>({})
let saveRequest: AbortController | null = null
onBeforeUnmount(() => saveRequest?.abort())

const variables = computed(() => (configState.value.status === 'ready' ? configState.value.config.variables : []))
const limits = computed(() => {
  const l = configState.value.status === 'ready' ? configState.value.config.limits : { sms: 160, email_subject: 80, email_body: 2000 }
  return { sms: l.sms, email_subject: l.email_subject, email_body: l.email_body }
})
const localProblems = computed(() => validateContent(local.value, limits.value, variables.value))
const canSave = computed(() => dirty.value && !saving.value && !streaming.value && Object.keys(localProblems.value).length === 0)

// A refusal that names fields goes beside them; any other failure gets the panel.
const panelError = computed(() => {
  const err = saveError.value
  if (err === null) return null
  const d = describeError(err, { variables: variables.value })
  return err instanceof ApiError && err.code === 'validation_failed' && d.fields.length > 0 ? null : err
})

async function save(): Promise<void> {
  if (!canSave.value) return
  saveRequest?.abort()
  const mine = new AbortController()
  saveRequest = mine
  saving.value = true
  saveError.value = null
  saved.value = false
  try {
    const next = await saveContent(props.campaign.id, { ...local.value }, mine.signal)
    if (mine.signal.aborted) return
    emit('updated', next)
    saved.value = true
  } catch (err) {
    if (mine.signal.aborted) return
    saveError.value = err
    if (mayBeOutOfDate(err)) emit('reload')
    const byField: Partial<Record<ContentField, string[]>> = {}
    for (const f of describeError(err, { variables: variables.value }).fields) {
      const field = FIELDS.find((x) => x.field === f.path)?.field
      if (field) byField[field] = [...(byField[field] ?? []), f.text]
    }
    serverProblems.value = byField
  } finally {
    if (saveRequest === mine) {
      saveRequest = null
      saving.value = false
    }
  }
}

// ---- Leaving with unsaved edits ----

// Following a link of the step rail while the edits are unsaved would lose them without a word: ask first.
const CONFIRM_LEAVE = 'Kaydedilmemiş değişiklikleriniz var. Bu adımdan ayrılırsanız kaybolacak. Ayrılmak istiyor musunuz?'
onBeforeRouteUpdate((to) => {
  if (dirty.value && to.query.step !== 'content') return window.confirm(CONFIRM_LEAVE)
  return true
})
// Leaving the campaign (the header's links, the browser's back button) loses the edits just the same.
onBeforeRouteLeave(() => {
  if (dirty.value) return window.confirm(CONFIRM_LEAVE)
  return true
})

function warnBeforeUnload(event: BeforeUnloadEvent): void {
  if (dirty.value) event.preventDefault()
}
onMounted(() => window.addEventListener('beforeunload', warnBeforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', warnBeforeUnload))

// Why a button is disabled, in words beside it.
const saveReason = computed(() => {
  if (saving.value) return ''
  if (!dirty.value) return 'Kaydedilecek değişiklik yok'
  return Object.keys(localProblems.value).length > 0 ? 'Önce hataları düzeltin' : ''
})
const approveReason = computed(() => (dirty.value ? 'Önce değişiklikleri kaydedin' : ''))

const progress = computed(() => {
  if (!streaming.value) return ''
  const n = stream.attempt.value
  return n > 1 ? `Model çıktıyı düzeltiyor (deneme ${n})…` : 'Model yazıyor…'
})
const channelOpen = (channel: 'sms' | 'email') => props.campaign.channels.includes(channel)

defineExpose({ dirty })
</script>

<template>
  <section class="content" aria-labelledby="content-title">
    <h2 id="content-title" tabindex="-1">İçerik</h2>

    <ErrorPanel v-if="configState.status === 'error'" :error="configState.error" @retry="loadConfig" />
    <LoadingState v-else-if="configState.status !== 'ready'" label="Ayarlar yükleniyor…" />

    <template v-else>
      <StatusNote v-if="campaign.segment_changed_after_content && !streaming" tone="info" title="Segment değişti">
        <p>Segment değişti; içeriği yeniden üretmek ister misin? Mevcut metin eski kitle için yazılmıştı.</p>
      </StatusNote>

      <div v-if="!hasContent && !streaming" class="empty">
        <p>Bu kampanya için henüz içerik yok. Model, kitleye uygun bir SMS ile bir e-posta yazar; yazarken izleyebilirsiniz.</p>
        <button type="button" class="btn btn--primary" @click="generate(false)">İçeriği üret</button>
      </div>

      <template v-if="hasContent || streaming">
        <div class="status" aria-live="polite">
          <p v-if="streaming" class="status__line">
            <span class="status__live" aria-hidden="true" />{{ progress }}
          </p>
        </div>

        <form class="editors" :aria-busy="streaming || saving" @submit.prevent="save">
          <FieldEditor
            v-for="f in FIELDS"
            :key="f.field"
            :model-value="shown[f.field]"
            :field="f.field"
            :label="f.label"
            :limit="limits[f.field]"
            :variables="variables"
            :multiline="f.multiline"
            :disabled="streaming || saving"
            :server-problems="serverProblems[f.field] ?? []"
            :hint="channelOpen(f.channel) ? undefined : 'Bu kanal kapalı; bu metin gönderilmeyecek.'"
            @update:model-value="edit(f.field, $event)"
          />

          <div v-if="streaming && stream.raw.value" class="raw">
            <p class="raw__title">Model çıktısı alanlara ayrılamadı; ham metin gösteriliyor</p>
            <pre class="raw__text" tabindex="0">{{ stream.raw.value }}</pre>
          </div>

          <div class="actions">
            <!-- One primary action: "Onaya geç" while nothing is unsaved, "Değişiklikleri kaydet" while something is. A disabled
                 button says why, next to it. -->
            <div v-if="!streaming" class="action">
              <button
                type="submit"
                class="btn"
                :class="{ 'btn--primary': dirty }"
                :disabled="!canSave"
                :aria-describedby="saveReason ? 'content-save-reason' : undefined"
              >
                {{ saving ? 'Kaydediliyor…' : 'Değişiklikleri kaydet' }}
              </button>
              <p v-if="saveReason" id="content-save-reason" class="reason">{{ saveReason }}</p>
            </div>
            <button v-else type="button" class="btn" @click="stream.abort()">Üretimi durdur</button>
            <button v-if="hasContent && !streaming" type="button" class="btn" :disabled="saving" @click="generate(true)">
              İçeriği yeniden üret
            </button>
            <div v-if="hasContent && !streaming && campaign.status === 'content_ready'" class="action">
              <button
                type="button"
                class="btn"
                :class="{ 'btn--primary': !dirty }"
                :disabled="dirty || saving"
                :aria-describedby="approveReason ? 'content-approve-reason' : undefined"
                @click="emit('approve')"
              >
                Onaya geç
              </button>
              <p v-if="approveReason" id="content-approve-reason" class="reason">{{ approveReason }}</p>
            </div>
          </div>
          <p class="visually-hidden" role="status">{{ saved ? 'İçerik kaydedildi.' : '' }}</p>
        </form>
      </template>

      <ErrorPanel v-if="stream.status.value === 'error' && !contentExists" :error="stream.error.value" @retry="run(lastRegenerate)" />
      <StatusNote v-if="contentExists" tone="warning" title="Bu kampanyanın içeriği zaten var" announce>
        <p>Siz sayfayı açtıktan sonra içerik üretilmiş. Mevcut içeriği görebilir ya da yenisini yazdırabilirsiniz.</p>
        <p class="actions">
          <button type="button" class="btn btn--sm" @click="emit('reload')">Mevcut içeriği göster</button>
          <button type="button" class="btn btn--sm" @click="confirmOpen = true">Yeniden üret</button>
        </p>
      </StatusNote>
      <ErrorPanel v-if="panelError" :error="panelError" :show-retry="false" />
      <StatusNote v-if="saveError && !panelError" tone="danger" title="Bazı alanlar geçersiz" announce>
        <p>İşaretli alanlardaki açıklamalara göre düzeltip tekrar kaydedin.</p>
      </StatusNote>
    </template>

    <ConfirmDialog :open="confirmOpen" title="İçerik yeniden üretilsin mi?" confirm-label="Yeniden üret" @confirm="confirmed" @cancel="confirmOpen = false">
      <p v-if="dirty">Kaydedilmemiş değişiklikleriniz var; yeni metin bunların yerine yazılır.</p>
      <p v-else-if="campaign.content_origin === 'edited'">Bu metni siz düzenlediniz; yeni metin düzenlemelerinizin yerine yazılır.</p>
      <p v-else>Mevcut metin yenisiyle değiştirilir.</p>
      <p>Bu işlem geri alınamaz.</p>
    </ConfirmDialog>
  </section>
</template>

<style scoped>
.content {
  display: grid;
  gap: var(--space-4);
}

.content h2:focus {
  outline: none;
}

.empty {
  display: grid;
  gap: var(--space-3);
  justify-items: start;
  padding: var(--space-4);
  border: 1px dashed var(--border-control);
  border-radius: var(--radius-md);
}

.status__line {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-weight: 600;
}

/* The highlighter marks text that is still being written: the same colour as a variable, "not final text". */
.status__live {
  width: 0.75rem;
  height: 1.1em;
  background: var(--mark);
  animation: blink 1s steps(2, start) infinite;
}

@keyframes blink {
  to {
    visibility: hidden;
  }
}

.editors {
  display: grid;
  gap: var(--space-5);
}

.raw__title {
  font-weight: 600;
}

.raw__text {
  max-height: 16rem;
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
  align-items: flex-start;
  gap: var(--space-3);
}

.action {
  display: grid;
  gap: var(--space-1);
  justify-items: start;
}

.reason {
  color: var(--text-soft);
  font-size: var(--text-sm);
}
</style>

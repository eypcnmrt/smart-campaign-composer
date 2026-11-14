<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { approveCampaign, getPreview } from '@/api/campaigns'
import { ApiError, isRecord } from '@/api/client'
import type { Campaign, PreviewSample, Warning } from '@/api/types'
import WarningList from '@/components/audience/WarningList.vue'
import ErrorPanel from '@/components/ui/ErrorPanel.vue'
import LoadingState from '@/components/ui/LoadingState.vue'
import StatusNote from '@/components/ui/StatusNote.vue'
import { formatNumber } from '@/domain/format'
import PreviewSamples from './PreviewSamples.vue'

// The fourth step: what will go out, to whom, and the one button that sends it.
//  * The samples are real users with the texts as they would receive them, as plain text.
//  * The approval carries the version of the campaign that is on screen. If the campaign changed meanwhile (another tab,
//    another person) the server refuses, the screen reloads and asks for a new look; nothing is approved unseen.
//  * Warnings must be acknowledged with a box that is ticked on purpose. "Nobody would receive anything" cannot be
//    acknowledged: the button is off and says why.
const props = defineProps<{ campaign: Campaign }>()
const emit = defineEmits<{
  updated: [campaign: Campaign]
  /** Read the campaign again: the screen may be out of date. */
  reload: []
}>()

const LABELS: Record<string, string> = { sms: 'SMS', email: 'E-posta' }

// ---- The preview, for the version on screen ----

const samples = shallowRef<PreviewSample[] | null>(null)
const previewError = shallowRef<unknown>(null)
let previewRequest: AbortController | null = null

async function loadPreview(): Promise<void> {
  previewRequest?.abort()
  const mine = new AbortController()
  previewRequest = mine
  samples.value = null
  previewError.value = null
  try {
    const list = await getPreview(props.campaign.id, 3, mine.signal)
    if (mine.signal.aborted) return
    samples.value = list
  } catch (err) {
    if (mine.signal.aborted) return
    previewError.value = err
  }
}

// Reset what the marketer ticked and read the texts again whenever the campaign on screen is a different version.
const acknowledged = shallowRef(false)
watch(
  () => props.campaign.version,
  () => {
    acknowledged.value = false
    void loadPreview()
  },
  { immediate: true },
)

// ---- What the approval means ----

const recipients = computed(() => {
  const a = props.campaign.audience
  return props.campaign.channels.map((c) => ({ channel: c, label: LABELS[c] ?? c, count: c === 'sms' ? (a?.sms ?? 0) : c === 'email' ? (a?.email ?? 0) : 0 }))
})
const total = computed(() => recipients.value.reduce((sum, r) => sum + r.count, 0))

const noRecipients = computed(() => props.campaign.warnings.some((w) => w.code === 'no_recipients'))
const acknowledgeable = computed(() => props.campaign.warnings.filter((w) => w.code !== 'no_recipients'))
const needsAcknowledgement = computed(() => acknowledgeable.value.length > 0)

const blocked = computed(() => (noRecipients.value ? 'Seçili kanallarda mesaj alacak kimse yok; bu hâliyle onaylanamaz.' : ''))
const busy = shallowRef(false)
const canApprove = computed(() => !busy.value && !noRecipients.value && samples.value !== null && (!needsAcknowledgement.value || acknowledged.value))

// ---- Approving ----

const error = shallowRef<unknown>(null)
/** The campaign changed under the marketer: said in words, and the screen is already showing the new one. */
const changed = shallowRef(false)
/** What the server said still needs acknowledging (acknowledgement_required). */
const serverWarnings = shallowRef<Warning[]>([])
let approveRequest: AbortController | null = null
onBeforeUnmount(() => {
  previewRequest?.abort()
  approveRequest?.abort()
})

function warningsOf(details: unknown): Warning[] {
  if (!isRecord(details) || !Array.isArray(details.warnings)) return []
  return details.warnings.filter(
    (w): w is Warning => isRecord(w) && typeof w.code === 'string' && typeof w.message === 'string',
  )
}

async function approve(): Promise<void> {
  if (!canApprove.value) return
  approveRequest?.abort()
  const mine = new AbortController()
  approveRequest = mine
  busy.value = true
  error.value = null
  changed.value = false
  serverWarnings.value = []
  // The version on screen: the one the marketer looked at, not whatever is newest on the server.
  const shownVersion = props.campaign.version
  try {
    const result = await approveCampaign(props.campaign.id, shownVersion, acknowledged.value, mine.signal)
    if (mine.signal.aborted) return
    // The campaign is now sending; the page derives its step from that and opens the delivery.
    emit('updated', result.campaign)
  } catch (err) {
    if (mine.signal.aborted) return
    if (err instanceof ApiError && err.code === 'version_mismatch') {
      changed.value = true
      acknowledged.value = false
    } else {
      error.value = err
      if (err instanceof ApiError && err.code === 'acknowledgement_required') serverWarnings.value = warningsOf(err.details)
    }
    // Whatever went wrong, the truth is on the server: show it. (A request that may or may not have been applied, such as a
    // dropped connection or approval_unconfirmed, is settled by the reload too: the campaign is either sending or it is not.)
    emit('reload')
  } finally {
    if (approveRequest === mine) {
      approveRequest = null
      busy.value = false
    }
  }
}
</script>

<template>
  <section class="approve" aria-labelledby="approve-title">
    <h2 id="approve-title" tabindex="-1">Onay</h2>

    <StatusNote v-if="changed" tone="warning" title="Kampanya değişti, lütfen tekrar gözden geçirin" announce>
      <p>Siz bakarken kampanya başka bir yerden değişti. Aşağıda güncel hâli var; onay kutusu sıfırlandı.</p>
    </StatusNote>

    <div class="summary">
      <h3>Özet</h3>
      <ul class="summary__channels">
        <li v-for="r in recipients" :key="r.channel">
          <strong>{{ r.label }}:</strong> <span class="num">{{ formatNumber(r.count) }}</span> alıcı
        </li>
      </ul>
      <p class="summary__total">
        Toplam <span class="num">{{ formatNumber(total) }}</span> mesaj kuyruğa alınacak. Onayladıktan sonra kampanya değiştirilemez.
      </p>
      <p class="summary__version">Gözden geçirdiğiniz sürüm: <span class="num">{{ campaign.version }}</span></p>
    </div>

    <div class="preview">
      <h3>Örnek mesajlar</h3>
      <LoadingState v-if="samples === null && previewError === null" label="Örnekler hazırlanıyor…" />
      <ErrorPanel v-else-if="previewError !== null" :error="previewError" @retry="loadPreview" />
      <PreviewSamples v-else-if="samples !== null && samples.length > 0" :samples="samples" />
      <p v-else class="none">Gösterilecek örnek kullanıcı yok.</p>
    </div>

    <WarningList :warnings="campaign.warnings" :audience="campaign.audience" />

    <StatusNote v-if="serverWarnings.length > 0" tone="warning" title="Sunucu şu uyarıların onaylanmasını istiyor" announce>
      <ul class="server-warnings">
        <li v-for="w in serverWarnings" :key="w.code" lang="en">{{ w.message }}</li>
      </ul>
    </StatusNote>

    <label v-if="needsAcknowledgement && !noRecipients" class="ack">
      <input v-model="acknowledged" type="checkbox" :disabled="busy" />
      <span>Yukarıdaki uyarıları okudum; kampanyanın yine de gönderilmesini onaylıyorum.</span>
    </label>

    <ErrorPanel v-if="error !== null" :error="error" :show-retry="false" />

    <div class="actions">
      <button type="button" class="btn btn--primary" :disabled="!canApprove" :aria-describedby="blocked ? 'approve-blocked' : undefined" @click="approve">
        {{ busy ? 'Onaylanıyor…' : 'Onayla ve gönder' }}
      </button>
    </div>
    <p v-if="blocked" id="approve-blocked" class="reason">{{ blocked }}</p>
    <p v-else-if="needsAcknowledgement && !acknowledged" class="reason">Göndermek için uyarıları onaylayan kutuyu işaretleyin.</p>
    <p class="visually-hidden" role="status">{{ busy ? 'Kampanya onaylanıyor.' : '' }}</p>
  </section>
</template>

<style scoped>
.approve {
  display: grid;
  gap: var(--space-4);
}

.approve h2:focus {
  outline: none;
}

.summary,
.preview {
  display: grid;
  gap: var(--space-2);
}

.summary__channels {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.summary__version,
.none,
.reason {
  color: var(--text-soft);
  font-size: var(--text-sm);
}

.server-warnings {
  margin: var(--space-2) 0 0;
  padding-inline-start: var(--space-5);
}

.ack {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--warning-border);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  font-weight: 600;
}

.ack input {
  flex: none;
  width: 1.25rem;
  height: 1.25rem;
  margin-block-start: 0.15rem;
  accent-color: var(--action);
}

.actions {
  display: flex;
  gap: var(--space-3);
}
</style>

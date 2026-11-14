<script setup lang="ts">
import { computed, ref, useTemplateRef } from 'vue'
import { describeError } from '@/api/errors'
import ErrorPanel from '@/components/ui/ErrorPanel.vue'
import StatusNote from '@/components/ui/StatusNote.vue'
import { useDebounced } from '@/composables/useDebounced'
import { formatNumber } from '@/domain/format'
import { runeCount } from '@/domain/sms'
import { trimSpace } from '@/domain/template'

// The first step: the brief, in the marketer's own words.
//   create  a new campaign: a textarea, a counter, examples, and the button that asks the model for the audience
//   draft   the campaign exists but its audience could not be made (the model failed): the brief is shown, with a retry
//   view    the audience exists: the brief is shown as it was written; it cannot be changed (a different brief is a new campaign)
const props = withDefaults(
  defineProps<{
    mode: 'create' | 'draft' | 'view'
    /** The limit in characters, from /api/config. It is counted as the server counts it: after trimming, in code points. */
    limit: number
    /** The brief of an existing campaign. */
    brief?: string
    /** A request is running. */
    busy?: boolean
    /** The last failure, as thrown by the API client. */
    error?: unknown
    /** When creating failed after the draft was saved: its id, to open it and retry from there. */
    draftId?: number | null
  }>(),
  { brief: '', busy: false, error: undefined, draftId: null },
)
const emit = defineEmits<{ submit: [brief: string]; retry: [] }>()

const EXAMPLES = [
  'İstanbul’da yaşayan ve son 30 günde sepete ürün ekleyip satın almayan müşterilere %10 indirim SMS’i gönder.',
  'Son 60 gündür alışveriş yapmamış, toplam harcaması 1.500 TL ve üzeri olan sadık müşterilere e-posta ile geri dönüş kampanyası hazırla.',
  'Ankara veya İzmir’deki müşterilere hafta sonu ücretsiz kargo duyurusu yap; hem SMS hem e-posta olsun.',
]

const text = ref(props.brief)
const heading = useTemplateRef<HTMLElement>('heading')
defineExpose({ focusHeading: () => heading.value?.focus() })

// The server trims the brief and then counts characters (code points). So does this.
const length = computed(() => runeCount(trimSpace(text.value)))
const over = computed(() => Math.max(0, length.value - props.limit))
const empty = computed(() => length.value === 0)
const canSubmit = computed(() => !props.busy && !empty.value && over.value === 0)

// Examples are offered while there is nothing of the marketer's own to lose.
const showExamples = computed(() => text.value === '' || EXAMPLES.includes(text.value))

/** Why the button is disabled, in words next to it (a disabled button says nothing by itself). */
const problem = computed(() => {
  if (empty.value) return 'Göndermek için brief yazın.'
  if (over.value > 0) return `Brief ${formatNumber(over.value)} karakter fazla; sınır ${formatNumber(props.limit)}.`
  return ''
})

// A screen reader hears the counter only when the limit is near, and only once typing has paused.
const near = computed(() => length.value >= props.limit * 0.9)
const announcement = useDebounced(
  () => (near.value ? `${formatNumber(length.value)} karakter yazıldı; sınır ${formatNumber(props.limit)}.` : ''),
  800,
)

const described = computed(() => (props.error ? describeError(props.error) : null))
// A refused brief is shown beside the field; every other failure gets the panel.
const fieldMessage = computed(() => described.value?.fields.find((f) => f.path === 'brief')?.text ?? '')
const showPanel = computed(() => props.error !== null && props.error !== undefined && fieldMessage.value === '')

function submit(): void {
  if (canSubmit.value) emit('submit', trimSpace(text.value))
}
</script>

<template>
  <section class="brief" aria-labelledby="brief-title">
    <h2 id="brief-title" ref="heading" tabindex="-1">Brief</h2>

    <form v-if="mode === 'create'" class="form" :aria-busy="busy" @submit.prevent="submit">
      <p class="lead">Kampanyayı kendi cümlelerinizle anlatın: kime, ne için, hangi kanaldan ulaşmak istediğinizi yazın.</p>

      <label class="label" for="brief-input">Kampanya brief’i</label>
      <textarea
        id="brief-input"
        v-model="text"
        class="textarea"
        :class="{ 'textarea--invalid': over > 0 || fieldMessage !== '' }"
        rows="6"
        :disabled="busy"
        :aria-invalid="over > 0 || fieldMessage !== ''"
        aria-describedby="brief-count brief-problem"
      />
      <div class="meta">
        <p id="brief-problem" class="problem" :class="{ 'problem--error': over > 0 || fieldMessage !== '' }">{{ fieldMessage || problem }}</p>
        <p id="brief-count" class="counter num" :class="{ 'counter--over': over > 0 }">
          {{ formatNumber(length) }} / {{ formatNumber(limit) }}<span class="visually-hidden"> karakter</span>
        </p>
      </div>
      <p class="visually-hidden" role="status">{{ announcement }}</p>

      <div v-if="showExamples" class="examples" role="group" aria-labelledby="examples-title">
        <p id="examples-title" class="examples__title">Örnek brief’ler</p>
        <ul>
          <li v-for="example in EXAMPLES" :key="example">
            <button type="button" class="example" :disabled="busy" @click="text = example">{{ example }}</button>
          </li>
        </ul>
      </div>

      <StatusNote v-if="busy" tone="info" announce>
        Model brief’i kitle kuralına çeviriyor ve kitleyi sayıyor. Bu birkaç saniye sürebilir.
      </StatusNote>
      <ErrorPanel v-if="showPanel" :error="error" :show-retry="false">
        <p v-if="draftId !== null" class="draft">
          Brief’iniz taslak olarak saklandı.
          <RouterLink :to="{ name: 'campaign', params: { id: draftId } }">Taslağı aç</RouterLink>
          ve oradan tekrar deneyin.
        </p>
      </ErrorPanel>

      <div class="actions">
        <button type="submit" class="btn btn--primary" :disabled="!canSubmit" :aria-describedby="problem ? 'brief-problem' : undefined">
          {{ busy ? 'Kitle oluşturuluyor…' : 'Kitleyi oluştur' }}
        </button>
      </div>
    </form>

    <div v-else class="saved">
      <blockquote class="saved__text">{{ brief }}</blockquote>

      <template v-if="mode === 'draft'">
        <StatusNote tone="warning" title="Kitle henüz oluşturulamadı">
          <p>Brief kaydedildi ama model kitle kuralını üretemedi. Aynı brief ile yeniden deneyebilirsiniz.</p>
        </StatusNote>
        <StatusNote v-if="busy" tone="info" announce>Model brief’i kitle kuralına çeviriyor. Bu birkaç saniye sürebilir.</StatusNote>
        <ErrorPanel v-if="error" :error="error" :show-retry="false" />
        <div class="actions">
          <button type="button" class="btn btn--primary" :disabled="busy" @click="emit('retry')">
            {{ busy ? 'Kitle oluşturuluyor…' : 'Kitleyi yeniden dene' }}
          </button>
        </div>
      </template>

      <p v-else class="hint">
        Brief kampanya oluşturulduktan sonra değiştirilemez. Farklı bir brief için
        <RouterLink :to="{ name: 'campaign-new' }">yeni kampanya oluşturun</RouterLink>.
      </p>
    </div>
  </section>
</template>

<style scoped>
.brief {
  display: grid;
  gap: var(--space-4);
}

.brief h2:focus {
  outline: none;
}

.form {
  display: grid;
  gap: var(--space-3);
}

.lead,
.hint {
  color: var(--text-soft);
}

.label {
  font-weight: 600;
}

.textarea {
  width: 100%;
  min-height: 9rem;
  padding: var(--space-3);
  border: 1px solid var(--border-control);
  border-radius: var(--radius-sm);
  background: var(--bg-surface);
  line-height: var(--leading-base);
  resize: vertical;
}

.textarea:disabled {
  background: var(--bg-sunken);
}

.textarea--invalid {
  border: 2px solid var(--danger);
}

.meta {
  display: flex;
  justify-content: space-between;
  gap: var(--space-4);
  margin-block-start: calc(-1 * var(--space-1));
  font-size: var(--text-sm);
}

/* An empty field is not an error yet: the hint is quiet until there is something wrong to say. */
.problem {
  color: var(--text-soft);
}

.problem--error {
  color: var(--danger);
  font-weight: 600;
}

.counter {
  flex: none;
  margin-inline-start: auto;
  color: var(--text-soft);
}

.counter--over {
  color: var(--danger);
  font-weight: 700;
}

.examples {
  display: grid;
  gap: var(--space-2);
}

.examples__title {
  font-weight: 600;
}

.examples ul {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

/* A button, not a drop zone: a plain frame on a white card, a tint and the action colour under the pointer, and the focus ring
   with the action colour on the frame. */
.example {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border-control);
  border-radius: var(--radius-sm);
  background: var(--bg-surface);
  color: var(--text);
  font-size: var(--text-sm);
  text-align: start;
  transition:
    background-color var(--dur-fast) var(--ease),
    border-color var(--dur-fast) var(--ease);
}

.example:hover:not(:disabled) {
  border-color: var(--action);
  background: var(--action-tint);
}

.example:focus-visible {
  border-color: var(--action);
  background: var(--action-tint);
}

.example:disabled {
  background: var(--bg-sunken);
  border-color: var(--border);
  color: var(--text-soft);
}

.actions {
  display: flex;
  gap: var(--space-3);
}

.saved {
  display: grid;
  gap: var(--space-4);
}

.saved__text {
  margin: 0;
  padding: var(--space-3) var(--space-4);
  border-inline-start: 4px solid var(--border-control);
  border-radius: var(--radius-sm);
  background: var(--bg-sunken);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.draft {
  margin-block-start: var(--space-2);
}
</style>

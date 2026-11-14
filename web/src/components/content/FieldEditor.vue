<script setup lang="ts">
import { computed, nextTick, useId, useTemplateRef, watch } from 'vue'
import { describeProblem } from '@/api/errors'
import type { ContentField } from '@/api/types'
import { useDebounced } from '@/composables/useDebounced'
import { formatNumber } from '@/domain/format'
import { analyzeSms, runeCount } from '@/domain/sms'
import { validateField } from '@/domain/template'

// One text of the campaign: SMS, e-mail subject or e-mail body. The editor
//  * counts characters the way the server does (code points) and shows the limit; it has no `maxlength`, which counts UTF-16
//    units and would cut an emoji in half, so the limit is a message and the save is refused, not a silent cut;
//  * flags a problem as it is typed (an unknown variable, a stray brace, too long), with the server's own code and params;
//  * adds a variable at the caret from a button, so nobody has to type braces;
//  * for the SMS, says how the text would be sent (encoding, units, messages): information, never a limit.
const props = defineProps<{
  field: ContentField
  label: string
  limit: number
  /** Whitelisted variable names without braces. */
  variables: readonly string[]
  multiline?: boolean
  /** Locked while the model writes, or while a save runs. */
  disabled?: boolean
  /** Sentences the server gave for this field on the last save; the marketer's next edit of the field removes them. */
  serverProblems?: readonly string[]
  /** A note under the label, e.g. that the channel is closed. */
  hint?: string
}>()

const model = defineModel<string>({ required: true })

const id = useId()
const control = useTemplateRef<HTMLTextAreaElement | HTMLInputElement>('control')

const length = computed(() => runeCount(model.value))
const over = computed(() => Math.max(0, length.value - props.limit))

const problems = computed(() => {
  const own = validateField(model.value, props.limit, props.variables).map((p) =>
    describeProblem({ path: props.field, code: p.code, message: '', params: p.params }, { variables: props.variables }),
  )
  // The server may name what the browser already named.
  const extra = (props.serverProblems ?? []).filter((s) => !own.includes(s))
  return [...own, ...extra]
})
const invalid = computed(() => problems.value.length > 0)

const sms = computed(() => (props.field === 'sms' ? analyzeSms(model.value) : null))

// A screen reader hears the counter only when the limit is close or passed, and only after typing has paused.
const near = computed(() => length.value >= props.limit * 0.9)
const announcement = useDebounced(
  () => (near.value ? `${formatNumber(length.value)} karakter; sınır ${formatNumber(props.limit)}.` : ''),
  800,
)

/** A variable as it is written in a text. */
const token = (name: string): string => '{{' + name + '}}'

// Where the caret was when the field lost focus (pressing a button takes the focus away). Text that arrives from outside (the
// model writing, a regeneration) makes it meaningless, so it is forgotten then.
let saved: { start: number; end: number } | null = null

function remember(): void {
  const el = control.value
  saved = el ? { start: el.selectionStart ?? el.value.length, end: el.selectionEnd ?? el.value.length } : null
}

watch(
  model,
  (text) => {
    // Runs before the DOM is patched: a value the marketer typed is already in the control, one from outside is not.
    if (control.value && control.value.value !== text) saved = null
  },
  { flush: 'pre' },
)

// A variable is a word of its own: a space goes between it and a neighbouring character, unless that is a space already, or
// punctuation that belongs next to it ("Merhaba {{first_name}}, ...").
const NO_SPACE_AFTER = /[([\{]$/
const NO_SPACE_BEFORE = /^[.,;:!?)\]}…]/

function spaced(before: string, inserted: string, after: string): string {
  const lead = before !== '' && !/\s$/.test(before) && !NO_SPACE_AFTER.test(before) ? ' ' : ''
  const trail = after !== '' && !/^\s/.test(after) && !NO_SPACE_BEFORE.test(after) ? ' ' : ''
  return lead + inserted + trail
}

async function insert(name: string): Promise<void> {
  const el = control.value
  if (!el || props.disabled) return
  const inserted = token(name)
  const text = model.value
  const focused = document.activeElement === el
  const from = !focused && saved ? saved : { start: el.selectionStart ?? text.length, end: el.selectionEnd ?? el.selectionStart ?? text.length }
  const start = Math.min(from.start, text.length)
  const end = Math.min(Math.max(from.end, start), text.length)
  const before = text.slice(0, start)
  const piece = spaced(before, inserted, text.slice(end))
  model.value = before + piece + text.slice(end)
  await nextTick()
  el.focus()
  // After the variable itself, before a space that was added behind it.
  const caret = start + piece.indexOf(inserted) + inserted.length
  el.setSelectionRange(caret, caret)
  saved = { start: caret, end: caret }
}

function typed(event: Event): void {
  model.value = (event.target as HTMLInputElement | HTMLTextAreaElement).value
}
</script>

<template>
  <div class="editor" :data-field="field">
    <label class="editor__label" :for="`${id}-control`">{{ label }}</label>
    <p v-if="hint" :id="`${id}-hint`" class="editor__hint">{{ hint }}</p>

    <textarea
      v-if="multiline"
      :id="`${id}-control`"
      ref="control"
      class="editor__control"
      :class="{ 'editor__control--invalid': invalid }"
      :rows="field === 'sms' ? 4 : 10"
      :value="model"
      :disabled="disabled"
      :aria-invalid="invalid"
      :aria-describedby="`${id}-count ${id}-problems${hint ? ` ${id}-hint` : ''}`"
      @blur="remember"
      @input="typed"
    />
    <input
      v-else
      :id="`${id}-control`"
      ref="control"
      type="text"
      class="editor__control"
      :class="{ 'editor__control--invalid': invalid }"
      :value="model"
      :disabled="disabled"
      :aria-invalid="invalid"
      :aria-describedby="`${id}-count ${id}-problems${hint ? ` ${id}-hint` : ''}`"
      @blur="remember"
      @input="typed"
    />

    <div class="editor__meta">
      <div class="editor__variables" role="group" :aria-label="`${label}: değişken ekle`">
        <span class="editor__variables-title" aria-hidden="true">Değişken ekle:</span>
        <button
          v-for="v in variables"
          :key="v"
          type="button"
          class="variable"
          :disabled="disabled"
          :aria-label="`${label} alanına ${v} değişkenini ekle`"
          @click="insert(v)"
        >
          <span class="mono">{{ token(v) }}</span>
        </button>
      </div>
      <p :id="`${id}-count`" class="editor__count num" :class="{ 'editor__count--over': over > 0 }">
        {{ formatNumber(length) }} / {{ formatNumber(limit) }}<span class="visually-hidden"> karakter</span>
      </p>
    </div>

    <div :id="`${id}-problems`" class="editor__problems">
      <p v-for="p in problems" :key="p" class="problem">
        <span class="visually-hidden">Hata: </span>{{ p }}
      </p>
    </div>

    <p v-if="sms" class="sms-info">
      <span class="num">{{ sms.encoding }}</span> kodlaması · <span class="num">{{ formatNumber(sms.units) }}</span> birim ·
      <span class="num">{{ formatNumber(sms.segments) }}</span> mesaj
      <span class="sms-info__why">
        (tek mesajda <span class="num">{{ formatNumber(sms.single) }}</span>, uzun mesajda parça başına
        <span class="num">{{ formatNumber(sms.multi) }}</span> birim). Yalnızca bilgi: değişkenler gerçek uzunluğu değiştirir.
      </span>
    </p>

    <p class="visually-hidden" role="status">{{ announcement }}</p>
  </div>
</template>

<style scoped>
.editor {
  display: grid;
  gap: var(--space-2);
}

.editor__label {
  font-weight: 600;
}

.editor__hint {
  color: var(--text-soft);
  font-size: var(--text-sm);
}

.editor__control {
  width: 100%;
  padding: var(--space-3);
  border: 1px solid var(--border-control);
  border-radius: var(--radius-sm);
  background: var(--bg-surface);
  line-height: var(--leading-base);
}

textarea.editor__control {
  resize: vertical;
}

.editor__control:disabled {
  background: var(--bg-sunken);
}

.editor__control--invalid {
  border: 2px solid var(--danger);
}

.editor__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
}

.editor__variables {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.editor__variables-title {
  color: var(--text-soft);
}

/* A variable is "not final text" (it becomes a name or a city per user): the highlighter colour says so. */
.variable {
  min-height: 2rem;
  padding: 0 var(--space-2);
  border: 1px solid var(--border-control);
  border-radius: var(--radius-sm);
  background: var(--mark);
  color: var(--on-mark);
}

.variable:disabled {
  background: var(--bg-sunken);
  color: var(--text-soft);
}

.editor__count {
  margin-inline-start: auto;
  color: var(--text-soft);
}

.editor__count--over {
  color: var(--danger);
  font-weight: 700;
}

.editor__problems {
  display: grid;
  gap: var(--space-1);
}

.problem {
  color: var(--danger);
  font-size: var(--text-sm);
  font-weight: 600;
}

.sms-info {
  color: var(--text-soft);
  font-size: var(--text-sm);
}
</style>

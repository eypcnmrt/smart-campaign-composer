<script setup lang="ts">
import { nextTick, useTemplateRef, watch } from 'vue'

// A question that must be answered before something that cannot be undone. A native <dialog> opened with showModal(): the
// browser traps the focus, makes the rest of the page inert and closes it on Escape (reported as cancel). Focus starts on the
// cancel button, so Enter does not confirm by accident, and returns to where it was when the dialog closes.
const props = defineProps<{ open: boolean; title: string; confirmLabel: string; cancelLabel?: string }>()
const emit = defineEmits<{ confirm: []; cancel: [] }>()

const dialog = useTemplateRef<HTMLDialogElement>('dialog')
const cancelButton = useTemplateRef<HTMLButtonElement>('cancelButton')
let opener: Element | null = null

watch(
  () => props.open,
  async (open) => {
    const el = dialog.value
    if (!el) return
    if (open && !el.open) {
      opener = document.activeElement
      el.showModal()
      await nextTick()
      cancelButton.value?.focus()
    } else if (!open && el.open) {
      el.close()
      if (opener instanceof HTMLElement) opener.focus()
      opener = null
    }
  },
  { immediate: true, flush: 'post' },
)
</script>

<template>
  <dialog ref="dialog" class="dialog" aria-labelledby="confirm-title" @cancel.prevent="emit('cancel')">
    <h2 id="confirm-title" class="dialog__title">{{ title }}</h2>
    <div class="dialog__body"><slot /></div>
    <div class="dialog__actions">
      <button ref="cancelButton" type="button" class="btn" @click="emit('cancel')">{{ cancelLabel ?? 'Vazgeç' }}</button>
      <button type="button" class="btn btn--primary" @click="emit('confirm')">{{ confirmLabel }}</button>
    </div>
  </dialog>
</template>

<style scoped>
.dialog {
  width: min(32rem, calc(100vw - 2 * var(--gutter)));
  padding: var(--space-5);
  border: 1px solid var(--border-control);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--text);
  box-shadow: var(--shadow-pop);
}

.dialog::backdrop {
  background: rgb(20 48 42 / 0.45);
}

.dialog__title {
  margin-block-end: var(--space-3);
}

.dialog__body {
  display: grid;
  gap: var(--space-2);
}

.dialog__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-3);
  margin-block-start: var(--space-5);
}
</style>

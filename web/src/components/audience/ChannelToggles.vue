<script setup lang="ts">
import { computed } from 'vue'

// Which channels the campaign goes out on. The last open channel cannot be closed here (the server refuses an empty list
// too); a channel nobody consented to is still allowed, and the server says so with the no_recipients warning.
const props = defineProps<{
  /** Every channel the system knows (from /api/config). */
  available: readonly string[]
  /** The channels that are open. */
  selected: readonly string[]
  busy: boolean
}>()
const emit = defineEmits<{ change: [channels: string[]] }>()

const LABELS: Record<string, string> = { sms: 'SMS', email: 'E-posta' }

const onlyOne = computed(() => props.selected.length <= 1)

function toggle(channel: string, on: boolean): void {
  if (props.busy) return
  const next = props.available.filter((c) => (c === channel ? on : props.selected.includes(c)))
  if (next.length === 0) return
  emit('change', next)
}
</script>

<template>
  <fieldset class="channels" :aria-busy="busy">
    <legend>Kanallar</legend>
    <label v-for="c in available" :key="c" class="channel" :class="{ 'channel--on': selected.includes(c) }">
      <input
        type="checkbox"
        :value="c"
        :checked="selected.includes(c)"
        :disabled="busy || (onlyOne && selected.includes(c))"
        :aria-describedby="onlyOne && selected.includes(c) ? 'channels-last' : undefined"
        @change="toggle(c, ($event.target as HTMLInputElement).checked)"
      />
      <span>{{ LABELS[c] ?? c }}</span>
    </label>
    <p v-if="onlyOne" id="channels-last" class="note">En az bir kanal açık kalmalı; bu yüzden son kanal kapatılamaz.</p>
  </fieldset>
</template>

<style scoped>
.channels {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  border: 0;
}

legend {
  float: left;
  margin-inline-end: var(--space-3);
  padding: 0;
  font-weight: 600;
}

.channel {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--control-h);
  padding: 0 var(--space-3);
  border: 1px solid var(--border-control);
  border-radius: var(--radius-sm);
  background: var(--bg-surface);
  font-weight: 600;
}

.channel--on {
  border-color: var(--action);
  background: var(--action-tint);
}

.channel input {
  width: 1.125rem;
  height: 1.125rem;
  accent-color: var(--action);
}

.note {
  flex-basis: 100%;
  color: var(--text-soft);
  font-size: var(--text-sm);
}
</style>

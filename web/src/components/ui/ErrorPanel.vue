<script setup lang="ts">
import { computed, watch } from 'vue'
import { describeError } from '@/api/errors'
import { useConfig } from '@/composables/useConfig'
import { useCountdown } from '@/composables/useCountdown'
import StatusNote from './StatusNote.vue'

// Any failed request, in Turkish: what happened, what to do, the fields that are wrong, a countdown when the server asked to
// wait, and the English technical lines in a collapsed panel. The wording comes from api/errors.ts and depends on the error's
// code, never on the server's English sentence.
const props = withDefaults(defineProps<{ error: unknown; retryLabel?: string; showRetry?: boolean }>(), {
  retryLabel: 'Tekrar dene',
  showRetry: true,
})
const emit = defineEmits<{ retry: [] }>()

const { state: configState } = useConfig()
const variables = computed(() => (configState.value.status === 'ready' ? configState.value.config.variables : ['first_name', 'city']))
const described = computed(() => describeError(props.error, { variables: variables.value }))

const countdown = useCountdown()
watch(
  described,
  (d) => {
    countdown.start(d.retryAfterSeconds ?? 0)
  },
  { immediate: true },
)
const waiting = computed(() => countdown.remaining.value > 0)
// Said once, when the wait is over, instead of every second.
const ready = computed(() => described.value.retryAfterSeconds !== null && !waiting.value)
</script>

<template>
  <StatusNote tone="danger" :title="described.title" announce>
    <p>{{ described.body }}</p>
    <ul v-if="described.fields.length > 0" class="fields">
      <li v-for="f in described.fields" :key="`${f.path}-${f.text}`">
        <strong>{{ f.label }}:</strong> {{ f.text }}
      </li>
    </ul>
    <details v-if="described.technical.length > 0" class="technical">
      <summary>Teknik ayrıntılar</summary>
      <!-- The server's own wording is English, and is shown as it is. -->
      <ul lang="en">
        <li v-for="(line, i) in described.technical" :key="i">
          <code>{{ line }}</code>
        </li>
      </ul>
    </details>
    <p v-if="described.requestId" class="meta">
      Hata kodu: <code>{{ described.requestId }}</code>
    </p>
    <div v-if="showRetry && described.retryable" class="actions">
      <button type="button" class="btn btn--sm" :disabled="waiting" @click="emit('retry')">
        {{ retryLabel }}<template v-if="waiting">
          (<span class="num">{{ countdown.remaining.value }}</span> sn)</template
        >
      </button>
      <span class="visually-hidden" role="status">{{ ready ? 'Tekrar deneyebilirsiniz.' : '' }}</span>
    </div>
    <slot />
  </StatusNote>
</template>

<style scoped>
.fields {
  margin: var(--space-2) 0 0;
  padding-inline-start: var(--space-5);
}

.technical {
  margin-block-start: var(--space-3);
}

.technical summary {
  cursor: pointer;
  font-weight: 600;
}

.technical ul {
  margin: var(--space-2) 0 0;
  padding-inline-start: var(--space-5);
}

.meta {
  margin-block-start: var(--space-2);
  font-size: var(--text-sm);
}

.actions {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-block-start: var(--space-3);
}
</style>

<script setup lang="ts">
import type { PreviewSample } from '@/api/types'

// A few real users with the texts as they would receive them. Everything here is text: the content was written by a model
// and edited by a person, and none of it is ever put into the page as markup (no v-html); line breaks are kept by CSS.
defineProps<{ samples: PreviewSample[] }>()
</script>

<template>
  <ol class="samples">
    <li v-for="s in samples" :key="s.user.id" class="sample">
      <h3 class="sample__who">{{ s.user.first_name }}<span v-if="s.user.city" class="sample__city"> · {{ s.user.city }}</span></h3>

      <div v-if="s.sms !== null" class="message" data-channel="sms">
        <p class="message__channel">SMS</p>
        <p class="message__text">{{ s.sms }}</p>
      </div>
      <div v-if="s.email_subject !== null || s.email_body !== null" class="message" data-channel="email">
        <p class="message__channel">E-posta</p>
        <p v-if="s.email_subject !== null" class="message__subject">{{ s.email_subject }}</p>
        <p v-if="s.email_body !== null" class="message__text">{{ s.email_body }}</p>
      </div>
      <p v-if="s.sms === null && s.email_subject === null && s.email_body === null" class="sample__none">
        Bu kullanıcı seçili kanalların hiçbirinde mesaj almayı kabul etmemiş.
      </p>
    </li>
  </ol>
</template>

<style scoped>
.samples {
  display: grid;
  gap: var(--space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}

.sample {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.sample__who {
  font-size: var(--text-lg);
}

.sample__city {
  color: var(--text-soft);
  font-weight: 400;
}

.message {
  display: grid;
  gap: var(--space-1);
  padding: var(--space-3);
  border-inline-start: 4px solid var(--border-control);
  border-radius: var(--radius-sm);
  background: var(--bg-sunken);
}

.message__channel {
  color: var(--text-soft);
  font-size: var(--text-sm);
  font-weight: 600;
}

.message__subject {
  font-weight: 700;
  overflow-wrap: anywhere;
}

.message__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.sample__none {
  color: var(--text-soft);
}
</style>

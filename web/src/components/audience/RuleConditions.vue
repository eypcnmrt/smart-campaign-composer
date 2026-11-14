<script setup lang="ts">
import { computed } from 'vue'
import { describeConditions } from '@/domain/ruleText'

// The rule in words: every line applies together ("ve"); an OR group is one line with its own list. A condition this app
// does not know is shown as the raw expression and marked, never dropped.
const props = defineProps<{ conditions: unknown }>()
const lines = computed(() => describeConditions(props.conditions))
</script>

<template>
  <ul class="rule">
    <li v-for="(line, i) in lines" :key="i" class="rule__line">
      <template v-if="line.anyOf">
        <span class="rule__text">{{ line.text }}:</span>
        <ul class="rule rule--nested">
          <li v-for="(child, j) in line.anyOf" :key="j" class="rule__line">
            <code v-if="!child.known" class="rule__raw">{{ child.text }}</code>
            <span v-else class="rule__text">{{ child.text }}</span>
            <span v-if="!child.known" class="rule__unknown"> (tanınmayan koşul)</span>
          </li>
        </ul>
      </template>
      <template v-else>
        <code v-if="!line.known" class="rule__raw">{{ line.text }}</code>
        <span v-else class="rule__text">{{ line.text }}</span>
        <span v-if="!line.known" class="rule__unknown"> (tanınmayan koşul)</span>
      </template>
    </li>
  </ul>
</template>

<style scoped>
.rule {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.rule__line {
  padding-inline-start: var(--space-3);
  border-inline-start: 3px solid var(--action);
}

.rule--nested {
  margin-block-start: var(--space-2);
}

.rule--nested .rule__line {
  border-inline-start-color: var(--border-control);
}

.rule__raw {
  overflow-wrap: anywhere;
}

.rule__unknown {
  color: var(--text-soft);
  font-size: var(--text-sm);
}
</style>

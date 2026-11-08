<script setup lang="ts">
import { STEPS, type Step } from '../lib/composer'

// Steps are real buttons in an ordered list: Tab moves between them, Enter/Space activates.
defineProps<{ current: Step; reachable: Record<Step, boolean> }>()
defineEmits<{ go: [Step] }>()

const order = STEPS.map((s) => s.id)
</script>

<template>
  <nav aria-label="Campaign steps">
    <ol class="stepper">
      <li v-for="(s, i) in STEPS" :key="s.id" :class="{ done: order.indexOf(current) > i }">
        <button
          type="button"
          :aria-current="current === s.id ? 'step' : undefined"
          :disabled="!reachable[s.id]"
          @click="$emit('go', s.id)"
        >
          <span class="n" aria-hidden="true">{{ order.indexOf(current) > i ? '✓' : i + 1 }}</span>
          <span>{{ s.label }}<span v-if="order.indexOf(current) > i" class="sr-only"> (done)</span></span>
        </button>
      </li>
    </ol>
  </nav>
</template>

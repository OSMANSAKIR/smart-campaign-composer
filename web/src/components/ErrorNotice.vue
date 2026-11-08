<script setup lang="ts">
import type { UserMessage } from '../lib/errors'

defineProps<{ error: UserMessage }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="notice bad" role="alert">
    <strong>{{ error.title }}</strong>
    <p>{{ error.message }}</p>
    <ul v-if="error.details.length">
      <li v-for="(d, i) in error.details" :key="i">{{ d }}</li>
    </ul>
    <div v-if="$slots.default || error.retryable" class="row" style="margin-top: 6px">
      <button v-if="error.retryable" class="btn" type="button" @click="$emit('retry')">Try again</button>
      <slot />
    </div>
  </div>
</template>

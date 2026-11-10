<script setup lang="ts">
import { computed } from 'vue'
import { STATUS_LABEL } from '../lib/format'

const props = defineProps<{ status?: string }>()
const tone = computed(() => {
  switch (props.status) {
    case 'completed':
      return 'ok'
    case 'completed_with_failures':
    case 'cancelled':
      return 'warn'
    case 'sending':
    case 'scheduled':
    case 'content_ready':
      return 'info'
    default:
      return ''
  }
})
</script>

<template>
  <span class="pill" :class="tone"><span class="dot" aria-hidden="true" />{{ STATUS_LABEL[status ?? ''] ?? status ?? 'Unknown' }}</span>
</template>

<script setup lang="ts">
import { formatCost, formatInt } from '../lib/format'
import type { LlmMeta } from '../lib/types'

// "How Claude produced this" (R-OBS-3). `what` names the result for the cost notice (F6, api.md §9.7).
defineProps<{ llm: LlmMeta; what: 'content' | 'audience' }>()
</script>

<template>
  <div class="stack" style="gap: 10px">
    <div v-if="llm.cost_recorded === false" class="notice warn" role="status">
      <strong>
        {{
          what === 'audience'
            ? 'The audience is ready, but its cost could not be recorded.'
            : 'The content is ready, but its cost could not be recorded.'
        }}
      </strong>
    </div>
    <div class="card">
      <header><h3>How Claude produced this</h3></header>
      <dl class="meta">
        <div><dt>Provider</dt><dd>{{ llm.provider }}</dd></div>
        <div><dt>Model</dt><dd>{{ llm.model }}</dd></div>
        <div><dt>Latency</dt><dd class="num">{{ formatInt(llm.latency_ms) }} ms</dd></div>
        <div><dt>Attempts</dt><dd class="num">{{ llm.attempts }}{{ llm.repaired ? ' (repaired)' : '' }}</dd></div>
        <div><dt>Tokens in / out</dt><dd class="num">{{ formatInt(llm.input_tokens) }} / {{ formatInt(llm.output_tokens) }}</dd></div>
        <div><dt>Cache read</dt><dd class="num">{{ formatInt(llm.cache_read_tokens) }}</dd></div>
        <div><dt>Cache write</dt><dd class="num">{{ formatInt(llm.cache_write_tokens) }}</dd></div>
        <div><dt>Cost</dt><dd class="num">{{ formatCost(llm.cost_usd) }}{{ llm.cost_recorded === false ? ' (not recorded)' : '' }}</dd></div>
        <div><dt>Prompt</dt><dd>{{ llm.prompt_version }}</dd></div>
      </dl>
    </div>
  </div>
</template>

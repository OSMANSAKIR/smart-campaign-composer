<script setup lang="ts">
import { computed, ref } from 'vue'
import { formatCost, formatInt } from '../lib/format'
import type { Campaign, LlmMeta } from '../lib/types'

const props = defineProps<{ campaign: Campaign; llm: LlmMeta | null; busy: boolean }>()
defineEmits<{ regenerate: []; next: [] }>()

const showJson = ref(false)
const seg = computed(() => props.campaign.segment)
const chips = computed(() => {
  const s = seg.value
  if (!s) return []
  // Prefer the campaign language; fall back to whichever rendering exists.
  const primary = s.rendered[s.language] ?? []
  return primary.length ? primary : (s.rendered.en.length ? s.rendered.en : s.rendered.tr)
})
const json = computed(() => JSON.stringify(seg.value?.rule ?? null, null, 2))
const nobody = computed(() => (seg.value?.audience.total ?? 0) === 0)
</script>

<template>
  <section v-if="seg" class="stack" aria-labelledby="aud-h">
    <div class="card stack">
      <header>
        <h2 id="aud-h">Who will receive it</h2>
        <span class="pill" :class="seg.source === 'edited' ? 'warn' : 'info'">
          {{ seg.source === 'edited' ? 'Edited by hand' : 'Proposed by Claude' }}
        </span>
      </header>

      <ul v-if="chips.length" class="chips" aria-label="Audience rules">
        <li v-for="(c, i) in chips" :key="i" class="chip">{{ c }}</li>
      </ul>
      <p v-else class="muted">No conditions: this audience is everyone in the database.</p>
      <p v-if="seg.explanation" class="muted">{{ seg.explanation }}</p>

      <div>
        <button
          type="button"
          class="btn quiet"
          :aria-expanded="showJson"
          aria-controls="rule-json"
          @click="showJson = !showJson"
        >
          {{ showJson ? 'Hide' : 'Show' }} raw rule (JSON)
        </button>
        <pre v-show="showJson" id="rule-json" class="code" tabindex="0" aria-label="Raw audience rule as JSON">{{ json }}</pre>
      </div>
    </div>

    <div class="counts" role="group" aria-label="Audience size">
      <div class="count lead">
        <div class="v num">{{ formatInt(seg.audience.total) }}</div>
        <div class="l">people match</div>
      </div>
      <div class="count">
        <div class="v num">{{ formatInt(seg.audience.sms_reachable) }}</div>
        <div class="l">can get SMS (consented)</div>
      </div>
      <div class="count">
        <div class="v num">{{ formatInt(seg.audience.email_reachable) }}</div>
        <div class="l">can get email (consented)</div>
      </div>
      <div class="count">
        <div class="v num">{{ formatInt(seg.audience.both) }}</div>
        <div class="l">reachable on both</div>
      </div>
    </div>

    <div v-if="nobody" class="notice warn" role="status">
      <strong>Nobody matches this brief.</strong>
      <p>Loosen a condition in the brief and find the audience again.</p>
    </div>

    <div v-if="seg.unsupported_criteria.length" class="notice warn" role="status">
      <strong>Claude could not apply some of what you asked for</strong>
      <p>These parts are not in the audience rule, so they do not narrow the audience:</p>
      <ul>
        <li v-for="(u, i) in seg.unsupported_criteria" :key="i">{{ u }}</li>
      </ul>
    </div>
    <div v-if="seg.assumptions.length" class="notice info">
      <strong>Assumptions Claude made</strong>
      <ul>
        <li v-for="(a, i) in seg.assumptions" :key="i">{{ a }}</li>
      </ul>
    </div>
    <div v-for="(w, i) in seg.warnings" :key="i" class="notice warn" role="status">
      <strong>{{ w.message }}</strong>
    </div>

    <div v-if="llm" class="card">
      <header><h3>How Claude produced this</h3></header>
      <dl class="meta">
        <div><dt>Provider</dt><dd>{{ llm.provider }}</dd></div>
        <div><dt>Model</dt><dd>{{ llm.model }}</dd></div>
        <div><dt>Latency</dt><dd class="num">{{ formatInt(llm.latency_ms) }} ms</dd></div>
        <div><dt>Attempts</dt><dd class="num">{{ llm.attempts }}{{ llm.repaired ? ' (repaired)' : '' }}</dd></div>
        <div><dt>Tokens in / out</dt><dd class="num">{{ formatInt(llm.input_tokens) }} / {{ formatInt(llm.output_tokens) }}</dd></div>
        <div><dt>Cache read</dt><dd class="num">{{ formatInt(llm.cache_read_tokens) }}</dd></div>
        <div><dt>Cost</dt><dd class="num">{{ formatCost(llm.cost_usd) }}</dd></div>
        <div><dt>Prompt</dt><dd>{{ llm.prompt_version }}</dd></div>
      </dl>
    </div>

    <div class="row">
      <button class="btn primary" type="button" :disabled="nobody || busy" @click="$emit('next')">
        {{ campaign.content ? 'Continue to content' : 'Write the content' }}
      </button>
      <button class="btn" type="button" :disabled="busy" @click="$emit('regenerate')">
        <span v-if="busy" class="spinner" aria-hidden="true" />
        {{ busy ? 'Working…' : 'Find audience again' }}
      </button>
      <span v-if="campaign.content && campaign.status === 'segmented'" class="muted">
        The audience changed, so the content needs a fresh check or a new draft.
      </span>
    </div>
  </section>
</template>

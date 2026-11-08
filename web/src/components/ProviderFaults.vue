<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { api } from '../lib/api'
import { describeError, type UserMessage } from '../lib/errors'
import { fromPercent, PRESETS, toPercent, type FaultPatch } from '../lib/providerFaults'

// Mock provider fault controls (need.md N-WEB-003): change the provider's behavior while a campaign runs and
// watch retries, 429 pauses and the error table react on the dashboard.
const form = reactive({ error: 0, r429: 0, hang: 0, latMin: 0, latMax: 0, spike: 0 })
const loaded = ref(false)
const saving = ref(false)
const message = ref('')
const error = ref<UserMessage | null>(null)

function fill(c: FaultPatch) {
  form.error = toPercent(c.error_rate)
  form.r429 = toPercent(c.rate_429)
  form.hang = toPercent(c.hang_rate)
  form.spike = toPercent(c.spike_rate)
  form.latMin = c.latency_min_ms
  form.latMax = c.latency_max_ms
}

async function load() {
  error.value = null
  try {
    fill(await api.providerConfig())
    loaded.value = true
  } catch (e) {
    error.value = describeError(e)
  }
}

async function save(patch?: FaultPatch, label?: string) {
  saving.value = true
  error.value = null
  message.value = ''
  try {
    const body: FaultPatch = patch ?? {
      error_rate: fromPercent(form.error),
      rate_429: fromPercent(form.r429),
      hang_rate: fromPercent(form.hang),
      spike_rate: fromPercent(form.spike),
      latency_min_ms: form.latMin,
      latency_max_ms: form.latMax,
    }
    fill(await api.saveProviderConfig(body))
    message.value = label ? `${label} applied.` : 'Saved.'
  } catch (e) {
    error.value = describeError(e)
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="card" aria-labelledby="s-faults">
    <header>
      <h2 id="s-faults">Mock provider faults</h2>
    </header>
    <p class="muted">Changes apply to the next requests, also during a running campaign.</p>
    <div v-if="error" class="notice bad" role="alert">
      <strong>{{ error.title }}</strong>
      <p>{{ error.message }}</p>
      <p v-for="d in error.details" :key="d">{{ d }}</p>
    </div>
    <template v-if="loaded">
      <div class="row presets">
        <button v-for="p in PRESETS" :key="p.id" class="btn" type="button" :title="p.hint" :disabled="saving" @click="save(p.patch, p.label)">
          {{ p.label }}
        </button>
      </div>
      <form class="grid" @submit.prevent="save()">
        <label>5xx errors (%)<input v-model.number="form.error" type="number" min="0" max="100" step="0.5" /></label>
        <label>429 rate limited (%)<input v-model.number="form.r429" type="number" min="0" max="100" step="0.5" /></label>
        <label>Hangs, no answer (%)<input v-model.number="form.hang" type="number" min="0" max="100" step="0.5" /></label>
        <label>Latency spikes (%)<input v-model.number="form.spike" type="number" min="0" max="100" step="0.5" /></label>
        <label>Latency min (ms)<input v-model.number="form.latMin" type="number" min="0" step="10" /></label>
        <label>Latency max (ms)<input v-model.number="form.latMax" type="number" min="0" step="10" /></label>
        <div class="row actions">
          <button class="btn primary" type="submit" :disabled="saving">Apply</button>
          <span class="muted" role="status">{{ message }}</span>
        </div>
      </form>
    </template>
  </section>
</template>

<style scoped>
.presets { gap: 8px; flex-wrap: wrap; margin: 8px 0 12px; }
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 10px 14px; }
.grid label { display: flex; flex-direction: column; gap: 4px; font-size: 0.9em; }
.grid input { padding: 6px 8px; border: 1px solid var(--line); border-radius: 6px; background: var(--panel); color: var(--ink); font: inherit; }
.actions { grid-column: 1 / -1; gap: 10px; align-items: center; }
</style>

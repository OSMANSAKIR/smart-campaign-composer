<script setup lang="ts">
// Phase 0 placeholder: proves the SPA is served and can reach the API. The web builder replaces it.
import { onMounted, ref } from 'vue'

interface SystemStatus {
  llm: { provider: string; model: string; mock: boolean }
  db: { status: string }
  worker: { status: string }
  provider: { status: string }
}

const status = ref<SystemStatus | null>(null)
const error = ref<string | null>(null)

onMounted(async () => {
  try {
    const res = await fetch('/api/system/status')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    status.value = (await res.json()) as SystemStatus
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
})
</script>

<template>
  <main>
    <h1>Smart Campaign Composer</h1>
    <p v-if="status?.llm.mock" role="status">Mock LLM — set ANTHROPIC_API_KEY for real generation</p>
    <p v-if="error" role="alert">Could not load system status: {{ error }}</p>
    <dl v-else-if="status">
      <dt>LLM</dt><dd>{{ status.llm.provider }} · {{ status.llm.model }}</dd>
      <dt>Database</dt><dd>{{ status.db.status }}</dd>
      <dt>Worker</dt><dd>{{ status.worker.status }}</dd>
      <dt>Provider</dt><dd>{{ status.provider.status }}</dd>
    </dl>
    <p v-else>Loading…</p>
  </main>
</template>

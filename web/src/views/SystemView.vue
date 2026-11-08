<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import ProviderFaults from '../components/ProviderFaults.vue'
import { api } from '../lib/api'
import { describeError, type UserMessage } from '../lib/errors'
import { formatClock } from '../lib/format'
import type { SystemStatus } from '../lib/types'

const status = ref<SystemStatus | null>(null)
const error = ref<UserMessage | null>(null)
const loading = ref(true)
let timer: ReturnType<typeof setInterval> | undefined

async function load() {
  try {
    status.value = await api.systemStatus()
    error.value = null
  } catch (e) {
    error.value = describeError(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => void load(), 5000)
})
onBeforeUnmount(() => clearInterval(timer))

function tone(s: string): string {
  if (s === 'ok') return 'ok'
  if (s === 'stale') return 'warn'
  return 'bad'
}
const WORD: Record<string, string> = {
  ok: 'Healthy',
  stale: 'Heartbeat is late',
  down: 'Down',
}
</script>

<template>
  <div class="shell">
    <div class="page-head">
      <div>
        <h1>System</h1>
        <p>Health of each part of the stack. Refreshes every 5 seconds.</p>
      </div>
    </div>

    <div v-if="loading" class="empty" role="status">
      <span class="spinner" aria-hidden="true" />
      <p>Checking the system…</p>
    </div>
    <ErrorNotice v-else-if="error && !status" :error="error" @retry="load" />

    <div v-else-if="status" class="stack">
      <div v-if="error" class="notice warn" role="status">
        <strong>Could not refresh.</strong>
        <p>Showing the last known status. {{ error.message }}</p>
      </div>

      <div class="status-grid">
        <section class="card" aria-labelledby="s-api">
          <header>
            <h2 id="s-api">API</h2>
            <span class="pill" :class="tone(status.api.status)"><span class="dot" aria-hidden="true" />{{ WORD[status.api.status] ?? status.api.status }}</span>
          </header>
          <p class="muted">Version {{ status.api.version }}</p>
        </section>
        <section class="card" aria-labelledby="s-db">
          <header>
            <h2 id="s-db">Database</h2>
            <span class="pill" :class="tone(status.db.status)"><span class="dot" aria-hidden="true" />{{ WORD[status.db.status] ?? status.db.status }}</span>
          </header>
          <p v-if="status.db.latency_ms !== undefined" class="muted num">Round trip {{ status.db.latency_ms }} ms</p>
        </section>
        <section class="card" aria-labelledby="s-worker">
          <header>
            <h2 id="s-worker">Worker</h2>
            <span class="pill" :class="tone(status.worker.status)"><span class="dot" aria-hidden="true" />{{ WORD[status.worker.status] ?? status.worker.status }}</span>
          </header>
          <ul v-if="status.worker.dispatchers?.length" class="issues">
            <li v-for="d in status.worker.dispatchers" :key="d.worker_id + d.channel">
              <b>{{ d.channel.toUpperCase() }}</b>
              <span class="muted">
                {{ d.stale ? 'no heartbeat' : d.holds_lock ? 'dispatching' : 'standby' }}, last seen {{ formatClock(d.last_seen) }}
                <template v-if="d.paused_until"> · paused by the provider</template>
              </span>
            </li>
          </ul>
          <p v-else class="muted">No dispatcher has reported in.</p>
        </section>
        <section class="card" aria-labelledby="s-prov">
          <header>
            <h2 id="s-prov">Message provider</h2>
            <span class="pill" :class="tone(status.provider.status)"><span class="dot" aria-hidden="true" />{{ WORD[status.provider.status] ?? status.provider.status }}</span>
          </header>
          <p v-if="status.provider.url" class="muted">{{ status.provider.url }}</p>
        </section>
        <section class="card" aria-labelledby="s-llm">
          <header>
            <h2 id="s-llm">Language model</h2>
            <span class="pill" :class="status.llm.mock ? 'warn' : 'ok'">{{ status.llm.mock ? 'Mock' : 'Live' }}</span>
          </header>
          <dl class="meta">
            <div><dt>Provider</dt><dd>{{ status.llm.provider }}</dd></div>
            <div><dt>Model</dt><dd>{{ status.llm.model }}</dd></div>
          </dl>
        </section>
      </div>
      <ProviderFaults />
    </div>
  </div>
</template>

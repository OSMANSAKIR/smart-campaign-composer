<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import StatusPill from '../components/StatusPill.vue'
import { api } from '../lib/api'
import { describeError, type UserMessage } from '../lib/errors'
import { formatClock, formatEta, formatInt } from '../lib/format'
import {
  applyEvent,
  applySnapshot,
  channelEntries,
  channelFraction,
  emptyDashboard,
  errorLabel,
  overallFraction,
} from '../lib/progress'
import { links } from '../lib/router'
import { connectProgress, type ConnectionState } from '../lib/sse'
import type { Campaign, Channel, ChannelProgress } from '../lib/types'

const props = defineProps<{ campaignId: string }>()

const campaign = ref<Campaign | null>(null)
const state = ref(emptyDashboard())
const conn = ref<ConnectionState>('connecting')
const error = ref<UserMessage | null>(null)
const loading = ref(true)
const finalStatus = ref<string | null>(null)
let stop: (() => void) | null = null

const CHANNEL_NAME: Record<Channel, string> = { sms: 'SMS', email: 'Email' }

const status = computed(() => finalStatus.value ?? state.value.progress?.status ?? campaign.value?.status)
const active = computed(() => status.value === 'sending')
const overall = computed(() => (state.value.progress ? Math.round(overallFraction(state.value.progress) * 100) : 0))
const errors = computed(() => Object.entries(state.value.progress?.errors ?? {}).filter(([, n]) => n > 0))
const channels = computed(() => (state.value.progress ? channelEntries(state.value.progress) : []))

function pct(c: ChannelProgress, n: number) {
  return c.total > 0 ? (n / c.total) * 100 : 0
}

async function load() {
  loading.value = true
  error.value = null
  try {
    campaign.value = await api.getCampaign(props.campaignId)
    if (campaign.value.progress) state.value = applySnapshot(state.value, campaign.value.progress)
    if (campaign.value.status === 'draft' || campaign.value.status === 'segmented' || campaign.value.status === 'content_ready') {
      return // not sent yet: nothing to stream
    }
    stop?.()
    stop = connectProgress(props.campaignId, {
      // Every snapshot is complete: replace, never merge (so a reconnect cannot show stale numbers).
      onSnapshot: (p) => (state.value = applySnapshot(state.value, p)),
      onEvent: (e) => (state.value = applyEvent(state.value, e)),
      onDone: (s) => {
        finalStatus.value = s
        void api.getCampaign(props.campaignId).then((c) => (campaign.value = c)).catch(() => {})
      },
      onState: (s) => (conn.value = s),
    })
  } catch (e) {
    error.value = describeError(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)
onBeforeUnmount(() => stop?.())

const connLabel = computed(() => {
  switch (conn.value) {
    case 'live':
      return active.value ? 'Live' : 'Connected'
    case 'reconnecting':
      return 'Connection lost. Reconnecting…'
    case 'closed':
      return finalStatus.value ? 'Finished' : 'Disconnected'
    default:
      return 'Connecting…'
  }
})
const connTone = computed(() => (conn.value === 'live' ? 'ok' : conn.value === 'reconnecting' ? 'warn' : ''))
const notSent = computed(
  () => campaign.value && ['draft', 'segmented', 'content_ready'].includes(campaign.value.status),
)
</script>

<template>
  <div class="shell">
    <div class="page-head">
      <div>
        <h1>Delivery</h1>
        <p v-if="campaign" class="brief-cell" :title="campaign.brief">{{ campaign.brief }}</p>
      </div>
      <div class="row">
        <StatusPill v-if="status" :status="status" />
        <span class="pill" :class="connTone" role="status"><span class="dot" aria-hidden="true" />{{ connLabel }}</span>
      </div>
    </div>

    <div v-if="loading" class="empty" role="status">
      <span class="spinner" aria-hidden="true" />
      <p>Loading delivery status…</p>
    </div>

    <ErrorNotice v-else-if="error" :error="error" @retry="load">
      <a class="btn" :href="links.list()">Back to campaigns</a>
    </ErrorNotice>

    <div v-else-if="notSent" class="empty">
      <p>This campaign has not been sent yet, so there is nothing to show.</p>
      <a class="btn primary" :href="links.composer(campaignId)">Continue editing</a>
    </div>

    <div v-else-if="!state.progress" class="empty" role="status">
      <span class="spinner" aria-hidden="true" />
      <p>Waiting for the first progress update…</p>
    </div>

    <div v-else class="stack">
      <div v-if="conn === 'reconnecting'" class="notice warn" role="status">
        <strong>Connection lost.</strong>
        <p>Reconnecting. The numbers below refresh as soon as the connection is back; nothing is lost.</p>
      </div>

      <div class="card">
        <header>
          <h2>Overall progress</h2>
          <span class="num">
            <b>{{ overall }}%</b>
            <span class="muted"> · {{ active ? `ETA ${formatEta(state.progress.eta_seconds)}` : 'finished' }}</span>
          </span>
        </header>
        <div
          class="bar"
          role="progressbar"
          aria-label="Overall delivery progress"
          :aria-valuenow="overall"
          aria-valuemin="0"
          aria-valuemax="100"
        >
          <span class="sent" :style="{ width: overall + '%' }" />
        </div>
      </div>

      <div class="two">
        <section v-for="[ch, c] in channels" :key="ch" class="card" :aria-labelledby="`ch-${ch}`">
          <header>
            <h2 :id="`ch-${ch}`">{{ CHANNEL_NAME[ch] }}</h2>
            <span class="num muted">
              {{ formatInt(c.throughput_per_s) }} msg/s · {{ formatInt(c.total) }} total
            </span>
          </header>
          <div
            class="bar"
            role="progressbar"
            :aria-label="`${CHANNEL_NAME[ch]} progress`"
            :aria-valuenow="Math.round(channelFraction(c) * 100)"
            aria-valuemin="0"
            aria-valuemax="100"
          >
            <span class="sent" :style="{ width: pct(c, c.sent) + '%' }" />
            <span class="failed" :style="{ width: pct(c, c.failed) + '%' }" />
            <span class="skipped" :style="{ width: pct(c, c.skipped_no_consent) + '%' }" />
          </div>
          <div v-if="c.paused_until" class="notice warn" style="margin-top: 10px" role="status">
            <strong>Paused by the provider</strong>
            <p>Sending resumes automatically after the provider’s rate-limit wait.</p>
          </div>
          <div class="cells num">
            <div class="cell"><div class="v">{{ formatInt(c.queued) }}</div><div class="l">Queued</div></div>
            <div class="cell"><div class="v">{{ formatInt(c.sending) }}</div><div class="l">Sending</div></div>
            <div class="cell ok"><div class="v">{{ formatInt(c.sent) }}</div><div class="l">Sent</div></div>
            <div class="cell"><div class="v">{{ formatInt(c.retry) }}</div><div class="l">Retrying</div></div>
            <div class="cell bad"><div class="v">{{ formatInt(c.failed) }}</div><div class="l">Failed</div></div>
            <div class="cell"><div class="v">{{ formatInt(c.skipped_no_consent) }}</div><div class="l">Skipped (no consent)</div></div>
          </div>
        </section>
      </div>

      <div class="two">
        <section class="card" aria-labelledby="err-h">
          <header><h2 id="err-h">Errors from the provider</h2></header>
          <p v-if="errors.length === 0" class="muted">No errors so far.</p>
          <table v-else class="list num">
            <thead><tr><th scope="col">Kind</th><th scope="col">Count</th></tr></thead>
            <tbody>
              <tr v-for="[k, n] in errors" :key="k"><td>{{ errorLabel(k) }}</td><td>{{ formatInt(n) }}</td></tr>
            </tbody>
          </table>
        </section>

        <section class="card" aria-labelledby="ev-h">
          <header><h2 id="ev-h">Recent attempts</h2></header>
          <p v-if="state.events.length === 0" class="muted">
            {{ active ? 'Waiting for attempts…' : 'No attempts were reported on this connection.' }}
          </p>
          <ul v-else class="events" aria-label="Recent delivery attempts">
            <li v-for="(e, i) in state.events" :key="`${e.message_id}-${e.attempt}-${i}`">
              <span class="t">{{ formatClock(e.at) }}</span>
              <span>{{ CHANNEL_NAME[e.channel] }}</span>
              <span class="pill" :class="e.outcome === 'sent' ? 'ok' : e.outcome === 'failed' ? 'bad' : 'warn'">{{ e.outcome }}</span>
              <span class="muted num">
                attempt {{ e.attempt }}<template v-if="e.http_status"> · HTTP {{ e.http_status }}</template
                ><template v-if="e.error_class"> · {{ errorLabel(e.error_class) }}</template>
              </span>
            </li>
          </ul>
        </section>
      </div>
    </div>
  </div>
</template>

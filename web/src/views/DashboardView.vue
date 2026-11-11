<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import MessagesPanel from '../components/MessagesPanel.vue'
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
  variantRows,
} from '../lib/progress'
import { localeRows } from '../lib/localization'
import { formatCountdown } from '../lib/schedule'
import { links } from '../lib/router'
import { appendSample, linePoints, LIMIT_PER_S, type Sample } from '../lib/throughput'
import { connectProgress, type ConnectionState } from '../lib/sse'
import type { Campaign, Channel, ChannelProgress } from '../lib/types'

const props = defineProps<{ campaignId: string }>()

const campaign = ref<Campaign | null>(null)
const state = ref(emptyDashboard())
const conn = ref<ConnectionState>('connecting')
const error = ref<UserMessage | null>(null)
const loading = ref(true)
const finalStatus = ref<string | null>(null)
const history = ref<Sample[]>([]) // throughput chart; starts when this page opens
let stop: (() => void) | null = null

const CHANNEL_NAME: Record<Channel, string> = { sms: 'SMS', email: 'Email' }

// A cancel answer is newer than any snapshot that was already on its way, so it wins over progress.status.
const status = computed(
  () =>
    finalStatus.value ??
    (campaign.value?.status === 'cancelled' ? 'cancelled' : (state.value.progress?.status ?? campaign.value?.status)),
)
const active = computed(() => status.value === 'sending')
const isScheduled = computed(() => status.value === 'scheduled')
const canStop = computed(() => status.value === 'sending' || status.value === 'scheduled')

// --- F5: scheduled start with a live countdown ---------------------------------------------------------
const nowMs = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | undefined
const scheduledAt = computed(() => (campaign.value?.scheduled_at ? new Date(campaign.value.scheduled_at) : null))
const scheduledLocal = computed(() =>
  scheduledAt.value ? scheduledAt.value.toLocaleString('en-GB', { dateStyle: 'medium', timeStyle: 'medium' }) : '',
)
const countdown = computed(() => (scheduledAt.value ? formatCountdown(scheduledAt.value.getTime() - nowMs.value) : ''))
function tick() {
  nowMs.value = Date.now()
  // Past the start time the worker's scheduler flips the campaign to sending; pick that up even if no snapshot comes.
  if (isScheduled.value && scheduledAt.value && nowMs.value > scheduledAt.value.getTime() && Math.floor(nowMs.value / 1000) % 3 === 0) {
    void api.getCampaign(props.campaignId).then((c) => (campaign.value = c)).catch(() => {})
  }
}

// --- F4: stop sending (in-page confirmation, never window.confirm) --------------------------------------
const stopDialog = ref<HTMLDialogElement | null>(null)
const stopOpener = ref<HTMLButtonElement | null>(null)
const stopping = ref(false)
const stopError = ref<UserMessage | null>(null)
const cancelledCount = ref<number | null>(null)

function askStop() {
  stopError.value = null
  stopDialog.value?.showModal()
}
function closeStop() {
  stopDialog.value?.close()
  stopOpener.value?.focus()
}
async function confirmStop() {
  stopping.value = true
  stopError.value = null
  try {
    const r = await api.cancel(props.campaignId)
    campaign.value = r.campaign
    cancelledCount.value = r.cancelled
    closeStop()
  } catch (e) {
    stopError.value = describeError(e)
  } finally {
    stopping.value = false
  }
}
const overall = computed(() => (state.value.progress ? Math.round(overallFraction(state.value.progress) * 100) : 0))
const errors = computed(() => Object.entries(state.value.progress?.errors ?? {}).filter(([, n]) => n > 0))
const channels = computed(() => (state.value.progress ? channelEntries(state.value.progress) : []))
const hasVariants = computed(() => channels.value.some(([, c]) => variantRows(c).length > 0))

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
      onSnapshot: (p) => {
        state.value = applySnapshot(state.value, p)
        history.value = appendSample(history.value, p)
      },
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

onMounted(() => {
  ticker = setInterval(tick, 1000)
  void load()
})
onBeforeUnmount(() => {
  stop?.()
  clearInterval(ticker)
})

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
// Chart box in SVG units; the y axis leaves headroom above the 100/s limit line.
const CHART_W = 600
const CHART_H = 140
const Y_MAX = 120
const limitY = CHART_H - (LIMIT_PER_S / Y_MAX) * CHART_H
const chartLabel = computed(() => {
  const last = history.value[history.value.length - 1]
  return last
    ? `Throughput over the last two minutes. Now: SMS ${last.sms} per second, email ${last.email} per second; limit ${LIMIT_PER_S} per second per channel.`
    : 'Throughput chart, waiting for data.'
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
        <button v-if="canStop && !loading && !error" ref="stopOpener" class="btn" type="button" @click="askStop">
          Stop sending
        </button>
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

    <div v-else-if="!state.progress && !isScheduled" class="empty" role="status">
      <span class="spinner" aria-hidden="true" />
      <p>Waiting for the first progress update…</p>
    </div>

    <div v-else class="stack">
      <div v-if="conn === 'reconnecting'" class="notice warn" role="status">
        <strong>Connection lost.</strong>
        <p>Reconnecting. The numbers below refresh as soon as the connection is back; nothing is lost.</p>
      </div>

      <section v-if="isScheduled" class="card stack" aria-labelledby="sched-h" data-testid="scheduled">
        <header><h2 id="sched-h">Scheduled</h2></header>
        <p>
          Sending starts at <b class="num">{{ scheduledLocal }}</b> (your local time).
        </p>
        <p class="num" role="timer" aria-live="off" style="font-size: 1.5rem; font-weight: 700">{{ countdown }}</p>
        <p class="muted">The audience is already fixed. Nothing is sent before that time, and you can stop it until then.</p>
      </section>

      <div v-if="status === 'cancelled'" class="notice warn" role="status" data-testid="cancelled">
        <strong>Sending was stopped.</strong>
        <p>
          <template v-if="cancelledCount !== null">{{ formatInt(cancelledCount) }} waiting messages were cancelled. </template>
          Messages already handed to the provider finished and are counted as sent or failed.
        </p>
      </div>

      <template v-if="state.progress">
      <div class="card">
        <header>
          <h2>Overall progress</h2>
          <span class="num">
            <b>{{ overall }}%</b>
            <span class="muted"> · {{ active ? `ETA ${formatEta(state.progress.eta_seconds)}` : isScheduled ? 'not started' : 'finished' }}</span>
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
            <span class="cancelled" :style="{ width: pct(c, c.cancelled ?? 0) + '%' }" />
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
            <div v-if="(c.cancelled ?? 0) > 0 || status === 'cancelled'" class="cell">
              <div class="v">{{ formatInt(c.cancelled ?? 0) }}</div><div class="l">Cancelled</div>
            </div>
          </div>
          <div v-if="variantRows(c).length" class="variants">
            <table class="list num" :aria-label="`${CHANNEL_NAME[ch]} delivery by variant`">
              <thead>
                <tr>
                  <th scope="col">Variant</th><th scope="col">Total</th><th scope="col">Sent</th><th scope="col">Failed</th>
                  <th v-if="status === 'cancelled'" scope="col">Cancelled</th><th scope="col">Skipped</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in variantRows(c)" :key="r.key" :data-variant="r.key">
                  <th scope="row">{{ r.key.toUpperCase() }}</th>
                  <td>{{ formatInt(r.v.total) }}</td>
                  <td>{{ formatInt(r.v.sent) }}</td>
                  <td>{{ formatInt(r.v.failed) }}</td>
                  <td v-if="status === 'cancelled'">{{ formatInt(r.v.cancelled) }}</td>
                  <td>{{ formatInt(r.v.skipped_no_consent) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-if="localeRows(c).length" class="variants">
            <table class="list num" :aria-label="`${CHANNEL_NAME[ch]} delivery by language`">
              <thead>
                <tr>
                  <th scope="col">Language</th><th scope="col">Total</th><th scope="col">Sent</th><th scope="col">Failed</th>
                  <th v-if="status === 'cancelled'" scope="col">Cancelled</th><th scope="col">Skipped</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in localeRows(c)" :key="r.locale" :data-locale="r.locale">
                  <th scope="row">{{ r.name }}</th>
                  <td>{{ formatInt(r.v.total) }}</td>
                  <td>{{ formatInt(r.v.sent) }}</td>
                  <td>{{ formatInt(r.v.failed) }}</td>
                  <td v-if="status === 'cancelled'">{{ formatInt(r.v.cancelled) }}</td>
                  <td>{{ formatInt(r.v.skipped_no_consent) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>

      <section v-if="history.length > 1" class="card" aria-labelledby="tp-h">
        <header>
          <h2 id="tp-h">Throughput</h2>
          <span class="muted legend">
            <span class="key sms" aria-hidden="true" />SMS <span class="key email" aria-hidden="true" />Email
            <span class="key limit" aria-hidden="true" />limit {{ LIMIT_PER_S }}/s
          </span>
        </header>
        <svg class="chart" :viewBox="`0 0 ${CHART_W} ${CHART_H}`" preserveAspectRatio="none" role="img" :aria-label="chartLabel">
          <line class="limit" x1="0" :y1="limitY" :x2="CHART_W" :y2="limitY" />
          <polyline class="sms" :points="linePoints(history, 'sms', CHART_W, CHART_H, Y_MAX)" />
          <polyline class="email" :points="linePoints(history, 'email', CHART_W, CHART_H, Y_MAX)" />
        </svg>
        <p class="muted small">Messages per second per channel (5 s average), last two minutes since this page opened.</p>
      </section>

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

      <MessagesPanel :campaign-id="campaignId" :has-variants="hasVariants" />
      </template>
    </div>

    <dialog ref="stopDialog" aria-labelledby="stop-h">
      <div class="inner">
        <h2 id="stop-h">Stop sending?</h2>
        <p>
          Messages that are still waiting will be cancelled and will not be sent. Messages already handed to the
          provider finish. This cannot be undone.
        </p>
        <p v-if="stopError" class="notice bad" role="alert">{{ stopError.title }}. {{ stopError.message }}</p>
        <div class="actions">
          <button class="btn" type="button" :disabled="stopping" @click="closeStop">Keep sending</button>
          <button class="btn primary" type="button" :disabled="stopping" @click="confirmStop">
            <span v-if="stopping" class="spinner" aria-hidden="true" />
            {{ stopping ? 'Stopping…' : 'Stop sending' }}
          </button>
        </div>
      </div>
    </dialog>
  </div>
</template>

<style scoped>
.variants { margin-top: 12px; overflow-x: auto; }
.variants table.list td, .variants table.list th { padding: 6px 10px; }
.chart { width: 100%; height: 140px; display: block; background: var(--paper); border-radius: 6px; }
.chart polyline { fill: none; stroke-width: 2; vector-effect: non-scaling-stroke; }
.chart .sms { stroke: var(--accent); }
.chart .email { stroke: var(--ok); }
.chart .limit { stroke: var(--bad); stroke-dasharray: 6 4; stroke-width: 1; vector-effect: non-scaling-stroke; }
.legend { display: inline-flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.key { display: inline-block; width: 14px; height: 3px; margin-left: 8px; vertical-align: middle; }
.key.sms { background: var(--accent); }
.key.email { background: var(--ok); }
.key.limit { background: var(--bad); }
.small { font-size: 0.85em; margin-top: 6px; }
</style>

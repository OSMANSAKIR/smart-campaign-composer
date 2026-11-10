<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ErrorNotice from '../components/ErrorNotice.vue'
import StatusPill from '../components/StatusPill.vue'
import { api } from '../lib/api'
import { describeError, type UserMessage } from '../lib/errors'
import { formatInt, formatTime } from '../lib/format'
import { links } from '../lib/router'
import type { CampaignSummary } from '../lib/types'

const items = ref<CampaignSummary[]>([])
const cursor = ref<string | null>(null)
const loading = ref(true)
const loadingMore = ref(false)
const error = ref<UserMessage | null>(null)

async function load(more = false) {
  if (more) loadingMore.value = true
  else loading.value = true
  error.value = null
  try {
    const page = await api.listCampaigns({ limit: 20, cursor: more ? cursor.value : null })
    items.value = more ? [...items.value, ...page.items] : page.items
    cursor.value = page.next_cursor
  } catch (e) {
    error.value = describeError(e)
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function href(c: CampaignSummary): string {
  const sent = ['scheduled', 'sending', 'completed', 'completed_with_failures', 'cancelled'].includes(c.status)
  return sent ? links.live(c.id) : links.composer(c.id)
}

onMounted(() => load())
</script>

<template>
  <div class="shell">
    <div class="page-head">
      <div>
        <h1>Campaigns</h1>
        <p>Write a brief, check the audience, approve, and watch delivery.</p>
      </div>
      <a class="btn primary" :href="links.new()" style="text-decoration: none">New campaign</a>
    </div>

    <div v-if="loading" class="empty" role="status">
      <span class="spinner" aria-hidden="true" />
      <p>Loading campaigns…</p>
    </div>
    <ErrorNotice v-else-if="error" :error="error" @retry="load()" />
    <div v-else-if="items.length === 0" class="empty card">
      <p>No campaigns yet. Start with a brief and Claude will propose the audience.</p>
      <a class="btn primary" :href="links.new()" style="text-decoration: none">Create the first campaign</a>
    </div>
    <div v-else class="card" style="padding: 6px 8px">
      <table class="list">
        <caption class="sr-only">Campaigns, newest first</caption>
        <thead>
          <tr><th scope="col">Brief</th><th scope="col">Status</th><th scope="col">Audience</th><th scope="col">Created</th></tr>
        </thead>
        <tbody>
          <tr v-for="c in items" :key="c.id">
            <td>
              <a :href="href(c)" class="brief-cell">{{ c.brief }}</a>
            </td>
            <td>
              <StatusPill :status="c.status" />
              <div v-if="c.status === 'scheduled' && c.scheduled_at" class="muted num" data-testid="scheduled-at">
                Scheduled for {{ formatTime(c.scheduled_at) }}
              </div>
            </td>
            <td class="num">{{ c.audience_total === null ? '—' : formatInt(c.audience_total) }}</td>
            <td class="muted num">{{ formatTime(c.created_at) }}</td>
          </tr>
        </tbody>
      </table>
      <div v-if="cursor" class="row" style="padding: 8px 12px">
        <button class="btn" type="button" :disabled="loadingMore" @click="load(true)">
          {{ loadingMore ? 'Loading…' : 'Show older campaigns' }}
        </button>
      </div>
    </div>
  </div>
</template>

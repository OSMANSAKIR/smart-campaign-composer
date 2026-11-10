<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../lib/api'
import { describeError, type UserMessage } from '../lib/errors'
import { formatClock } from '../lib/format'
import type { Channel, MessageItem } from '../lib/types'
import ErrorNotice from './ErrorNotice.vue'

// Per-message list with the variant each person got (B-2, api.md §9.3). Loaded when opened, not streamed:
// the live numbers above are the dashboard; this is for looking up individual messages.
const props = defineProps<{ campaignId: string; hasVariants: boolean }>()

const open = ref(false)
const items = ref<MessageItem[]>([])
const cursor = ref<string | null>(null)
const loading = ref(false)
const error = ref<UserMessage | null>(null)
const status = ref('')
const channel = ref<'' | Channel>('')

const STATUSES = ['queued', 'sending', 'retry', 'sent', 'failed', 'skipped_no_consent', 'cancelled']

async function load(more = false) {
  loading.value = true
  error.value = null
  try {
    const page = await api.listMessages(props.campaignId, {
      status: status.value || undefined,
      channel: channel.value || undefined,
      limit: 50,
      cursor: more ? cursor.value : null,
    })
    items.value = more ? [...items.value, ...page.items] : page.items
    cursor.value = page.next_cursor
  } catch (e) {
    error.value = describeError(e)
  } finally {
    loading.value = false
  }
}

function toggle(e: Event) {
  open.value = (e.target as HTMLDetailsElement).open
  if (open.value && items.value.length === 0) void load()
}
</script>

<template>
  <details class="card" :open="open" @toggle="toggle">
    <summary style="cursor: pointer; font-weight: 650">Messages</summary>
    <div v-if="open" class="stack" style="margin-top: 12px">
      <div class="row">
        <label class="row" style="gap: 6px">
          <span>Status</span>
          <select v-model="status" aria-label="Filter messages by status" @change="load()">
            <option value="">All</option>
            <option v-for="s in STATUSES" :key="s" :value="s">{{ s.replace(/_/g, ' ') }}</option>
          </select>
        </label>
        <label class="row" style="gap: 6px">
          <span>Channel</span>
          <select v-model="channel" aria-label="Filter messages by channel" @change="load()">
            <option value="">All</option>
            <option value="sms">SMS</option>
            <option value="email">Email</option>
          </select>
        </label>
        <button class="btn" type="button" :disabled="loading" @click="load()">Refresh</button>
      </div>

      <ErrorNotice v-if="error" :error="error" @retry="load()" />
      <p v-else-if="!loading && items.length === 0" class="muted">No messages match.</p>

      <div v-else class="scroll">
        <table class="list num" aria-label="Messages">
          <thead>
            <tr>
              <th scope="col">User</th>
              <th scope="col">Channel</th>
              <th v-if="hasVariants" scope="col">Variant</th>
              <th scope="col">Status</th>
              <th scope="col">Attempts</th>
              <th scope="col">Sent at</th>
              <th scope="col">Last error</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in items" :key="m.id">
              <td>{{ m.user_id }}</td>
              <td>{{ m.channel === 'sms' ? 'SMS' : 'Email' }}</td>
              <td v-if="hasVariants">{{ m.variant ? m.variant.toUpperCase() : '' }}</td>
              <td>{{ m.status.replace(/_/g, ' ') }}</td>
              <td>{{ m.attempts }}</td>
              <td>{{ m.sent_at ? formatClock(m.sent_at) : '' }}</td>
              <td>{{ m.last_error ?? '' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="row">
        <button v-if="cursor" class="btn" type="button" :disabled="loading" @click="load(true)">Load more</button>
        <span v-if="loading" class="muted" role="status"><span class="spinner" aria-hidden="true" /> Loading…</span>
      </div>
    </div>
  </details>
</template>

<style scoped>
.scroll { overflow-x: auto; }
select { font: inherit; padding: 6px 8px; border: 1px solid #b7bfce; border-radius: 6px; background: #fff; }
</style>

<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { formatInt } from '../lib/format'
import type { Campaign, Channel } from '../lib/types'
import type { Gate } from '../lib/approveGate'

const props = defineProps<{
  campaign: Campaign
  channels: Channel[]
  gate: Gate
  recipients: { sms: number; email: number }
  approving: boolean
}>()
const emit = defineEmits<{ toggle: [Channel, boolean]; confirm: []; back: [] }>()

const dialog = ref<HTMLDialogElement | null>(null)
const opener = ref<HTMLButtonElement | null>(null)

const audience = () => props.campaign.segment?.audience

async function open() {
  dialog.value?.showModal() // native modal: focus is trapped, Esc closes, background is inert
  await nextTick()
}
function close() {
  dialog.value?.close()
  opener.value?.focus()
}
function confirm() {
  emit('confirm')
}
defineExpose({ close })
</script>

<template>
  <section class="stack" aria-labelledby="approve-h">
    <div class="card stack">
      <header><h2 id="approve-h">Approve and send</h2></header>

      <fieldset style="border: 0; padding: 0; margin: 0" class="stack">
        <legend style="font-weight: 600; margin-bottom: 8px">Send on</legend>
        <label class="row" style="gap: 10px">
          <input
            type="checkbox"
            :checked="channels.includes('sms')"
            :disabled="(audience()?.sms_reachable ?? 0) === 0"
            @change="emit('toggle', 'sms', ($event.target as HTMLInputElement).checked)"
          />
          <span>SMS <span class="muted num">({{ formatInt(audience()?.sms_reachable ?? 0) }} people with consent)</span></span>
        </label>
        <label class="row" style="gap: 10px">
          <input
            type="checkbox"
            :checked="channels.includes('email')"
            :disabled="(audience()?.email_reachable ?? 0) === 0"
            @change="emit('toggle', 'email', ($event.target as HTMLInputElement).checked)"
          />
          <span>Email <span class="muted num">({{ formatInt(audience()?.email_reachable ?? 0) }} people with consent)</span></span>
        </label>
      </fieldset>

      <p class="muted">
        Only people who agreed to a channel get messages on that channel. Version
        <b>{{ campaign.content?.version }}</b> of the content will be sent.
      </p>

      <div class="row">
        <button ref="opener" class="btn primary" type="button" :disabled="!gate.enabled" aria-describedby="gate-reason" @click="open">
          Review and approve
        </button>
        <button class="btn" type="button" @click="emit('back')">Back to content</button>
      </div>
      <p id="gate-reason" class="muted" role="status" aria-live="polite">
        {{ gate.enabled ? 'Everything is valid. Nothing is sent until you confirm.' : gate.reason }}
      </p>
    </div>

    <dialog ref="dialog" aria-labelledby="confirm-h" @cancel="opener?.focus()">
      <div class="inner">
        <h2 id="confirm-h">Send this campaign?</h2>
        <p>You are about to queue these messages. This cannot be undone.</p>
        <div class="big-counts num">
          <div v-if="channels.includes('sms')"><span>SMS to</span><b>{{ formatInt(recipients.sms) }} people</b></div>
          <div v-if="channels.includes('email')"><span>Email to</span><b>{{ formatInt(recipients.email) }} people</b></div>
        </div>
        <div class="actions">
          <button class="btn" type="button" :disabled="approving" @click="close">Cancel</button>
          <button class="btn primary" type="button" :disabled="approving || !gate.enabled" @click="confirm">
            <span v-if="approving" class="spinner" aria-hidden="true" />
            {{ approving ? 'Sending…' : 'Approve and send' }}
          </button>
        </div>
      </div>
    </dialog>
  </section>
</template>

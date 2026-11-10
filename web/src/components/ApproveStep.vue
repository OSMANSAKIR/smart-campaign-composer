<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { formatInt } from '../lib/format'
import { parseLocalInput, scheduleBounds } from '../lib/schedule'
import type { Campaign, Channel, SendVariant } from '../lib/types'
import type { Gate } from '../lib/approveGate'
import { variantProblem, variantsOf } from '../lib/variants'

const props = defineProps<{
  campaign: Campaign
  channels: Channel[]
  gate: Gate
  recipients: { sms: number; email: number }
  approving: boolean
  variant: SendVariant
  scheduleMode: 'now' | 'later'
  scheduleAt: string
  scheduleProblem: string
}>()
const emit = defineEmits<{
  toggle: [Channel, boolean]
  confirm: []
  back: []
  'update:variant': [SendVariant]
  'update:scheduleMode': ['now' | 'later']
  'update:scheduleAt': [string]
}>()

const dialog = ref<HTMLDialogElement | null>(null)
const opener = ref<HTMLButtonElement | null>(null)
const bounds = ref(scheduleBounds(new Date()))

const audience = () => props.campaign.segment?.audience
const variants = computed(() => (props.campaign.content ? variantsOf(props.campaign.content) : null))

// B options are disabled when there is no B or it is invalid for a selected channel (B-2).
const OPTIONS: { value: SendVariant; label: string }[] = [
  { value: 'a', label: 'Send A' },
  { value: 'b', label: 'Send B' },
  { value: 'ab', label: 'A/B test (50/50)' },
]
function optionProblem(v: SendVariant): string {
  return props.campaign.content ? variantProblem(props.campaign.content, v, props.channels) : ''
}
const variantName = (k: 'a' | 'b') => variants.value?.[k]?.label || `Variant ${k.toUpperCase()}`
const choiceText = computed(() =>
  props.variant === 'ab'
    ? `A/B test: ${variantName('a')} and ${variantName('b')}, split 50/50 per person`
    : `${props.variant === 'a' ? 'Variant A' : 'Variant B'}: ${variantName(props.variant)}`,
)
const whenText = computed(() => {
  if (props.scheduleMode === 'now') return 'Right away'
  const d = parseLocalInput(props.scheduleAt)
  return d ? d.toLocaleString('en-GB', { dateStyle: 'medium', timeStyle: 'short' }) : 'Pick a time'
})

async function open() {
  bounds.value = scheduleBounds(new Date()) // the window moves with the clock; recompute when the dialog opens
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
function setMode(m: 'now' | 'later') {
  emit('update:scheduleMode', m)
  if (m === 'later' && !props.scheduleAt) {
    bounds.value = scheduleBounds(new Date())
    emit('update:scheduleAt', bounds.value.min)
  }
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

      <fieldset style="border: 0; padding: 0; margin: 0" class="stack" aria-describedby="ab-note">
        <legend style="font-weight: 600; margin-bottom: 8px">What to send</legend>
        <label v-for="o in OPTIONS" :key="o.value" class="row" style="gap: 10px">
          <input
            type="radio"
            name="send-variant"
            :value="o.value"
            :checked="variant === o.value"
            :disabled="o.value !== 'a' && !!optionProblem(o.value) && variant !== o.value"
            @change="emit('update:variant', o.value)"
          />
          <span>
            {{ o.label }}
            <span v-if="o.value !== 'a' && optionProblem(o.value)" class="muted">({{ optionProblem(o.value) }})</span>
          </span>
        </label>
        <p id="ab-note" class="muted">
          Delivery is split 50/50 per user; the mock provider has no open or click data.
        </p>
      </fieldset>

      <fieldset style="border: 0; padding: 0; margin: 0" class="stack">
        <legend style="font-weight: 600; margin-bottom: 8px">When</legend>
        <label class="row" style="gap: 10px">
          <input type="radio" name="send-when" value="now" :checked="scheduleMode === 'now'" @change="setMode('now')" />
          <span>Send now</span>
        </label>
        <label class="row" style="gap: 10px">
          <input type="radio" name="send-when" value="later" :checked="scheduleMode === 'later'" @change="setMode('later')" />
          <span>Schedule</span>
        </label>
        <div v-if="scheduleMode === 'later'" class="stack" style="gap: 6px">
          <label class="field" for="send-at">
            <span>Send at (your local time)</span>
          </label>
          <input
            id="send-at"
            type="datetime-local"
            class="dt"
            :value="scheduleAt"
            :min="bounds.min"
            :max="bounds.max"
            aria-describedby="send-at-hint"
            @input="emit('update:scheduleAt', ($event.target as HTMLInputElement).value)"
          />
          <p id="send-at-hint" class="muted" role="status">
            {{ scheduleProblem || 'Between one minute and seven days from now. You can stop it before it starts.' }}
          </p>
        </div>
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
        <h2 id="confirm-h">{{ scheduleMode === 'later' ? 'Schedule this campaign?' : 'Send this campaign?' }}</h2>
        <p>
          {{
            scheduleMode === 'later'
              ? 'The audience is fixed now and the messages go out at the chosen time. You can stop it before then.'
              : 'You are about to queue these messages. This cannot be undone.'
          }}
        </p>
        <div class="big-counts num">
          <div v-if="channels.includes('sms')"><span>SMS to</span><b>{{ formatInt(recipients.sms) }} people</b></div>
          <div v-if="channels.includes('email')"><span>Email to</span><b>{{ formatInt(recipients.email) }} people</b></div>
          <div><span>Content</span><b>{{ choiceText }}</b></div>
          <div><span>When</span><b>{{ whenText }}</b></div>
        </div>
        <div class="actions">
          <button class="btn" type="button" :disabled="approving" @click="close">Cancel</button>
          <button class="btn primary" type="button" :disabled="approving || !gate.enabled" @click="confirm">
            <span v-if="approving" class="spinner" aria-hidden="true" />
            {{ approving ? 'Sending…' : scheduleMode === 'later' ? 'Approve and schedule' : 'Approve and send' }}
          </button>
        </div>
      </div>
    </dialog>
  </section>
</template>

<style scoped>
.dt { font: inherit; padding: 8px 10px; border: 1px solid #b7bfce; border-radius: 6px; max-width: 280px; }
.big-counts b { text-align: right; }
</style>

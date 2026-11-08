<script setup lang="ts">
import { computed } from 'vue'
import { formatCost, formatInt } from '../lib/format'
import { renderPreview } from '../lib/templateVars'
import type { Campaign, ContentInput, LlmMeta, ValidationReport } from '../lib/types'
import ContentField from './ContentField.vue'

// `form` is the composer's reactive editor state; the fields bind to it directly.
const props = defineProps<{
  campaign: Campaign
  form: ContentInput
  stream: { active: boolean; repairing: boolean; attempt: number; started: boolean }
  report: ValidationReport | null
  reportIsCurrent: boolean
  validating: boolean
  hasUnsavedEdits: boolean
  saving: boolean
  llm: LlmMeta | null
}>()
defineEmits<{ generate: []; stop: []; save: []; next: [] }>()

// While a check is pending, keep showing the previous numbers' issues dimmed? No: show none for stale text,
// so the marketer never sees a badge that belongs to different text.
const current = computed(() => (props.reportIsCurrent ? props.report : null))
const issuesFor = (path: string) => [
  ...(current.value?.errors ?? []).filter((i) => i.path === path),
  ...(current.value?.warnings ?? []).filter((i) => i.path === path),
]

const summary = computed(() => {
  if (props.stream.active) return props.stream.repairing ? 'Fixing the draft…' : 'Claude is writing…'
  if (props.validating || !props.reportIsCurrent) return 'Checking…'
  const r = props.report
  if (!r) return ''
  if (r.valid) return 'Valid for SMS and email.'
  const ok = [r.valid_for.sms ? 'SMS' : '', r.valid_for.email ? 'email' : ''].filter(Boolean)
  const n = r.errors.length
  return `${n} ${n === 1 ? 'error' : 'errors'} to fix.${ok.length ? ` Valid for ${ok.join(' and ')}.` : ''}`
})

const values = computed(() => current.value?.worst_case ?? props.report?.worst_case ?? { first_name: 'Ayşe', city: 'İstanbul' })
const smsPreview = computed(() => renderPreview(props.form.sms_text, values.value))
const subjectPreview = computed(() => renderPreview(props.form.email_subject, values.value))
const bodyPreview = computed(() => renderPreview(props.form.email_body, values.value))
const hasContent = computed(() => props.form.sms_text || props.form.email_subject || props.form.email_body)
</script>

<template>
  <section class="stack" aria-labelledby="content-h">
    <div class="card stack">
      <header>
        <h2 id="content-h">Write the messages</h2>
        <div class="row">
          <button v-if="!stream.active" class="btn primary" type="button" @click="$emit('generate')">
            {{ campaign.content ? 'Write a new draft with Claude' : 'Write with Claude' }}
          </button>
          <button v-else class="btn" type="button" @click="$emit('stop')">Stop</button>
        </div>
      </header>

      <div v-if="stream.active" class="notice info" role="status">
        <strong>
          <span class="spinner" aria-hidden="true" />
          {{ stream.repairing ? ` Fixing… (attempt ${stream.attempt})` : ' Claude is writing' }}
        </strong>
        <p v-if="stream.repairing">The first draft broke a rule, so Claude is rewriting it. The text below restarts.</p>
        <p v-else>The fields fill as the text arrives. The final text is checked once it is complete.</p>
      </div>

      <div v-else-if="!hasContent" class="empty">
        <p>No content yet. Let Claude draft the SMS and email, or type your own below.</p>
      </div>

      <p class="sr-only" role="status" aria-live="polite">{{ summary }}</p>

      <ContentField
        id="sms-text"
        v-model="form.sms_text"
        label="SMS text"
        :multiline="true"
        :rows="3"
        :limit="160"
        :report="current?.fields.sms_text ?? null"
        :issues="issuesFor('sms_text')"
        :streaming="stream.active"
        sms
      />
      <ContentField
        id="email-subject"
        v-model="form.email_subject"
        label="Email subject"
        :multiline="false"
        :limit="80"
        :report="current?.fields.email_subject ?? null"
        :issues="issuesFor('email_subject')"
        :streaming="stream.active"
      />
      <ContentField
        id="email-body"
        v-model="form.email_body"
        label="Email body"
        :multiline="true"
        :rows="9"
        :limit="0"
        :report="current?.fields.email_body ?? null"
        :issues="issuesFor('email_body')"
        :streaming="stream.active"
      />

      <div class="row">
        <button class="btn primary" type="button" :disabled="!hasUnsavedEdits || saving || stream.active" @click="$emit('save')">
          <span v-if="saving" class="spinner" aria-hidden="true" />
          {{ saving ? 'Saving…' : 'Save changes' }}
        </button>
        <span class="muted" role="status">
          <template v-if="stream.active">{{ summary }}</template>
          <template v-else-if="hasUnsavedEdits">Unsaved changes. {{ summary }}</template>
          <template v-else-if="campaign.content">Saved as version {{ campaign.content.version }}. {{ summary }}</template>
        </span>
        <span class="grow" />
        <button
          class="btn"
          type="button"
          :disabled="!campaign.content || hasUnsavedEdits || stream.active"
          @click="$emit('next')"
        >
          Continue to approve
        </button>
      </div>
    </div>

    <div v-if="hasContent" class="card">
      <header>
        <h3>Preview</h3>
        <span class="muted">Using the longest values in this audience: {{ values.first_name }}, {{ values.city }}</span>
      </header>
      <div class="content-grid">
        <div class="stack" style="gap: 8px">
          <h3>SMS</h3>
          <div class="bubble">{{ smsPreview || 'Nothing to show yet' }}</div>
        </div>
        <div class="stack" style="gap: 8px">
          <h3>Email</h3>
          <div class="mail">
            <div class="subj">{{ subjectPreview || 'No subject yet' }}</div>
            <div class="body">{{ bodyPreview || 'No body yet' }}</div>
          </div>
        </div>
      </div>
    </div>

    <div v-if="llm" class="card">
      <header><h3>How Claude produced this</h3></header>
      <dl class="meta">
        <div><dt>Provider</dt><dd>{{ llm.provider }}</dd></div>
        <div><dt>Model</dt><dd>{{ llm.model }}</dd></div>
        <div><dt>Latency</dt><dd class="num">{{ formatInt(llm.latency_ms) }} ms</dd></div>
        <div><dt>Attempts</dt><dd class="num">{{ llm.attempts }}{{ llm.repaired ? ' (repaired)' : '' }}</dd></div>
        <div><dt>Tokens in / out</dt><dd class="num">{{ formatInt(llm.input_tokens) }} / {{ formatInt(llm.output_tokens) }}</dd></div>
        <div><dt>Cost</dt><dd class="num">{{ formatCost(llm.cost_usd) }}</dd></div>
      </dl>
    </div>
  </section>
</template>

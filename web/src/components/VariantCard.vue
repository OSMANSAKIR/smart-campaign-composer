<script setup lang="ts">
import { computed, ref } from 'vue'
import { renderPreview } from '../lib/templateVars'
import type { ContentVariant, ValidationReport, VariantInput, VariantKey } from '../lib/types'
import ContentField from './ContentField.vue'

// One variant of the content (F1, B-2): its own label, SMS, subject and body, with counters and validation.
// `form` is the composer's reactive state for this variant; the fields bind to it directly.
const props = defineProps<{
  k: VariantKey
  form: VariantInput
  // The server's report for exactly the text in the editor; null while a check is pending.
  report: ValidationReport | null
  // The last saved row of this variant (for the "refined" history hint); null when none.
  savedRow: ContentVariant | null
  savedVersion: number | null
  streaming: boolean
  // True while this card's own text is being rewritten by a refine call.
  refining: boolean
  // Why "Rewrite this variant" is unavailable right now ('' when it is available).
  refineBlocked: string
}>()
const emit = defineEmits<{ refine: [string] }>()

const KEY = computed(() => props.k.toUpperCase())
const instruction = ref('')
const MAX_INSTRUCTION = 500
const instructionRunes = computed(() => [...instruction.value].length)
const instructionOk = computed(() => instructionRunes.value >= 1 && instructionRunes.value <= MAX_INSTRUCTION)

const issuesFor = (path: string) => [
  ...(props.report?.errors ?? []).filter((i) => i.path === path),
  ...(props.report?.warnings ?? []).filter((i) => i.path === path),
]

const values = computed(() => props.report?.worst_case ?? { first_name: 'Ayşe', city: 'İstanbul' })
const smsPreview = computed(() => renderPreview(props.form.sms_text, values.value))
const subjectPreview = computed(() => renderPreview(props.form.email_subject, values.value))
const bodyPreview = computed(() => renderPreview(props.form.email_body, values.value))
const hasText = computed(() => props.form.sms_text || props.form.email_subject || props.form.email_body)

const history = computed(() => {
  const r = props.savedRow
  if (!r || props.savedVersion === null) return ''
  if (r.source === 'refined' && r.instruction) return `Version ${props.savedVersion} (refined: ${r.instruction})`
  return ''
})

function submit() {
  if (!instructionOk.value || props.refineBlocked || props.streaming) return
  emit('refine', instruction.value)
}
</script>

<template>
  <section class="card stack variant" :aria-labelledby="`variant-${k}-h`" :data-variant="k">
    <header>
      <h3 :id="`variant-${k}-h`">
        <span class="pill info">{{ KEY }}</span> Variant {{ KEY }}
      </h3>
      <span v-if="history" class="muted num">{{ history }}</span>
    </header>

    <label class="field">
      <span>Name of variant {{ KEY }}</span>
      <input
        :id="`label-${k}`"
        v-model="form.label"
        type="text"
        maxlength="40"
        :readonly="streaming"
        :class="{ 'streaming-field': streaming }"
      />
    </label>

    <ContentField
      :id="`sms-text-${k}`"
      v-model="form.sms_text"
      :label="`SMS text, variant ${KEY}`"
      :multiline="true"
      :rows="3"
      :limit="160"
      :report="report?.fields.sms_text ?? null"
      :issues="issuesFor('sms_text')"
      :streaming="streaming"
      sms
    />
    <ContentField
      :id="`email-subject-${k}`"
      v-model="form.email_subject"
      :label="`Email subject, variant ${KEY}`"
      :multiline="false"
      :limit="80"
      :report="report?.fields.email_subject ?? null"
      :issues="issuesFor('email_subject')"
      :streaming="streaming"
    />
    <ContentField
      :id="`email-body-${k}`"
      v-model="form.email_body"
      :label="`Email body, variant ${KEY}`"
      :multiline="true"
      :rows="8"
      :limit="0"
      :report="report?.fields.email_body ?? null"
      :issues="issuesFor('email_body')"
      :streaming="streaming"
    />

    <details v-if="hasText" class="preview">
      <summary>Preview of variant {{ KEY }}</summary>
      <div class="stack" style="gap: 8px; margin-top: 8px">
        <div class="bubble">{{ smsPreview || 'Nothing to show yet' }}</div>
        <div class="mail">
          <div class="subj">{{ subjectPreview || 'No subject yet' }}</div>
          <div class="body">{{ bodyPreview || 'No body yet' }}</div>
        </div>
        <p class="muted small">Using the longest values in this audience: {{ values.first_name }}, {{ values.city }}</p>
      </div>
    </details>

    <form class="stack refine" style="gap: 6px" @submit.prevent="submit">
      <label :for="`refine-${k}`" style="font-weight: 600">Ask Claude to rewrite variant {{ KEY }}</label>
      <input
        :id="`refine-${k}`"
        v-model="instruction"
        type="text"
        :disabled="streaming"
        placeholder="For example: shorter, friendlier, mention free shipping"
        :aria-describedby="`refine-${k}-count refine-${k}-hint`"
      />
      <div class="row" style="justify-content: space-between">
        <span :id="`refine-${k}-count`" class="muted num" :style="instructionRunes > MAX_INSTRUCTION ? 'color: var(--bad)' : ''">
          {{ instructionRunes }} / {{ MAX_INSTRUCTION }}
        </span>
        <button class="btn" type="submit" :disabled="!instructionOk || !!refineBlocked || streaming">
          <span v-if="refining" class="spinner" aria-hidden="true" />
          {{ refining ? 'Rewriting…' : `Rewrite variant ${KEY}` }}
        </button>
      </div>
      <p :id="`refine-${k}-hint`" class="muted small" role="status">
        {{ refineBlocked || 'Only this variant changes; the other one is kept. Earlier versions are kept too.' }}
      </p>
    </form>
  </section>
</template>

<style scoped>
.small { font-size: 0.85em; }
.preview summary { cursor: pointer; font-weight: 600; color: var(--accent); }
.refine { border-top: 1px solid var(--line-soft); padding-top: 12px; }
</style>

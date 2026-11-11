<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { renderPreview } from '../lib/templateVars'
import { LOCALES, LOCALE_NAME } from '../lib/localization'
import type { ContentVariant, Language, LocalizedCopy, ValidationReport, VariantInput, VariantKey } from '../lib/types'
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
  // B-3: the language of the primary copy, and the adaptation to the other language. `showTabs` is true when
  // somebody in the audience reads the other language or an adaptation exists.
  primaryLocale: Language
  showTabs: boolean
  localForm: VariantInput
  localReport: ValidationReport | null
  localSaved: LocalizedCopy | null
  localStale: boolean
  // True while the adaptation is being written; why "Re-adapt" is unavailable ('' when it is available).
  localizing: boolean
  localizeBlocked: string
}>()
const emit = defineEmits<{ refine: [string]; localize: [] }>()

// B-3: TR | EN tabs. The primary tab is the one the campaign is written in.
const tab = ref<Language>(props.primaryLocale)
watch(() => props.primaryLocale, (l) => (tab.value = l))
watch(() => props.localizing, (on) => {
  if (on) tab.value = otherOf(props.primaryLocale)
})
const otherOf = (l: Language): Language => (l === 'tr' ? 'en' : 'tr')
const onOther = computed(() => props.showTabs && tab.value !== props.primaryLocale)
const cur = computed(() => (onOther.value ? props.localForm : props.form))
const curReport = computed(() => (onOther.value ? props.localReport : props.report))
const sfx = computed(() => (onOther.value ? `-${tab.value}` : ''))
const otherName = computed(() => LOCALE_NAME[otherOf(props.primaryLocale)])
const emptyOther = computed(() => onOther.value && !props.localSaved && !props.localForm.sms_text && !props.localForm.email_subject && !props.localForm.email_body && !props.localizing)
function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
  tab.value = otherOf(tab.value)
  ;(e.currentTarget as HTMLElement).parentElement?.querySelector<HTMLElement>(`[data-tab="${tab.value}"]`)?.focus()
}

const KEY = computed(() => props.k.toUpperCase())
const instruction = ref('')
const MAX_INSTRUCTION = 500
const instructionRunes = computed(() => [...instruction.value].length)
// A new saved version (the rewrite arrived) used the instruction up.
watch(() => props.savedVersion, () => (instruction.value = ''))
const instructionOk = computed(() => instructionRunes.value >= 1 && instructionRunes.value <= MAX_INSTRUCTION)

const issuesFor = (path: string) => [
  ...(curReport.value?.errors ?? []).filter((i) => i.path === path),
  ...(curReport.value?.warnings ?? []).filter((i) => i.path === path),
]

const values = computed(() => curReport.value?.worst_case ?? { first_name: 'Ayşe', city: 'İstanbul' })
const smsPreview = computed(() => renderPreview(cur.value.sms_text, values.value))
const subjectPreview = computed(() => renderPreview(cur.value.email_subject, values.value))
const bodyPreview = computed(() => renderPreview(cur.value.email_body, values.value))
const hasText = computed(() => cur.value.sms_text || cur.value.email_subject || cur.value.email_body)

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

    <div v-if="showTabs" class="tabs" role="tablist" :aria-label="`Language of variant ${KEY}`">
      <button
        v-for="l in LOCALES"
        :id="`tab-${k}-${l}`"
        :key="l"
        type="button"
        role="tab"
        class="tab"
        :class="{ on: tab === l }"
        :data-tab="l"
        :aria-selected="tab === l"
        :aria-controls="`panel-${k}`"
        :tabindex="tab === l ? 0 : -1"
        :aria-label="`${LOCALE_NAME[l]} version of variant ${KEY}`"
        @click="tab = l"
        @keydown="onKeydown"
      >
        {{ l.toUpperCase() }}
        <span v-if="l !== primaryLocale && localStale" class="pill warn">out of date</span>
      </button>
    </div>

    <div :id="`panel-${k}`" class="stack" :role="showTabs ? 'tabpanel' : undefined" :aria-labelledby="showTabs ? `tab-${k}-${tab}` : undefined">
      <div v-if="onOther && localStale" class="notice warn" role="status">
        <strong>The {{ LOCALE_NAME[primaryLocale] }} text changed after this version was written.</strong>
        <p>Re-adapt it, or edit it here yourself. Approving for each recipient's language needs a current version.</p>
      </div>
      <div v-if="emptyOther" class="empty" role="status">
        <p>There is no {{ otherName }} version of variant {{ KEY }} yet.</p>
      </div>
      <div v-if="onOther" class="row">
        <button class="btn" type="button" :disabled="!!localizeBlocked || localizing || streaming" @click="emit('localize')">
          <span v-if="localizing" class="spinner" aria-hidden="true" />
          {{ localizing ? 'Adapting…' : localSaved ? `Re-adapt to ${otherName}` : `Write the ${otherName} version` }}
        </button>
        <span class="muted small" role="status">{{ localizeBlocked || 'Claude adapts both variants from the saved text.' }}</span>
      </div>
    <label class="field">
      <span>Name of variant {{ KEY }}<template v-if="onOther"> ({{ LOCALE_NAME[tab] }})</template></span>
      <input
        :id="`label-${k}${sfx}`"
        v-model="cur.label"
        type="text"
        maxlength="40"
        :readonly="streaming"
        :class="{ 'streaming-field': streaming }"
      />
    </label>

    <ContentField
      :id="`sms-text-${k}${sfx}`"
      v-model="cur.sms_text"
      :label="`SMS text, variant ${KEY}${onOther ? ` (${LOCALE_NAME[tab]})` : ''}`"
      :multiline="true"
      :rows="3"
      :limit="160"
      :report="curReport?.fields.sms_text ?? null"
      :issues="issuesFor('sms_text')"
      :streaming="streaming"
      sms
    />
    <ContentField
      :id="`email-subject-${k}${sfx}`"
      v-model="cur.email_subject"
      :label="`Email subject, variant ${KEY}${onOther ? ` (${LOCALE_NAME[tab]})` : ''}`"
      :multiline="false"
      :limit="80"
      :report="curReport?.fields.email_subject ?? null"
      :issues="issuesFor('email_subject')"
      :streaming="streaming"
    />
    <ContentField
      :id="`email-body-${k}${sfx}`"
      v-model="cur.email_body"
      :label="`Email body, variant ${KEY}${onOther ? ` (${LOCALE_NAME[tab]})` : ''}`"
      :multiline="true"
      :rows="8"
      :limit="0"
      :report="curReport?.fields.email_body ?? null"
      :issues="issuesFor('email_body')"
      :streaming="streaming"
    />

    <details v-if="hasText" class="preview">
      <summary>Preview of variant {{ KEY }}<template v-if="onOther"> ({{ LOCALE_NAME[tab] }})</template></summary>
      <div class="stack" style="gap: 8px; margin-top: 8px">
        <div class="bubble">{{ smsPreview || 'Nothing to show yet' }}</div>
        <div class="mail">
          <div class="subj">{{ subjectPreview || 'No subject yet' }}</div>
          <div class="body">{{ bodyPreview || 'No body yet' }}</div>
        </div>
        <p class="muted small">Using the longest values in this audience: {{ values.first_name }}, {{ values.city }}</p>
      </div>
    </details>
    </div>

    <form v-if="!onOther" class="stack refine" style="gap: 6px" @submit.prevent="submit">
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
.tabs { display: flex; gap: 4px; border-bottom: 1px solid var(--line-soft); }
.tab { font: inherit; background: none; border: 0; border-bottom: 3px solid transparent; padding: 6px 14px; cursor: pointer; font-weight: 600; color: var(--muted, #555); display: inline-flex; gap: 8px; align-items: center; }
.tab.on { color: var(--accent); border-bottom-color: var(--accent); }
.refine { border-top: 1px solid var(--line-soft); padding-top: 12px; }
</style>

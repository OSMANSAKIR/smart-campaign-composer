<script setup lang="ts">
import { computed } from 'vue'
import { LOCALE_NAME, otherLocale } from '../lib/localization'
import type { Campaign, LlmMeta, LocalizedCopy, ValidationReport, VariantInput, VariantKey } from '../lib/types'
import { variantsOf } from '../lib/variants'
import LlmPanel from './LlmPanel.vue'
import VariantCard from './VariantCard.vue'

// `forms` is the composer's reactive editor state; the cards bind to it directly.
const props = defineProps<{
  campaign: Campaign
  forms: Record<VariantKey, VariantInput>
  hasB: boolean
  checks: Record<VariantKey, { report: ValidationReport | null; key: string; validating: boolean }>
  reportIsCurrent: (k: VariantKey) => boolean
  stream: { active: boolean; repairing: boolean; attempt: number; started: boolean; target: 'all' | VariantKey | 'localize' }
  validating: boolean
  hasUnsavedEdits: boolean
  saving: boolean
  llm: LlmMeta | null
  // B-3: the adaptation to the campaign's other language, one editor state per variant. All optional so a
  // plain content step (no other-language recipients) needs none of it.
  localForms?: Record<VariantKey, VariantInput>
  localChecks?: Record<VariantKey, { report: ValidationReport | null; key: string; validating: boolean }>
  localReportIsCurrent?: (k: VariantKey) => boolean
  localStale?: (k: VariantKey) => boolean
  savedLocalized?: (k: VariantKey) => LocalizedCopy | null
  otherRecipients?: number
  hasLocalized?: boolean
}>()
defineEmits<{ generate: []; stop: []; save: []; next: []; refine: [VariantKey, string]; localize: [] }>()

const emptyForm: VariantInput = { label: '', sms_text: '', email_subject: '', email_body: '' }
const otherName = computed(() => LOCALE_NAME[otherLocale(props.campaign.language)])
// The tabs show when somebody reads the other language or an adaptation already exists.
const showTabs = computed(() => (props.otherRecipients ?? 0) > 0 || !!props.hasLocalized)
const localizing = computed(() => props.stream.active && props.stream.target === 'localize')
const localCurrent = (k: VariantKey) => (props.localReportIsCurrent?.(k) ? (props.localChecks?.[k].report ?? null) : null)
// Adapting works on the saved version, so unsaved edits would be lost: save first (like refine).
const localizeBlocked = computed(() => {
  if (!props.campaign.content) return 'Write the content with Claude first.'
  if (props.stream.active) return 'Wait for Claude to finish.'
  if (props.hasUnsavedEdits) return 'Save your changes first; Claude adapts the saved version.'
  if (!props.hasB) return 'Adapting needs both variants. Write a new draft with Claude.'
  return ''
})

const keys = computed<VariantKey[]>(() => (props.hasB ? ['a', 'b'] : ['a']))
const current = (k: VariantKey) => (props.reportIsCurrent(k) ? props.checks[k].report : null)
const savedVariants = computed(() => (props.campaign.content ? variantsOf(props.campaign.content) : null))

const hasContent = computed(() =>
  keys.value.some((k) => props.forms[k].sms_text || props.forms[k].email_subject || props.forms[k].email_body),
)

function describe(k: VariantKey): string {
  const r = current(k)
  if (!r) return ''
  if (r.valid) return 'Valid for SMS and email.'
  const ok = [r.valid_for.sms ? 'SMS' : '', r.valid_for.email ? 'email' : ''].filter(Boolean)
  const n = r.errors.length
  return `${n} ${n === 1 ? 'error' : 'errors'} to fix.${ok.length ? ` Valid for ${ok.join(' and ')}.` : ''}`
}

const summary = computed(() => {
  if (props.stream.active) return props.stream.repairing ? 'Fixing the draft…' : 'Claude is writing…'
  // An empty editor is never sent for checking, so there is nothing to wait for.
  const anyReport = keys.value.some((k) => props.checks[k].report)
  if (!props.validating && !anyReport && !hasContent.value) return ''
  if (props.validating || keys.value.some((k) => !props.reportIsCurrent(k))) return 'Checking…'
  if (keys.value.length === 1) return describe('a')
  if (keys.value.every((k) => current(k)?.valid)) return 'Both variants are valid for SMS and email.'
  return keys.value.map((k) => `Variant ${k.toUpperCase()}: ${describe(k)}`).join(' ')
})

// After the audience changed the content must be saved again (a new version, validated for the new audience)
// before approval, even when the text is the same (api.md §3).
const needsResave = computed(() => props.campaign.status === 'segmented' && !!props.campaign.content)

// Refine rewrites the saved version of a variant, so unsaved edits would be lost: save first.
const refineBlocked = computed(() => {
  if (!props.campaign.content) return 'Write the content with Claude first.'
  if (props.stream.active) return 'Wait for Claude to finish.'
  if (props.hasUnsavedEdits) return 'Save your changes first; Claude rewrites the saved version.'
  return ''
})
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
          <button
            v-if="!stream.active && (otherRecipients ?? 0) > 0 && campaign.content"
            class="btn"
            type="button"
            :disabled="!!localizeBlocked"
            :title="localizeBlocked"
            @click="$emit('localize')"
          >
            {{ hasLocalized ? `Re-adapt the ${otherName} version` : `Write the ${otherName} version` }}
            ({{ otherRecipients }} {{ otherRecipients === 1 ? 'recipient' : 'recipients' }})
          </button>
          <button v-else-if="stream.active" class="btn" type="button" @click="$emit('stop')">Stop</button>
        </div>
      </header>

      <div v-if="stream.active" class="notice info" role="status">
        <strong>
          <span class="spinner" aria-hidden="true" />
          {{
            stream.repairing
              ? ` Fixing… (attempt ${stream.attempt})`
              : stream.target === 'all'
                ? ' Claude is writing two variants'
                : stream.target === 'localize'
                  ? ` Claude is adapting both variants to ${otherName}`
                  : ` Claude is rewriting variant ${stream.target.toUpperCase()}`
          }}
        </strong>
        <p v-if="stream.repairing">The first draft broke a rule, so Claude is rewriting it. The text below restarts.</p>
        <p v-else>The fields fill as the text arrives. The final text is checked once it is complete.</p>
      </div>

      <div v-else-if="!hasContent" class="empty">
        <p>No content yet. Let Claude draft two variants of the SMS and email, or type your own below.</p>
      </div>

      <p class="sr-only" role="status" aria-live="polite">{{ summary }}</p>

      <p v-if="hasB" class="muted">
        Two variants, A and B. Choose which to send when you approve: A, B, or an even 50/50 split.
      </p>

      <div class="variant-grid" :class="{ two: hasB }">
        <VariantCard
          v-for="k in keys"
          :key="k"
          :k="k"
          :form="forms[k]"
          :report="current(k)"
          :saved-row="savedVariants?.[k] ?? null"
          :saved-version="campaign.content?.version ?? null"
          :streaming="stream.active"
          :refining="stream.active && stream.target === k"
          :refine-blocked="refineBlocked"
          :primary-locale="campaign.language"
          :show-tabs="showTabs"
          :local-form="localForms?.[k] ?? emptyForm"
          :local-report="localCurrent(k)"
          :local-saved="savedLocalized?.(k) ?? null"
          :local-stale="localStale?.(k) ?? false"
          :localizing="localizing"
          :localize-blocked="localizeBlocked"
          @refine="(text: string) => $emit('refine', k, text)"
          @localize="$emit('localize')"
        />
      </div>

      <div class="row">
        <button
          class="btn primary"
          type="button"
          :disabled="(!hasUnsavedEdits && !needsResave) || saving || stream.active"
          @click="$emit('save')"
        >
          <span v-if="saving" class="spinner" aria-hidden="true" />
          {{ saving ? 'Saving…' : needsResave && !hasUnsavedEdits ? 'Re-check and save' : 'Save changes' }}
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

    <LlmPanel v-if="llm" :llm="llm" what="content" />
  </section>
</template>

<style scoped>
/* Two cards side by side on wide screens, stacked on narrow ones. */
.variant-grid { display: grid; gap: 16px; grid-template-columns: minmax(0, 1fr); }
@media (min-width: 900px) {
  .variant-grid.two { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>

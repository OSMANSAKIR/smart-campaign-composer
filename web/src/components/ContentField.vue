<script setup lang="ts">
import { computed, ref } from 'vue'
import { countText } from '../lib/textCount'
import { ALLOWED_VARIABLES, parseTemplate } from '../lib/templateVars'
import type { Issue, TextFieldReport } from '../lib/types'

const text = defineModel<string>({ required: true })
const props = defineProps<{
  id: string
  label: string
  multiline: boolean
  rows?: number
  limit: number
  // Server numbers for the exact text in the editor; null while a check is pending.
  report: TextFieldReport | { runes: number; limit: number } | null
  issues: Issue[]
  streaming: boolean
  sms?: boolean
}>()

const el = ref<HTMLTextAreaElement | HTMLInputElement | null>(null)

const runes = computed(() => countText(text.value).runes)
const worst = computed(() => (props.report && 'worst_case_runes' in props.report ? props.report.worst_case_runes : null))
const smsInfo = computed(() => (props.report && 'segments' in props.report ? props.report : null))
const measured = computed(() => worst.value ?? runes.value)
const pct = computed(() => Math.min(100, Math.round((measured.value / props.limit) * 100)))
const over = computed(() => measured.value > props.limit)

// Instant hint from the shared grammar while the server check is pending (the server stays authoritative).
const localIssues = computed<Issue[]>(() => {
  if (props.report) return []
  return parseTemplate(text.value).errors.map((code) => ({
    path: props.id,
    code,
    message:
      code === 'unknown_variable'
        ? 'Unknown variable. Allowed: ' + ALLOWED_VARIABLES.map((v) => `{{${v}}}`).join(', ')
        : 'Unbalanced braces. Braces are only for variables like {{first_name}}.',
  }))
})
const shown = computed(() => [...props.issues, ...localIssues.value])

function insert(name: string) {
  const node = el.value
  const token = `{{${name}}}`
  if (!node) {
    text.value += token
    return
  }
  const start = node.selectionStart ?? text.value.length
  const end = node.selectionEnd ?? start
  text.value = text.value.slice(0, start) + token + text.value.slice(end)
  const pos = start + token.length
  requestAnimationFrame(() => {
    node.focus()
    node.setSelectionRange(pos, pos)
  })
}
</script>

<template>
  <div class="stack" style="gap: 8px">
    <div class="row" style="justify-content: space-between">
      <label :for="id" style="font-weight: 600">{{ label }}</label>
      <span v-if="!streaming" class="row" style="gap: 4px">
        <button
          v-for="v in ALLOWED_VARIABLES"
          :key="v"
          type="button"
          class="btn quiet"
          style="padding-top: 2px; padding-bottom: 2px; font-size: 0.85rem"
          :aria-label="`Insert ${v} into ${label}`"
          @click="insert(v)"
        >
          + {{ v }}
        </button>
      </span>
    </div>

    <textarea
      v-if="multiline"
      :id="id"
      ref="el"
      v-model="text"
      :rows="rows ?? 4"
      :readonly="streaming"
      :class="{ 'streaming-field': streaming }"
      :aria-describedby="`${id}-counts ${id}-issues`"
      :aria-invalid="issues.some((i) => !i.code.startsWith('contains_')) || undefined"
    />
    <input
      v-else
      :id="id"
      ref="el"
      v-model="text"
      type="text"
      :readonly="streaming"
      :class="{ 'streaming-field': streaming }"
      :aria-describedby="`${id}-counts ${id}-issues`"
      :aria-invalid="issues.some((i) => !i.code.startsWith('contains_')) || undefined"
    />

    <div v-if="limit > 0" class="meter" :class="{ over }" aria-hidden="true">
      <span :style="{ width: pct + '%' }" />
    </div>

    <div :id="`${id}-counts`" class="counters num">
      <span><b>{{ runes }}</b> characters typed</span>
      <span v-if="worst !== null">
        <b :style="over ? 'color: var(--bad)' : ''">{{ worst }}</b> / {{ limit }} with longest name and city
      </span>
      <span v-else-if="limit > 0 && text" class="muted">checking length…</span>
      <span v-if="sms && smsInfo?.encoding">
        {{ smsInfo.encoding === 'ucs2' ? 'UCS-2 (Turkish letters or emoji)' : 'GSM-7' }},
        <b>{{ smsInfo.segments }}</b> {{ smsInfo.segments === 1 ? 'segment' : 'segments' }}
      </span>
      <span v-if="report && !('worst_case_runes' in report)"><b>{{ report.runes }}</b> / {{ report.limit }} allowed</span>
    </div>

    <ul :id="`${id}-issues`" class="issues" aria-live="polite">
      <li v-for="(i, k) in shown" :key="k">
        <span class="pill" :class="i.code.startsWith('contains_') ? 'warn' : 'bad'">
          {{ i.code.startsWith('contains_') ? 'Warning' : 'Error' }}
        </span>
        <span>{{ i.message }}</span>
      </li>
    </ul>
  </div>
</template>

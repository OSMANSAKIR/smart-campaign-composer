<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { formatInt } from '../lib/format'
import {
  PREVIEW_DEBOUNCE_MS,
  addCondition,
  addGroup,
  canAddCondition,
  canAddGroup,
  changeField,
  changeOp,
  cloneRule,
  debounce,
  fieldOf,
  issueLocation,
  labelOf,
  opLabel,
  opsFor,
  removeCondition,
  removeGroup,
  sameRule,
  valueKind,
} from '../lib/ruleEditor'
import type { Catalog, CatalogField, Issue, Language, RuleCondition, SegmentPreview, SegmentRule } from '../lib/types'

// Catalog-driven editor for the audience rule (F2). Groups are ANDed; the conditions inside a group are ORed.
const props = defineProps<{
  catalog: Catalog
  rule: SegmentRule
  language: Language
  campaignId: string
  saving: boolean
}>()
const emit = defineEmits<{ save: [SegmentRule]; cancel: [] }>()

const draft = ref<SegmentRule>(cloneRule(props.rule))
const preview = ref<SegmentPreview | null>(null)
const pending = ref(true)
const previewError = ref('')
let seq = 0
let abort: AbortController | undefined

async function runPreview(rule: SegmentRule) {
  const mine = ++seq
  abort?.abort()
  abort = new AbortController()
  try {
    const r = await api.previewSegment(rule, props.campaignId, abort.signal)
    if (mine !== seq) return
    preview.value = r
    previewError.value = ''
  } catch (e) {
    if (e instanceof Error && e.name === 'AbortError') return
    if (mine !== seq) return
    preview.value = null
    previewError.value = describeError(e).message
  } finally {
    if (mine === seq) pending.value = false
  }
}

const debounced = debounce(runPreview, PREVIEW_DEBOUNCE_MS)
// The first preview runs right away so the marketer sees the rendered rule and counts for what is loaded.
void runPreview(draft.value)
watch(
  draft,
  (r) => {
    pending.value = true
    debounced.call(cloneRule(r))
  },
  { deep: true },
)
onBeforeUnmount(() => {
  debounced.cancel()
  abort?.abort()
})

const limits = computed(() => props.catalog.limits)
const changed = computed(() => !sameRule(draft.value, props.rule))
const canSave = computed(() => !pending.value && !props.saving && changed.value && preview.value?.valid === true)

// Errors per condition (and per group / general) from the server's preview.
const issueMap = computed(() => {
  const byCondition = new Map<string, Issue[]>()
  const byGroup = new Map<number, Issue[]>()
  const general: Issue[] = []
  for (const i of preview.value?.errors ?? []) {
    const loc = issueLocation(i.path)
    if (!loc) general.push(i)
    else if (loc.condition === null) byGroup.set(loc.group, [...(byGroup.get(loc.group) ?? []), i])
    else {
      const k = `${loc.group}.${loc.condition}`
      byCondition.set(k, [...(byCondition.get(k) ?? []), i])
    }
  }
  return { byCondition, byGroup, general }
})
const conditionIssues = (g: number, c: number) => issueMap.value.byCondition.get(`${g}.${c}`) ?? []

const rendered = computed(() => {
  const r = preview.value?.rendered
  if (!r) return []
  const primary = r[props.language] ?? []
  return primary.length ? primary : r.en.length ? r.en : r.tr
})

const fieldFor = (c: RuleCondition): CatalogField | undefined => fieldOf(props.catalog, c.field)
const kindOf = (c: RuleCondition) => {
  const f = fieldFor(c)
  return f ? valueKind(f, c.op) : 'none'
}
const at = (g: number, c: number) => `Condition ${c + 1} of group ${g + 1}`

// --- value editing ---------------------------------------------------------------------------------
function num(raw: string): number | null {
  if (raw.trim() === '') return null // empty stays null so the server names the problem
  const n = Number(raw)
  return Number.isNaN(n) ? null : n
}
function setValue(g: number, c: number, v: RuleCondition['value']) {
  draft.value.groups[g]!.conditions[c]!.value = v
}
function setRange(g: number, c: number, side: 0 | 1, raw: string) {
  const cur = draft.value.groups[g]!.conditions[c]!.value
  const pair: (number | string | null)[] = Array.isArray(cur) ? [...cur] : [null, null]
  pair[side] = num(raw)
  setValue(g, c, pair)
}
const rangePart = (v: RuleCondition['value'], side: 0 | 1) => (Array.isArray(v) ? (v[side] ?? '') : '')
const listHas = (v: RuleCondition['value'], x: string) => Array.isArray(v) && v.includes(x)
function toggleListValue(g: number, c: number, x: string, on: boolean) {
  const cur = draft.value.groups[g]!.conditions[c]!.value
  const set = new Set<string>(Array.isArray(cur) ? cur.map(String) : [])
  if (on) set.add(x)
  else set.delete(x)
  const order = fieldFor(draft.value.groups[g]!.conditions[c]!)?.values ?? []
  setValue(g, c, order.filter((v) => set.has(v)))
}
function pickField(g: number, c: number, name: string) {
  draft.value.groups[g]!.conditions[c] = changeField(props.catalog, draft.value.groups[g]!.conditions[c]!, name)
}
function pickOp(g: number, c: number, op: string) {
  draft.value.groups[g]!.conditions[c] = changeOp(props.catalog, draft.value.groups[g]!.conditions[c]!, op)
}
</script>

<template>
  <section class="card stack rule-editor" aria-labelledby="rule-edit-h">
    <header>
      <h3 id="rule-edit-h">Edit the audience rule</h3>
    </header>
    <p class="muted">
      A person must match <b>every group</b>. Inside a group, matching <b>any one</b> condition is enough.
      Nothing is saved until you press Save rule.
    </p>

    <p v-if="draft.groups.length === 0" class="muted">
      No groups: the audience is everyone in the database (only people with consent receive messages).
    </p>

    <template v-for="(g, gi) in draft.groups" :key="gi">
      <p v-if="gi > 0" class="joiner" aria-hidden="true">AND</p>
      <fieldset class="group" :aria-label="`Group ${gi + 1}`">
        <div class="row" style="justify-content: space-between">
          <strong>Group {{ gi + 1 }}: match any of</strong>
          <button class="btn quiet" type="button" :aria-label="`Remove group ${gi + 1}`" @click="draft = removeGroup(draft, gi)">
            Remove group
          </button>
        </div>
        <ul v-if="issueMap.byGroup.get(gi)?.length" class="issues">
          <li v-for="(i, k) in issueMap.byGroup.get(gi)" :key="k"><span class="pill bad">Error</span><span>{{ i.message }}</span></li>
        </ul>

        <template v-for="(c, ci) in g.conditions" :key="ci">
          <p v-if="ci > 0" class="joiner or" aria-hidden="true">or</p>
          <div class="cond" :class="{ invalid: conditionIssues(gi, ci).length }">
            <div class="cond-row">
              <select
                :aria-label="`${at(gi, ci)}: field`"
                :value="c.field"
                @change="pickField(gi, ci, ($event.target as HTMLSelectElement).value)"
              >
                <option v-for="f in catalog.fields" :key="f.name" :value="f.name">{{ labelOf(f, f.name, language) }}</option>
              </select>

              <select
                :aria-label="`${at(gi, ci)}: operator`"
                :value="c.op"
                @change="pickOp(gi, ci, ($event.target as HTMLSelectElement).value)"
              >
                <option v-for="op in fieldFor(c) ? opsFor(fieldFor(c)!) : [c.op]" :key="op" :value="op">{{ opLabel(op) }}</option>
              </select>

              <template v-if="fieldFor(c)">
                <input
                  v-if="kindOf(c) === 'number'"
                  type="number"
                  :aria-label="`${at(gi, ci)}: value`"
                  :value="c.value ?? ''"
                  :min="fieldFor(c)!.min"
                  :max="fieldFor(c)!.max"
                  :step="fieldFor(c)!.integer ? 1 : 'any'"
                  @input="setValue(gi, ci, num(($event.target as HTMLInputElement).value))"
                />
                <span v-else-if="kindOf(c) === 'range'" class="range">
                  <input
                    type="number"
                    :aria-label="`${at(gi, ci)}: lower bound`"
                    :value="rangePart(c.value, 0)"
                    :min="fieldFor(c)!.min"
                    :max="fieldFor(c)!.max"
                    :step="fieldFor(c)!.integer ? 1 : 'any'"
                    @input="setRange(gi, ci, 0, ($event.target as HTMLInputElement).value)"
                  />
                  <span class="muted">and</span>
                  <input
                    type="number"
                    :aria-label="`${at(gi, ci)}: upper bound`"
                    :value="rangePart(c.value, 1)"
                    :min="fieldFor(c)!.min"
                    :max="fieldFor(c)!.max"
                    :step="fieldFor(c)!.integer ? 1 : 'any'"
                    @input="setRange(gi, ci, 1, ($event.target as HTMLInputElement).value)"
                  />
                </span>
                <select
                  v-else-if="kindOf(c) === 'enum'"
                  :aria-label="`${at(gi, ci)}: value`"
                  :value="c.value ?? ''"
                  @change="setValue(gi, ci, ($event.target as HTMLSelectElement).value)"
                >
                  <option v-for="v in fieldFor(c)!.values ?? []" :key="v" :value="v">{{ v }}</option>
                </select>
                <fieldset v-else-if="kindOf(c) === 'enum_list'" class="multi" :aria-label="`${at(gi, ci)}: values`">
                  <label v-for="v in fieldFor(c)!.values ?? []" :key="v" class="row" style="gap: 6px">
                    <input
                      type="checkbox"
                      :checked="listHas(c.value, v)"
                      @change="toggleListValue(gi, ci, v, ($event.target as HTMLInputElement).checked)"
                    />
                    <span>{{ v }}</span>
                  </label>
                </fieldset>
                <input
                  v-else-if="kindOf(c) === 'days'"
                  type="number"
                  :aria-label="`${at(gi, ci)}: number of days`"
                  :value="c.value ?? ''"
                  :min="limits.min_days"
                  :max="limits.max_days"
                  step="1"
                  @input="setValue(gi, ci, num(($event.target as HTMLInputElement).value))"
                />
                <input
                  v-else-if="kindOf(c) === 'date'"
                  type="date"
                  :aria-label="`${at(gi, ci)}: date`"
                  :value="typeof c.value === 'string' ? c.value : ''"
                  :min="limits.min_date?.slice(0, 10)"
                  :max="limits.max_date?.slice(0, 10)"
                  @input="setValue(gi, ci, ($event.target as HTMLInputElement).value || null)"
                />
              </template>

              <button
                class="btn quiet"
                type="button"
                :aria-label="`Remove ${at(gi, ci).toLowerCase()}`"
                @click="draft = removeCondition(draft, gi, ci)"
              >
                Remove
              </button>
            </div>
            <ul v-if="conditionIssues(gi, ci).length" class="issues">
              <li v-for="(i, k) in conditionIssues(gi, ci)" :key="k"><span class="pill bad">Error</span><span>{{ i.message }}</span></li>
            </ul>
          </div>
        </template>

        <div>
          <button
            class="btn"
            type="button"
            :disabled="!canAddCondition(draft, gi, limits)"
            :aria-label="`Add a condition to group ${gi + 1}`"
            @click="draft = addCondition(draft, gi, catalog)"
          >
            Add condition
          </button>
        </div>
      </fieldset>
    </template>

    <div class="row">
      <button class="btn" type="button" :disabled="!canAddGroup(draft, limits)" @click="draft = addGroup(draft, catalog)">
        Add group (AND)
      </button>
      <span class="muted num">
        {{ draft.groups.length }} / {{ limits.max_groups }} groups
      </span>
    </div>

    <ul v-if="issueMap.general.length" class="issues">
      <li v-for="(i, k) in issueMap.general" :key="k"><span class="pill bad">Error</span><span>{{ i.message }}</span></li>
    </ul>
    <p v-if="previewError" class="notice bad" role="alert">{{ previewError }}</p>
    <div v-for="(w, i) in preview?.warnings ?? []" :key="i" class="notice warn" role="status">
      <strong>{{ w.message }}</strong>
    </div>

    <div class="preview-box" aria-live="polite" aria-label="Preview of the edited rule">
      <h4 style="margin: 0">Preview</h4>
      <p v-if="pending" class="muted"><span class="spinner" aria-hidden="true" /> Checking the rule…</p>
      <template v-else-if="preview">
        <ul v-if="rendered.length" class="chips" aria-label="Edited rule in words">
          <li v-for="(r, i) in rendered" :key="i" class="chip">{{ r }}</li>
        </ul>
        <p v-if="preview.valid && preview.audience" class="num" data-testid="preview-count">
          <b>{{ formatInt(preview.audience.total) }}</b> people match ·
          <b>{{ formatInt(preview.audience.sms_reachable) }}</b> can get SMS ·
          <b>{{ formatInt(preview.audience.email_reachable) }}</b> can get email
        </p>
        <p v-else-if="!preview.valid" class="muted">Fix the errors above to see how many people match.</p>
      </template>
    </div>

    <div class="row">
      <button class="btn primary" type="button" :disabled="!canSave" @click="emit('save', cloneRule(draft))">
        <span v-if="saving" class="spinner" aria-hidden="true" />
        {{ saving ? 'Saving…' : 'Save rule' }}
      </button>
      <button class="btn" type="button" :disabled="saving" @click="emit('cancel')">Cancel</button>
    </div>
  </section>
</template>

<style scoped>
.group { border: 1px solid var(--line); border-radius: var(--radius); padding: 12px 14px; display: grid; gap: 10px; margin: 0; min-width: 0; }
.joiner { text-align: center; font-weight: 700; color: var(--accent); letter-spacing: 0.06em; font-size: 0.85rem; }
.joiner.or { text-align: left; color: var(--ink-3); text-transform: lowercase; letter-spacing: 0; }
.cond { display: grid; gap: 4px; }
.cond.invalid { border-left: 3px solid var(--bad); padding-left: 8px; }
.cond-row { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
.cond-row select, .cond-row input[type='number'], .cond-row input[type='date'] {
  font: inherit; padding: 8px 10px; border: 1px solid #b7bfce; border-radius: 6px; background: #fff; color: var(--ink); max-width: 100%;
}
.cond-row input[type='number'] { width: 9em; }
.range { display: inline-flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.multi { border: 1px solid var(--line); border-radius: 6px; padding: 6px 10px; display: flex; flex-wrap: wrap; gap: 4px 14px; margin: 0; min-width: 0; }
.preview-box { background: var(--paper); border-radius: var(--radius); padding: 12px 14px; display: grid; gap: 8px; }
</style>

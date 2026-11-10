<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { loadCatalog } from '../lib/catalog'
import { describeError } from '../lib/errors'
import { formatInt } from '../lib/format'
import { renderRule } from '../lib/ruleEditor'
import type { Campaign, Catalog, LlmMeta, SegmentRule } from '../lib/types'
import LlmPanel from './LlmPanel.vue'
import RuleEditor from './RuleEditor.vue'

const props = defineProps<{
  campaign: Campaign
  llm: LlmMeta | null
  busy: boolean
  // Saves a hand-edited rule; resolves true when the server accepted it (F2).
  saveRule: (rule: SegmentRule) => Promise<boolean>
  savingRule: boolean
}>()
defineEmits<{ regenerate: []; next: [] }>()

const showJson = ref(false)
const editing = ref(false)
const catalog = ref<Catalog | null>(null)
const catalogError = ref('')
const loadingCatalog = ref(false)

const seg = computed(() => props.campaign.segment)
const chips = computed(() => {
  const s = seg.value
  if (!s) return []
  // Prefer the campaign language; fall back to whichever rendering exists.
  const primary = s.rendered[s.language] ?? []
  return primary.length ? primary : (s.rendered.en.length ? s.rendered.en : s.rendered.tr)
})
const json = computed(() => JSON.stringify(seg.value?.rule ?? null, null, 2))
const nobody = computed(() => (seg.value?.audience.total ?? 0) === 0)

async function ensureCatalog() {
  if (catalog.value) return
  loadingCatalog.value = true
  catalogError.value = ''
  try {
    catalog.value = await loadCatalog()
  } catch (e) {
    catalogError.value = describeError(e).message
  } finally {
    loadingCatalog.value = false
  }
}
async function startEditing() {
  await ensureCatalog()
  if (catalog.value) editing.value = true
}
async function onSave(rule: SegmentRule) {
  if (await props.saveRule(rule)) editing.value = false
}

// Claude's original rule is rendered client-side from the catalog labels; without a catalog the JSON shows.
onMounted(() => {
  if (props.campaign.segment?.original_rule) void ensureCatalog()
})
const original = computed(() => seg.value?.original_rule ?? null)
const originalLines = computed(() =>
  original.value && catalog.value && seg.value ? renderRule(original.value, catalog.value, seg.value.language) : [],
)
const originalJson = computed(() => JSON.stringify(original.value, null, 2))
</script>

<template>
  <section v-if="seg" class="stack" aria-labelledby="aud-h">
    <div class="card stack">
      <header>
        <h2 id="aud-h">Who will receive it</h2>
        <span class="pill" :class="seg.source === 'edited' ? 'warn' : 'info'">
          {{ seg.source === 'edited' ? 'Edited' : 'Proposed by Claude' }}
        </span>
      </header>

      <ul v-if="chips.length" class="chips" aria-label="Audience rules">
        <li v-for="(c, i) in chips" :key="i" class="chip">{{ c }}</li>
      </ul>
      <p v-else class="muted">No conditions: this audience is everyone in the database.</p>
      <p v-if="seg.explanation" class="muted">{{ seg.explanation }}</p>

      <div class="row">
        <button v-if="!editing" type="button" class="btn" :disabled="loadingCatalog || busy" @click="startEditing">
          <span v-if="loadingCatalog" class="spinner" aria-hidden="true" />
          Edit rule
        </button>
        <span v-if="catalogError" class="muted" role="alert">{{ catalogError }}</span>
      </div>

      <details v-if="seg.source === 'edited' && original" class="original">
        <summary>Claude’s original rule</summary>
        <ul v-if="originalLines.length" class="chips" aria-label="Claude’s original rule in words">
          <li v-for="(l, i) in originalLines" :key="i" class="chip">{{ l }}</li>
        </ul>
        <pre v-else class="code" tabindex="0" aria-label="Claude’s original rule as JSON">{{ originalJson }}</pre>
        <p v-if="seg.edited_at" class="muted">Edited {{ new Date(seg.edited_at).toLocaleString('en-GB') }}.</p>
      </details>

      <div>
        <button
          type="button"
          class="btn quiet"
          :aria-expanded="showJson"
          aria-controls="rule-json"
          @click="showJson = !showJson"
        >
          {{ showJson ? 'Hide' : 'Show' }} raw rule (JSON)
        </button>
        <pre v-show="showJson" id="rule-json" class="code" tabindex="0" aria-label="Raw audience rule as JSON">{{ json }}</pre>
      </div>
    </div>

    <RuleEditor
      v-if="editing && catalog"
      :catalog="catalog"
      :rule="seg.rule"
      :language="seg.language"
      :campaign-id="campaign.id"
      :saving="savingRule"
      @save="onSave"
      @cancel="editing = false"
    />

    <div class="counts" role="group" aria-label="Audience size">
      <div class="count lead">
        <div class="v num">{{ formatInt(seg.audience.total) }}</div>
        <div class="l">people match</div>
      </div>
      <div class="count">
        <div class="v num">{{ formatInt(seg.audience.sms_reachable) }}</div>
        <div class="l">can get SMS (consented)</div>
      </div>
      <div class="count">
        <div class="v num">{{ formatInt(seg.audience.email_reachable) }}</div>
        <div class="l">can get email (consented)</div>
      </div>
      <div class="count">
        <div class="v num">{{ formatInt(seg.audience.both) }}</div>
        <div class="l">reachable on both</div>
      </div>
    </div>

    <div v-if="nobody" class="notice warn" role="status">
      <strong>Nobody matches this brief.</strong>
      <p>Loosen a condition in the brief and find the audience again.</p>
    </div>

    <div v-if="seg.unsupported_criteria.length" class="notice warn" role="status">
      <strong>Claude could not apply some of what you asked for</strong>
      <p>These parts are not in the audience rule, so they do not narrow the audience:</p>
      <ul>
        <li v-for="(u, i) in seg.unsupported_criteria" :key="i">{{ u }}</li>
      </ul>
    </div>
    <div v-if="seg.assumptions.length" class="notice info">
      <strong>Assumptions Claude made</strong>
      <ul>
        <li v-for="(a, i) in seg.assumptions" :key="i">{{ a }}</li>
      </ul>
    </div>
    <div v-for="(w, i) in seg.warnings" :key="i" class="notice warn" role="status">
      <strong>{{ w.message }}</strong>
    </div>

    <LlmPanel v-if="llm" :llm="llm" what="audience" />

    <div class="row">
      <button class="btn primary" type="button" :disabled="nobody || busy" @click="$emit('next')">
        {{ campaign.content ? 'Continue to content' : 'Write the content' }}
      </button>
      <button class="btn" type="button" :disabled="busy" @click="$emit('regenerate')">
        <span v-if="busy" class="spinner" aria-hidden="true" />
        {{ busy ? 'Working…' : 'Find audience again' }}
      </button>
      <span v-if="campaign.content && campaign.status === 'segmented'" class="muted">
        The audience changed, so the content needs a fresh check or a new draft.
      </span>
    </div>
  </section>
</template>

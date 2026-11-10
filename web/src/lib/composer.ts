// State and actions behind the four-step composer (brief → audience → content → approve).
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { api, newIdempotencyKey } from './api'
import { approveGate } from './approveGate'
import { describeError, ApiError, type UserMessage } from './errors'
import { extractVariants, type StreamedVariant } from './partialJson'
import { parseLocalInput, scheduleProblem as checkSchedule, toOffsetISO } from './schedule'
import { errorFromBody, streamGenerate, streamRefine } from './sse'
import type {
  ApproveResult,
  Campaign,
  Channel,
  ContentVersion,
  LlmMeta,
  SegmentRule,
  SendVariant,
  ValidationReport,
  VariantInput,
  VariantKey,
} from './types'
import { toInput, variantsOf } from './variants'

export type Step = 'brief' | 'audience' | 'content' | 'approve'
export const STEPS: { id: Step; label: string }[] = [
  { id: 'brief', label: 'Brief' },
  { id: 'audience', label: 'Audience' },
  { id: 'content', label: 'Content' },
  { id: 'approve', label: 'Approve' },
]

// Official example brief (KICKOFF §2) and a few more for the example chips.
export const OFFICIAL_BRIEF =
  'Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul’daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun.'
export const EXAMPLE_BRIEFS: { label: string; text: string }[] = [
  { label: 'Abandoned cart, Istanbul', text: OFFICIAL_BRIEF },
  {
    label: 'Win back lapsed buyers',
    text: 'Son 6 aydır alışveriş yapmayan, SMS izni olan müşterilere “Seni özledik” mesajı ve %15 indirim kodu gönder.',
  },
  {
    label: 'Email-only newsletter',
    text: 'Send a friendly product-news email to everyone in Ankara who opted in to email. Keep it short.',
  },
]

const variantKey = (c: VariantInput) => JSON.stringify([c.label, c.sms_text, c.email_subject, c.email_body])
const emptyForm = (label: string): VariantInput => ({ label, sms_text: '', email_subject: '', email_body: '' })
const defaultLabel = (k: VariantKey) => `Variant ${k.toUpperCase()}`
const KEYS: VariantKey[] = ['a', 'b']

interface VariantCheck {
  report: ValidationReport | null
  key: string // content key the report belongs to
  validating: boolean
}

export function useComposer(initialId: string | null) {
  const campaign = ref<Campaign | null>(null)
  const brief = ref(OFFICIAL_BRIEF)
  const step = ref<Step>('brief')
  const loading = ref(initialId !== null)
  const busy = ref<null | 'create' | 'segment' | 'save' | 'approve' | 'rule'>(null)
  const error = ref<UserMessage | null>(null)
  const segmentLlm = ref<LlmMeta | null>(null)
  const contentLlm = ref<LlmMeta | null>(null)
  const channels = ref<Channel[]>(['sms', 'email'])
  const approved = ref<ApproveResult | null>(null)

  // --- send options (approve dialog) -------------------------------------------------------------------
  const variant = ref<SendVariant>('a')
  const scheduleMode = ref<'now' | 'later'>('now')
  const scheduleAt = ref('') // datetime-local value, browser local time

  // --- content editor ---------------------------------------------------------------------------------
  // One form per variant; `hasB` says whether variant B exists in the editor.
  const forms = reactive<Record<VariantKey, VariantInput>>({ a: emptyForm('Variant A'), b: emptyForm('Variant B') })
  const hasB = ref(false)
  const stream = reactive({
    active: false,
    repairing: false,
    attempt: 1,
    buffer: '',
    started: false,
    // 'all' while generating two variants, a/b while refining one.
    target: 'all' as 'all' | VariantKey,
  })
  const checks = reactive<Record<VariantKey, VariantCheck>>({
    a: { report: null, key: '', validating: false },
    b: { report: null, key: '', validating: false },
  })
  const timers: Partial<Record<VariantKey, ReturnType<typeof setTimeout>>> = {}
  const seqs: Record<VariantKey, number> = { a: 0, b: 0 }
  const aborts: Partial<Record<VariantKey, AbortController>> = {}
  let streamAbort: AbortController | undefined

  const saved = computed(() => campaign.value?.content ?? null)
  const savedVariants = computed(() => (saved.value ? variantsOf(saved.value) : null))
  const activeKeys = computed<VariantKey[]>(() => (hasB.value ? KEYS : ['a']))
  const hasUnsavedEdits = computed(() => {
    const s = savedVariants.value
    if (!s) return forms.a.sms_text !== '' || forms.a.email_subject !== '' || forms.a.email_body !== ''
    return activeKeys.value.some((k) => {
      const sv = s[k]
      return !sv || variantKey(forms[k]) !== variantKey(toInput(sv))
    })
  })
  // A report is current when it was produced for exactly the text in that variant's editor.
  const reportIsCurrent = (k: VariantKey) => checks[k].report !== null && checks[k].key === variantKey(forms[k])
  const validating = computed(() => activeKeys.value.some((k) => checks[k].validating))

  const scheduleProblem = computed(() =>
    scheduleMode.value === 'later' ? checkSchedule(scheduleAt.value, new Date()) : '',
  )
  const gate = computed(() =>
    approveGate({
      campaign: campaign.value,
      channels: channels.value,
      hasUnsavedEdits: hasUnsavedEdits.value || stream.active,
      busy: busy.value !== null,
      variant: variant.value,
      scheduleProblem: scheduleProblem.value,
    }),
  )

  function fail(e: unknown) {
    error.value = describeError(e)
  }

  function adopt(c: Campaign) {
    campaign.value = c
    brief.value = c.brief
    const seg = c.segment?.channels_requested
    if (seg === 'sms') channels.value = ['sms']
    else if (seg === 'email') channels.value = ['email']
  }

  function setFormsFrom(ct: ContentVersion | null) {
    const vs = ct ? variantsOf(ct) : null
    for (const k of KEYS) {
      const v = vs?.[k]
      forms[k].label = v?.label ?? defaultLabel(k)
      forms[k].sms_text = v?.sms_text ?? ''
      forms[k].email_subject = v?.email_subject ?? ''
      forms[k].email_body = v?.email_body ?? ''
      checks[k].report = v?.validation ?? null
      checks[k].key = variantKey(forms[k])
      checks[k].validating = false
    }
    hasB.value = !!vs?.b
    if (!hasB.value && variant.value !== 'a') variant.value = 'a'
  }

  function loadFormFromSaved() {
    setFormsFrom(saved.value)
  }

  async function load(id: string) {
    loading.value = true
    error.value = null
    try {
      const c = await api.getCampaign(id)
      adopt(c)
      loadFormFromSaved()
      step.value = c.content ? 'content' : c.segment ? 'audience' : 'brief'
    } catch (e) {
      fail(e)
    } finally {
      loading.value = false
    }
  }

  async function refresh() {
    if (!campaign.value) return
    const c = await api.getCampaign(campaign.value.id)
    campaign.value = c
  }

  // Step 1 → 2: create the draft (once), then ask Claude for the audience rule.
  async function findAudience() {
    error.value = null
    try {
      if (!campaign.value) {
        busy.value = 'create'
        adopt(await api.createCampaign(brief.value.trim()))
      }
      await generateSegment()
    } catch (e) {
      fail(e)
    } finally {
      busy.value = null
    }
    if (campaign.value?.segment) step.value = 'audience'
  }

  async function generateSegment() {
    if (!campaign.value) return
    busy.value = 'segment'
    error.value = null
    try {
      const r = await api.generateSegment(campaign.value.id)
      adopt(r.campaign)
      segmentLlm.value = r.llm
      if (step.value === 'brief') step.value = 'audience'
    } catch (e) {
      fail(e)
    } finally {
      busy.value = null
    }
  }

  // F2: save a hand-edited rule, then take the campaign the server returns (new counts, status `segmented`).
  // Returns true when saved; the editor shows the error itself for a 422.
  async function saveRule(rule: SegmentRule): Promise<boolean> {
    const c = campaign.value
    if (!c) return false
    busy.value = 'rule'
    error.value = null
    try {
      const r = await api.saveSegment(c.id, rule)
      adopt(r.campaign)
      return true
    } catch (e) {
      fail(e)
      return false
    } finally {
      busy.value = null
    }
  }

  // --- streaming generation and refine ----------------------------------------------------------------
  function applyStreamed(k: VariantKey, x: StreamedVariant | null) {
    if (!x) return
    if (x.label) forms[k].label = x.label
    forms[k].sms_text = x.smsText
    forms[k].email_subject = x.emailSubject
    forms[k].email_body = x.emailBody
  }

  async function runStream(
    target: 'all' | VariantKey,
    start: (onEvent: Parameters<typeof streamGenerate>[1], signal: AbortSignal) => Promise<void>,
  ) {
    if (!campaign.value || stream.active) return
    error.value = null
    streamAbort = new AbortController()
    stream.active = true
    stream.repairing = false
    stream.attempt = 1
    stream.buffer = ''
    stream.started = true
    stream.target = target
    cancelValidation()
    const clearTarget = () => {
      for (const k of target === 'all' ? KEYS : [target]) {
        forms[k].sms_text = ''
        forms[k].email_subject = ''
        forms[k].email_body = ''
        if (target === 'all') forms[k].label = defaultLabel(k)
      }
    }
    if (target === 'all') {
      hasB.value = true // both cards show while they fill
      clearTarget()
    }
    let finished = false
    try {
      await start((ev) => {
        switch (ev.type) {
          case 'delta': {
            stream.repairing = false
            stream.buffer += ev.data.text
            // Display only: the canonical text arrives with `result`.
            const x = extractVariants(stream.buffer)
            if (target === 'all') {
              applyStreamed('a', x.a)
              applyStreamed('b', x.b)
            } else applyStreamed(target, x.a)
            break
          }
          case 'repairing':
            // The previous attempt was invalid: drop its partial text; a new attempt streams next.
            stream.repairing = true
            stream.attempt = ev.data.attempt
            stream.buffer = ''
            if (target === 'all') clearTarget()
            break
          case 'result': {
            finished = true
            contentLlm.value = ev.data.llm
            const ct = ev.data.content
            setFormsFrom(ct)
            if (campaign.value) campaign.value = { ...campaign.value, content: ct }
            break
          }
          case 'error':
            finished = true
            fail(errorFromBody(ev.data))
            break
          default:
            break
        }
      }, streamAbort.signal)
      if (!finished) {
        fail(new ApiError(0, 'network', 'The stream ended before a result arrived'))
      } else if (!error.value) {
        await refresh() // status moves to content_ready on the server
      }
    } catch (e) {
      if (!(e instanceof Error && e.name === 'AbortError')) fail(e)
    } finally {
      stream.active = false
      stream.repairing = false
      // A failed attempt leaves partial text on screen; fall back to the last saved version instead.
      if (error.value || !finished) loadFormFromSaved()
    }
  }

  function generateContent() {
    const c = campaign.value
    if (!c) return Promise.resolve()
    return runStream('all', (onEvent, signal) => streamGenerate(c.id, onEvent, signal))
  }

  // F3: rewrite one variant with an instruction. Uses the saved version (unsaved edits must be saved first).
  function refineVariant(k: VariantKey, instruction: string) {
    const c = campaign.value
    const v = saved.value?.version
    if (!c || v === undefined) return Promise.resolve()
    return runStream(k, (onEvent, signal) =>
      streamRefine(c.id, { variant: k, instruction: instruction.trim(), base_version: v }, onEvent, signal),
    )
  }

  function stopGenerating() {
    streamAbort?.abort()
  }

  // --- validation and saving --------------------------------------------------------------------------
  function cancelValidation() {
    for (const k of KEYS) {
      clearTimeout(timers[k])
      aborts[k]?.abort()
      checks[k].validating = false
    }
  }

  async function validateNow(k: VariantKey) {
    const c = campaign.value
    const seq = ++seqs[k]
    const key = variantKey(forms[k])
    const body = { sms_text: forms[k].sms_text, email_subject: forms[k].email_subject, email_body: forms[k].email_body }
    aborts[k]?.abort()
    const ctl = new AbortController()
    aborts[k] = ctl
    checks[k].validating = true
    try {
      const r = await api.validateContent({ ...body, campaign_id: c?.id, language: c?.language }, ctl.signal)
      if (seq !== seqs[k]) return // a newer edit superseded this response
      checks[k].report = r
      checks[k].key = key
    } catch (e) {
      if (e instanceof Error && e.name === 'AbortError') return
      if (seq === seqs[k]) fail(e)
    } finally {
      if (seq === seqs[k]) checks[k].validating = false
    }
  }

  const DEBOUNCE_MS = 400
  for (const k of KEYS) {
    watch(
      () => variantKey(forms[k]),
      (key) => {
        if (stream.active || !campaign.value) return
        if (k === 'b' && !hasB.value) return
        if (key === checks[k].key) return
        clearTimeout(timers[k])
        checks[k].validating = true
        timers[k] = setTimeout(() => void validateNow(k), DEBOUNCE_MS)
      },
    )
  }

  async function saveContent() {
    const c = campaign.value
    if (!c) return
    cancelValidation()
    busy.value = 'save'
    error.value = null
    try {
      const r = await api.saveContent(
        c.id,
        { variants: { a: { ...forms.a }, b: hasB.value ? { ...forms.b } : null } },
        c.content?.version ?? 0,
      )
      campaign.value = { ...c, content: r.content }
      setFormsFrom(r.content)
      await refresh()
    } catch (e) {
      fail(e)
    } finally {
      busy.value = null
    }
  }

  // Reloads the latest server state and discards local edits (used after a 409).
  async function reloadFromServer() {
    if (!campaign.value) return
    await load(campaign.value.id)
  }

  // --- approve ----------------------------------------------------------------------------------------
  const recipients = computed(() => {
    const a = campaign.value?.segment?.audience
    return {
      sms: channels.value.includes('sms') ? (a?.sms_reachable ?? 0) : 0,
      email: channels.value.includes('email') ? (a?.email_reachable ?? 0) : 0,
    }
  })

  // Returns true when the campaign was accepted. A fresh Idempotency-Key is generated for every attempt.
  async function approve(): Promise<boolean> {
    const c = campaign.value
    const v = saved.value?.version
    if (!c || v === undefined || !gate.value.enabled) return false
    let sendAt: string | null = null
    if (scheduleMode.value === 'later') {
      const d = parseLocalInput(scheduleAt.value)
      if (!d || checkSchedule(scheduleAt.value, new Date())) return false // window moved while the dialog was open
      sendAt = toOffsetISO(d)
    }
    busy.value = 'approve'
    error.value = null
    try {
      approved.value = await api.approve(c.id, [...channels.value], v, newIdempotencyKey(), {
        variant: variant.value,
        sendAt,
      })
      campaign.value = approved.value.campaign
      return true
    } catch (e) {
      fail(e)
      return false
    } finally {
      busy.value = null
    }
  }

  function toggleChannel(ch: Channel, on: boolean) {
    const set = new Set(channels.value)
    if (on) set.add(ch)
    else set.delete(ch)
    channels.value = (['sms', 'email'] as const).filter((x) => set.has(x))
  }

  function goTo(s: Step) {
    error.value = null
    step.value = s
  }

  onBeforeUnmount(() => {
    cancelValidation()
    streamAbort?.abort()
  })

  if (initialId) void load(initialId)

  return {
    campaign, brief, step, loading, busy, error, segmentLlm, contentLlm, channels, approved,
    forms, hasB, checks, stream, reportIsCurrent, validating, hasUnsavedEdits, gate, recipients,
    variant, scheduleMode, scheduleAt, scheduleProblem,
    findAudience, generateSegment, saveRule, generateContent, refineVariant, stopGenerating, saveContent,
    reloadFromServer, approve, toggleChannel, goTo, load,
  }
}

// State and actions behind the four-step composer (brief → audience → content → approve).
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { api, newIdempotencyKey } from './api'
import { approveGate } from './approveGate'
import { describeError, ApiError, type UserMessage } from './errors'
import { errorFromBody, streamGenerate } from './sse'
import { extractContent } from './partialJson'
import type { ApproveResult, Campaign, Channel, ContentInput, LlmMeta, ValidationReport } from './types'

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

const contentKey = (c: ContentInput) => JSON.stringify([c.sms_text, c.email_subject, c.email_body])

export function useComposer(initialId: string | null) {
  const campaign = ref<Campaign | null>(null)
  const brief = ref(OFFICIAL_BRIEF)
  const step = ref<Step>('brief')
  const loading = ref(initialId !== null)
  const busy = ref<null | 'create' | 'segment' | 'save' | 'approve'>(null)
  const error = ref<UserMessage | null>(null)
  const segmentLlm = ref<LlmMeta | null>(null)
  const contentLlm = ref<LlmMeta | null>(null)
  const channels = ref<Channel[]>(['sms', 'email'])
  const approved = ref<ApproveResult | null>(null)

  // --- content editor ---------------------------------------------------------------------------------
  const form = reactive<ContentInput>({ sms_text: '', email_subject: '', email_body: '' })
  const stream = reactive({ active: false, repairing: false, attempt: 1, buffer: '', started: false })
  const report = ref<ValidationReport | null>(null)
  const reportKey = ref('')
  const validating = ref(false)
  let validateTimer: ReturnType<typeof setTimeout> | undefined
  let validateSeq = 0
  let validateAbort: AbortController | undefined
  let streamAbort: AbortController | undefined

  const saved = computed(() => campaign.value?.content ?? null)
  const hasUnsavedEdits = computed(() => {
    const s = saved.value
    if (!s) return form.sms_text !== '' || form.email_subject !== '' || form.email_body !== ''
    return contentKey(form) !== contentKey(s)
  })
  // The report is current when it was produced for exactly the text in the editor.
  const reportIsCurrent = computed(() => report.value !== null && reportKey.value === contentKey(form))

  const gate = computed(() =>
    approveGate({
      campaign: campaign.value,
      channels: channels.value,
      hasUnsavedEdits: hasUnsavedEdits.value || stream.active,
      busy: busy.value !== null,
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

  function loadFormFromSaved() {
    const s = saved.value
    form.sms_text = s?.sms_text ?? ''
    form.email_subject = s?.email_subject ?? ''
    form.email_body = s?.email_body ?? ''
    report.value = s?.validation ?? null
    reportKey.value = contentKey(form)
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

  // --- streaming generation ---------------------------------------------------------------------------
  async function generateContent() {
    const c = campaign.value
    if (!c || stream.active) return
    error.value = null
    streamAbort = new AbortController()
    stream.active = true
    stream.repairing = false
    stream.attempt = 1
    stream.buffer = ''
    stream.started = true
    cancelValidation()
    let finished = false
    try {
      await streamGenerate(
        c.id,
        (ev) => {
          switch (ev.type) {
            case 'delta': {
              stream.repairing = false
              stream.buffer += ev.data.text
              // Display only: the canonical text arrives with `result`.
              const x = extractContent(stream.buffer)
              form.sms_text = x.smsText
              form.email_subject = x.emailSubject
              form.email_body = x.emailBody
              break
            }
            case 'repairing':
              // The previous attempt was invalid: drop its partial text; a new attempt streams next.
              stream.repairing = true
              stream.attempt = ev.data.attempt
              stream.buffer = ''
              form.sms_text = ''
              form.email_subject = ''
              form.email_body = ''
              break
            case 'result': {
              finished = true
              contentLlm.value = ev.data.llm
              const ct = ev.data.content
              form.sms_text = ct.sms_text
              form.email_subject = ct.email_subject
              form.email_body = ct.email_body
              report.value = ct.validation
              reportKey.value = contentKey(ct)
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
        },
        streamAbort.signal,
      )
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
      if (error.value) loadFormFromSaved()
    }
  }

  function stopGenerating() {
    streamAbort?.abort()
  }

  // --- validation and saving --------------------------------------------------------------------------
  function cancelValidation() {
    clearTimeout(validateTimer)
    validateAbort?.abort()
    validating.value = false
  }

  async function validateNow() {
    const c = campaign.value
    const seq = ++validateSeq
    const key = contentKey(form)
    const body = { ...form }
    validateAbort?.abort()
    validateAbort = new AbortController()
    validating.value = true
    try {
      const r = await api.validateContent(
        { ...body, campaign_id: c?.id, language: c?.language },
        validateAbort.signal,
      )
      if (seq !== validateSeq) return // a newer edit superseded this response
      report.value = r
      reportKey.value = key
    } catch (e) {
      if (e instanceof Error && e.name === 'AbortError') return
      if (seq === validateSeq) fail(e)
    } finally {
      if (seq === validateSeq) validating.value = false
    }
  }

  const DEBOUNCE_MS = 400
  watch(
    () => contentKey(form),
    (key) => {
      if (stream.active || !campaign.value) return
      if (key === reportKey.value) return
      clearTimeout(validateTimer)
      validating.value = true
      validateTimer = setTimeout(() => void validateNow(), DEBOUNCE_MS)
    },
  )

  async function saveContent() {
    const c = campaign.value
    if (!c) return
    cancelValidation()
    busy.value = 'save'
    error.value = null
    try {
      const r = await api.saveContent(c.id, { ...form }, c.content?.version ?? 0)
      campaign.value = { ...c, content: r.content }
      report.value = r.content.validation
      reportKey.value = contentKey(r.content)
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
    busy.value = 'approve'
    error.value = null
    try {
      approved.value = await api.approve(c.id, [...channels.value], v, newIdempotencyKey())
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
    form, stream, report, reportIsCurrent, validating, hasUnsavedEdits, gate, recipients,
    findAudience, generateSegment, generateContent, stopGenerating, saveContent, reloadFromServer,
    approve, toggleChannel, goTo, load,
  }
}

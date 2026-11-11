// @vitest-environment jsdom
// B-3: content in the recipient's language: the pure helpers, the approve gate and the composer's localize,
// save and approve flows against a stubbed fetch.
import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { api } from './api'
import { approveGate } from './approveGate'
import { useComposer } from './composer'
import { audienceLocaleLines, isStale, localeRows, localizationProblem } from './localization'
import type { Campaign, ChannelProgress, ContentVariant, ContentVersion, LocalizedCopy, ValidationReport } from './types'

const rep = (smsOk = true, emailOk = true): ValidationReport => ({
  valid: smsOk && emailOk,
  valid_for: { sms: smsOk, email: emailOk },
  fields: {
    sms_text: { runes: 5, worst_case_runes: 9, limit: 160 },
    email_subject: { runes: 5, worst_case_runes: 9, limit: 80 },
    email_body: { runes: 50, limit: 5000 },
  },
  worst_case: { first_name: 'Abdurrahman', city: 'Eskişehir' },
  variables: [],
  errors: [],
  warnings: [],
})
const loc = (label: string, over: Partial<LocalizedCopy> = {}): LocalizedCopy => ({
  label, source: 'llm', sms_text: `${label} sms`, email_subject: `${label} subj`, email_body: `${label} body`, validation: rep(), stale: false, ...over,
})
const row = (label: string, localized?: Partial<Record<'en' | 'tr', LocalizedCopy>>): ContentVariant => ({
  label, source: 'llm', instruction: null, sms_text: `${label} sms`, email_subject: `${label} subj`, email_body: `${label} body`,
  validation: rep(), locale: 'tr', localized,
})
const version = (n: number, a: ContentVariant, b: ContentVariant | null): ContentVersion => ({
  version: n, source: 'llm', sms_text: a.sms_text, email_subject: a.email_subject, email_body: a.email_body, validation: a.validation,
  created_at: '', variants: { a, b },
})
// 10 en and 20 tr people; en: 9 sms, 10 email.
function campaign(content: ContentVersion | null, status: Campaign['status'] = 'content_ready'): Campaign {
  return {
    id: 'c1', brief: 'b', status, language: 'tr',
    segment: {
      version: 1, source: 'llm', rule: { groups: [] }, rendered: { en: [], tr: [] }, unsupported_criteria: [], assumptions: [],
      explanation: '', channels_requested: 'both', language: 'tr', warnings: [],
      audience: {
        total: 30, sms_reachable: 27, email_reachable: 30, both: 27, counted_at: '',
        by_locale: { tr: { total: 20, sms_reachable: 18, email_reachable: 20, both: 18 }, en: { total: 10, sms_reachable: 9, email_reachable: 10, both: 9 } },
      },
    },
    content, channels: [], approved_content_version: null, created_at: '', approved_at: null, completed_at: null,
  }
}
const good = () => version(2, row('Friendly', { en: loc('Friendly EN') }), row('Urgent', { en: loc('Urgent EN') }))

describe('localization helpers (B-3)', () => {
  it('lists the audience by language, Turkish first, and nothing without a breakdown', () => {
    const lines = audienceLocaleLines(campaign(null).segment!.audience)
    expect(lines.map((l) => [l.name, l.total, l.sms, l.email])).toEqual([['Turkish', 20, 18, 20], ['English', 10, 9, 10]])
    expect(audienceLocaleLines({ total: 1, sms_reachable: 1, email_reachable: 1, both: 1, counted_at: '' })).toEqual([])
  })

  it('knows a stale adaptation from the server flag or from unsaved primary edits, not from the label', () => {
    const primary = row('Friendly')
    const form = { sms_text: primary.sms_text, email_subject: primary.email_subject, email_body: primary.email_body }
    expect(isStale(null, primary, form)).toBe(false)
    expect(isStale(loc('x'), primary, form)).toBe(false)
    expect(isStale(loc('x', { stale: true }), primary, form)).toBe(true)
    expect(isStale(loc('x'), primary, { ...form, sms_text: 'edited' })).toBe(true)
    expect(isStale(loc('x'), primary, { ...form })).toBe(false)
  })

  it('mirrors the server gate: missing, stale and invalid adaptations, per sent variant and channel', () => {
    const c = campaign(good())
    expect(localizationProblem(c.content, c, ['sms', 'email'], 'ab')).toBe('')
    const missing = campaign(version(2, row('A'), row('B')))
    expect(localizationProblem(missing.content, missing, ['sms'], 'a')).toMatch(/Variant A has no English version/)
    const stale = campaign(version(2, row('A', { en: loc('A', { stale: true }) }), row('B', { en: loc('B') })))
    expect(localizationProblem(stale.content, stale, ['sms'], 'a')).toMatch(/out of date/)
    expect(localizationProblem(stale.content, stale, ['sms'], 'b')).toBe('') // b is current; a is not sent
    const smsBad = campaign(version(2, row('A', { en: loc('A', { validation: rep(false, true) }) }), row('B', { en: loc('B') })))
    expect(localizationProblem(smsBad.content, smsBad, ['sms'], 'a')).toMatch(/not valid for SMS/)
    expect(localizationProblem(smsBad.content, smsBad, ['email'], 'a')).toBe('') // valid for email
  })

  it('needs no adaptation when nobody reads the other language on the chosen channels', () => {
    const c = campaign(version(2, row('A'), row('B')))
    c.segment!.audience.by_locale!.en = { total: 3, sms_reachable: 0, email_reachable: 3, both: 0 }
    expect(localizationProblem(c.content, c, ['sms'], 'a')).toBe('')
    expect(localizationProblem(c.content, c, ['email'], 'a')).toMatch(/no English version/)
    const alone = campaign(version(2, row('A'), row('B')))
    alone.segment!.audience.by_locale = { tr: alone.segment!.audience.by_locale!.tr!, en: { total: 0, sms_reachable: 0, email_reachable: 0, both: 0 } }
    expect(localizationProblem(alone.content, alone, ['sms', 'email'], 'ab')).toBe('')
  })

  it('the approve gate refuses recipient mode until it is valid, and leaves campaign mode alone', () => {
    const missing = campaign(version(2, row('A'), row('B')))
    const base = { channels: ['sms', 'email'] as ('sms' | 'email')[], hasUnsavedEdits: false, variant: 'ab' as const }
    expect(approveGate({ ...base, campaign: missing, languageMode: 'campaign' }).enabled).toBe(true)
    const g = approveGate({ ...base, campaign: missing, languageMode: 'recipient' })
    expect(g.enabled).toBe(false)
    expect(g.reason).toMatch(/English version/)
    expect(approveGate({ ...base, campaign: campaign(good()), languageMode: 'recipient' }).enabled).toBe(true)
  })

  it('reads the per-language delivery rows in Turkish, English order, only for languages with messages', () => {
    const c = { total: 3, queued: 0, sending: 0, sent: 2, retry: 0, failed: 1, skipped_no_consent: 0, throughput_per_s: 0, paused_until: null,
      locales: { en: { total: 1, sent: 1, failed: 0, cancelled: 0, skipped_no_consent: 0 }, tr: { total: 2, sent: 1, failed: 1, cancelled: 0, skipped_no_consent: 0 } } } as ChannelProgress
    expect(localeRows(c).map((r) => [r.name, r.v.total, r.v.sent, r.v.failed])).toEqual([['Turkish', 2, 1, 1], ['English', 1, 1, 0]])
    expect(localeRows({ ...c, locales: undefined })).toEqual([])
  })
})

afterEach(() => vi.unstubAllGlobals())
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
const sse = (events: [string, unknown][]) =>
  new Response(events.map(([e, d]) => `event: ${e}\ndata: ${JSON.stringify(d)}\n\n`).join(''), { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
const llm = { provider: 'mock', model: 'm', prompt_version: 'localize_v1', latency_ms: 1, attempts: 1, repaired: false, input_tokens: 1, output_tokens: 1, cache_read_tokens: 0, cache_write_tokens: 0, cost_usd: 0, cost_recorded: true }

type Call = { url: string; method: string; body: unknown }
let calls: Call[]
function stub(routes: (url: string, method: string, body: unknown) => Response | undefined) {
  calls = []
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init: RequestInit) => {
    const body = init.body ? JSON.parse(init.body as string) : undefined
    calls.push({ url, method: init.method ?? 'GET', body })
    return Promise.resolve(routes(url, init.method ?? 'GET', body) ?? json({ error: { code: 'internal', message: `unstubbed ${url}` } }, 500))
  }))
}
function setup() {
  let c!: ReturnType<typeof useComposer>
  mount(defineComponent({ setup() { c = useComposer('c1'); return () => null } }))
  return c
}
const settle = async () => { for (let i = 0; i < 6; i++) await nextTick(); await new Promise((r) => setTimeout(r, 0)) }

describe('composer: localize, save and approve (B-3)', () => {
  const V1 = version(1, row('Friendly'), row('Urgent'))

  it('streams the adaptation into the other-language forms and takes the canonical result', async () => {
    const V2 = good()
    stub((url, method) => {
      if (url === '/api/campaigns/c1' && method === 'GET') return json(campaign(calls.some((x) => x.url.endsWith('/localize')) ? V2 : V1))
      if (url === '/api/campaigns/c1/content/localize') {
        const partial = JSON.stringify({ language: 'en', variants: { a: { label: 'Friendly EN', sms: { text: 'Friendly EN sms' } } } })
        return sse([['meta', { provider: 'mock', model: 'm', prompt_version: 'localize_v1' }], ['delta', { text: partial }], ['result', { content: V2, llm }]])
      }
      return undefined
    })
    const c = setup()
    await settle()
    expect(c.otherRecipients.value).toBe(10)
    expect(c.other.value).toBe('en')
    expect(c.hasLocalized.value).toBe(false)
    await c.localizeContent()
    const call = calls.find((x) => x.url.endsWith('/localize'))!
    expect(call.method).toBe('POST')
    expect(call.body).toEqual({ target_locale: 'en' })
    expect(c.localForms.a.sms_text).toBe('Friendly EN sms')
    expect(c.localForms.b.label).toBe('Urgent EN')
    expect(c.forms.a.sms_text).toBe('Friendly sms') // the primary copy is untouched
    expect(c.hasLocalized.value).toBe(true)
    expect(c.hasUnsavedEdits.value).toBe(false)
    expect(c.campaign.value?.content?.version).toBe(2)
  })

  it('shows a localize error in words and restores the saved text', async () => {
    stub((url) => {
      if (url === '/api/campaigns/c1') return json(campaign(V1))
      if (url.endsWith('/localize')) return sse([['meta', {}], ['error', { error: { code: 'llm_invalid_output', message: 'bad', details: [] } }]])
      return undefined
    })
    const c = setup()
    await settle()
    await c.localizeContent()
    expect(c.error.value).not.toBeNull()
    expect(c.stream.active).toBe(false)
    expect(c.localForms.a.sms_text).toBe('')
  })

  it('marks the adaptation stale as soon as the primary text is edited, and sends it with the save', async () => {
    const V3 = version(3, row('Friendly', { en: loc('Friendly EN', { stale: true }) }), row('Urgent', { en: loc('Urgent EN') }))
    stub((url, method) => {
      if (url === '/api/campaigns/c1' && method === 'GET') return json(campaign(calls.some((x) => x.method === 'PUT') ? V3 : good()))
      if (url === '/api/campaigns/c1/content' && method === 'PUT') return json({ content: V3 })
      return undefined
    })
    const c = setup()
    await settle()
    expect(c.localStale('a')).toBe(false)
    c.forms.a.sms_text = 'Friendly edited'
    await nextTick()
    expect(c.localStale('a')).toBe(true)
    expect(c.localStale('b')).toBe(false)
    await c.saveContent()
    const put = calls.find((x) => x.method === 'PUT')!
    expect(put.body).toEqual({
      base_version: 2,
      variants: {
        a: { label: 'Friendly', sms_text: 'Friendly edited', email_subject: 'Friendly subj', email_body: 'Friendly body',
          localized: { en: { label: 'Friendly EN', sms_text: 'Friendly EN sms', email_subject: 'Friendly EN subj', email_body: 'Friendly EN body' } } },
        b: { label: 'Urgent', sms_text: 'Urgent sms', email_subject: 'Urgent subj', email_body: 'Urgent body',
          localized: { en: { label: 'Urgent EN', sms_text: 'Urgent EN sms', email_subject: 'Urgent EN subj', email_body: 'Urgent EN body' } } },
      },
    })
    expect(c.localStale('a')).toBe(true) // the server now says so
  })

  it('an edit of the adaptation alone is an unsaved edit', async () => {
    stub((url) => (url === '/api/campaigns/c1' ? json(campaign(good())) : undefined))
    const c = setup()
    await settle()
    expect(c.hasUnsavedEdits.value).toBe(false)
    c.localForms.b.sms_text = 'changed by hand'
    await nextTick()
    expect(c.hasUnsavedEdits.value).toBe(true)
  })

  it('defaults to each recipient in their language when valid, falls back when not, and approves with it', async () => {
    stub((url, method) => {
      if (url === '/api/campaigns/c1' && method === 'GET') return json(campaign(good()))
      if (url === '/api/campaigns/c1/approve') return json({ campaign: campaign(good(), 'sending'), enqueued: { sms: 27, email: 30 } }, 202)
      return undefined
    })
    const c = setup()
    await settle()
    expect(c.offersRecipientMode.value).toBe(true)
    expect(c.recipientModeProblem.value).toBe('')
    expect(c.languageMode.value).toBe('recipient')
    expect(c.gate.value.enabled).toBe(true)
    // The marketer's explicit choice sticks.
    c.setLanguageMode('campaign')
    await nextTick()
    expect(c.languageMode.value).toBe('campaign')
    c.setLanguageMode('recipient')
    await nextTick()
    expect(await c.approve()).toBe(true)
    expect(calls.find((x) => x.url.endsWith('/approve'))!.body).toMatchObject({ language_mode: 'recipient', content_version: 2 })
  })

  it('stays on everyone-in-the-campaign-language when an adaptation is missing', async () => {
    stub((url) => (url === '/api/campaigns/c1' ? json(campaign(V1)) : undefined))
    const c = setup()
    await settle()
    expect(c.offersRecipientMode.value).toBe(true)
    expect(c.recipientModeProblem.value).toMatch(/no English version/)
    expect(c.languageMode.value).toBe('campaign')
    c.setLanguageMode('recipient') // a stale choice must not stay selected while it cannot be approved
    await nextTick()
    expect(c.languageMode.value).toBe('campaign')
  })

  it('does not offer the choice when nobody reads the other language', async () => {
    const solo = campaign(V1)
    solo.segment!.audience.by_locale!.en = { total: 0, sms_reachable: 0, email_reachable: 0, both: 0 }
    stub((url) => (url === '/api/campaigns/c1' ? json(solo) : undefined))
    const c = setup()
    await settle()
    expect(c.offersRecipientMode.value).toBe(false)
    expect(c.otherRecipients.value).toBe(0)
    expect(c.languageMode.value).toBe('campaign')
  })
})

describe('api: approve body (B-3)', () => {
  it('sends language_mode only for recipient mode, so every earlier body is unchanged', async () => {
    stub(() => json({ campaign: campaign(good(), 'sending'), enqueued: { sms: 1, email: 1 } }, 202))
    await api.approve('c1', ['sms'], 2, 'k1', { variant: 'ab', languageMode: 'recipient' })
    await api.approve('c1', ['sms'], 2, 'k2', { variant: 'a', languageMode: 'campaign' })
    expect(calls[0]!.body).toEqual({ channels: ['sms'], content_version: 2, variant: 'ab', language_mode: 'recipient' })
    expect(calls[1]!.body).toEqual({ channels: ['sms'], content_version: 2, variant: 'a' })
  })
})

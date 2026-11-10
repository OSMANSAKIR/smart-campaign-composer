// @vitest-environment jsdom
// The composer's content flow against a stubbed fetch: two streamed variants (B-2), saving both, refining one
// (F3), approving with a variant and a schedule (F5).
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { useComposer } from './composer'
import { toLocalInputValue } from './schedule'
import type { Campaign, ContentVariant, ContentVersion, ValidationReport } from './types'

const rep = (valid = true): ValidationReport => ({
  valid,
  valid_for: { sms: valid, email: valid },
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

const row = (label: string, sms: string, over: Partial<ContentVariant> = {}): ContentVariant => ({
  label,
  source: 'llm',
  instruction: null,
  sms_text: sms,
  email_subject: `${label} subject`,
  email_body: `${label} body`,
  validation: rep(),
  ...over,
})

const version = (n: number, a: ContentVariant, b: ContentVariant | null): ContentVersion => ({
  version: n,
  source: 'llm',
  sms_text: a.sms_text,
  email_subject: a.email_subject,
  email_body: a.email_body,
  validation: a.validation,
  created_at: '',
  variants: { a, b },
})

function campaign(content: ContentVersion | null, status: Campaign['status'] = 'segmented'): Campaign {
  return {
    id: 'c1',
    brief: 'b',
    status,
    language: 'tr',
    segment: {
      version: 1,
      source: 'llm',
      rule: { groups: [] },
      rendered: { en: [], tr: [] },
      unsupported_criteria: [],
      assumptions: [],
      explanation: '',
      channels_requested: 'both',
      language: 'tr',
      warnings: [],
      audience: { total: 10, sms_reachable: 5, email_reachable: 6, both: 3, counted_at: '' },
    },
    content,
    channels: [],
    approved_content_version: null,
    created_at: '',
    approved_at: null,
    completed_at: null,
  }
}

const sse = (events: [string, unknown][]) =>
  new Response(events.map(([e, d]) => `event: ${e}\ndata: ${JSON.stringify(d)}\n\n`).join(''), {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  })
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
const llm = { provider: 'mock', model: 'm', prompt_version: 'content_v2', latency_ms: 1, attempts: 1, repaired: false, input_tokens: 1, output_tokens: 1, cache_read_tokens: 0, cache_write_tokens: 0, cost_usd: 0, cost_recorded: true }

function setup() {
  let c!: ReturnType<typeof useComposer>
  const w = mount(
    defineComponent({
      setup() {
        c = useComposer('c1')
        return () => null
      },
    }),
  )
  return { c, w }
}

const V1 = version(1, row('Friendly', 'A one'), row('Urgent', 'B one'))
type Call = { url: string; method: string; body: unknown }
let calls: Call[]

function stub(routes: (url: string, method: string, body: unknown) => Response | undefined) {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn().mockImplementation((url: string, init: RequestInit) => {
      const body = init.body ? JSON.parse(init.body as string) : undefined
      calls.push({ url, method: init.method ?? 'GET', body })
      return Promise.resolve(routes(url, init.method ?? 'GET', body) ?? json({ error: { code: 'internal', message: `unstubbed ${url}` } }, 500))
    }),
  )
}

const settle = async () => {
  for (let i = 0; i < 6; i++) await vi.advanceTimersByTimeAsync(0)
  await nextTick()
}

describe('composer: two variants (B-2, F3, F5)', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('loads a two-variant version into two forms', async () => {
    stub((url) => (url === '/api/campaigns/c1' ? json(campaign(V1, 'content_ready')) : undefined))
    const { c } = setup()
    await settle()
    expect(c.hasB.value).toBe(true)
    expect(c.forms.a.sms_text).toBe('A one')
    expect(c.forms.b.label).toBe('Urgent')
    expect(c.hasUnsavedEdits.value).toBe(false)
    expect(c.reportIsCurrent('a')).toBe(true)
    expect(c.reportIsCurrent('b')).toBe(true)
  })

  it('streams both variants, then takes the canonical result', async () => {
    let generated = false
    stub((url, method) => {
      if (url === '/api/campaigns/c1/content/generate') {
        generated = true
        return sse([
          ['delta', { text: '{"variants":{"a":{"label":"Friendly","sms":{"text":"A on' }],
          ['result', { content: V1, llm }],
        ])
      }
      // The campaign has no content until the generation has run.
      if (url === '/api/campaigns/c1' && method === 'GET') return json(campaign(generated ? V1 : null, generated ? 'content_ready' : 'segmented'))
      return undefined
    })
    const { c } = setup()
    await settle()
    expect(c.hasB.value).toBe(false)
    await c.generateContent()
    expect(c.hasB.value).toBe(true)
    expect(c.forms.a.sms_text).toBe('A one')
    expect(c.forms.b.sms_text).toBe('B one')
    expect(c.hasUnsavedEdits.value).toBe(false)
    expect(c.campaign.value?.content?.version).toBe(1)
  })

  it('saves both variants with base_version in the v2 body', async () => {
    const V2 = version(2, row('Friendly', 'A edited', { source: 'edited' }), V1.variants!.b)
    stub((url, method) => {
      if (url === '/api/campaigns/c1' && method === 'GET') {
        return json(campaign(calls.some((x) => x.method === 'PUT') ? V2 : V1, 'content_ready'))
      }
      if (url === '/api/campaigns/c1/content' && method === 'PUT') return json({ content: V2 })
      return undefined
    })
    const { c } = setup()
    await settle()
    c.forms.a.sms_text = 'A edited'
    await nextTick()
    expect(c.hasUnsavedEdits.value).toBe(true)
    await c.saveContent()
    const put = calls.find((x) => x.method === 'PUT')!
    expect(put.body).toEqual({
      base_version: 1,
      variants: {
        a: { label: 'Friendly', sms_text: 'A edited', email_subject: 'Friendly subject', email_body: 'Friendly body' },
        b: { label: 'Urgent', sms_text: 'B one', email_subject: 'Urgent subject', email_body: 'Urgent body' },
      },
    })
    expect(c.hasUnsavedEdits.value).toBe(false)
  })

  it('refines only variant B and keeps A', async () => {
    const refinedB = row('Urgent', 'B shorter', { source: 'refined', instruction: 'daha kısa' })
    const V2 = version(2, V1.variants!.a, refinedB)
    stub((url, method) => {
      if (url === '/api/campaigns/c1' && method === 'GET') return json(campaign(calls.some((x) => x.url.endsWith('/refine')) ? V2 : V1, 'content_ready'))
      if (url === '/api/campaigns/c1/content/refine') {
        return sse([
          ['delta', { text: '{"label":"Urgent","sms":{"text":"B sho' }],
          ['result', { content: V2, llm: { ...llm, prompt_version: 'refine_v1' } }],
        ])
      }
      return undefined
    })
    const { c } = setup()
    await settle()
    await c.refineVariant('b', '  daha kısa ')
    const post = calls.find((x) => x.url.endsWith('/refine'))!
    expect(post.body).toEqual({ variant: 'b', instruction: 'daha kısa', base_version: 1 })
    expect(c.forms.a.sms_text).toBe('A one')
    expect(c.forms.b.sms_text).toBe('B shorter')
    expect(c.campaign.value?.content?.variants?.b?.instruction).toBe('daha kısa')
    expect(c.hasUnsavedEdits.value).toBe(false)
  })

  it('a provider rejection is shown with the server message, and the saved text is restored', async () => {
    const msg = 'The AI provider rejected the request (check the API key or credits).'
    stub((url, method) => {
      if (url === '/api/campaigns/c1' && method === 'GET') return json(campaign(V1, 'content_ready'))
      if (url.endsWith('/content/generate')) {
        return sse([['error', { error: { code: 'llm_provider_rejected', message: msg } }]])
      }
      return undefined
    })
    const { c } = setup()
    await settle()
    await c.generateContent()
    expect(c.error.value?.message).toBe(msg)
    expect(c.error.value?.message).not.toMatch(/timed out/)
    expect(c.forms.a.sms_text).toBe('A one')
  })

  it('approves the A/B split, and a schedule goes out as ISO 8601 with an offset', async () => {
    stub((url, method) => {
      if (url === '/api/campaigns/c1' && method === 'GET') return json(campaign(V1, 'content_ready'))
      if (url.endsWith('/approve')) return json({ campaign: campaign(V1, 'scheduled'), enqueued: { sms: 5, email: 6 } }, 202)
      return undefined
    })
    const { c } = setup()
    await settle()
    c.variant.value = 'ab'
    c.scheduleMode.value = 'later'
    expect(c.gate.value.enabled).toBe(false) // no time picked yet
    c.scheduleAt.value = toLocalInputValue(new Date(Date.now() + 2 * 3_600_000))
    expect(c.gate.value.enabled).toBe(true)
    expect(await c.approve()).toBe(true)
    const body = calls.find((x) => x.url.endsWith('/approve'))!.body as Record<string, unknown>
    expect(body).toMatchObject({ channels: ['sms', 'email'], content_version: 1, variant: 'ab' })
    expect(body.send_at).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:00[+-]\d{2}:\d{2}$/)
    expect(c.campaign.value?.status).toBe('scheduled')
  })

  it('"Send now" sends no send_at', async () => {
    stub((url, method) => {
      if (url === '/api/campaigns/c1' && method === 'GET') return json(campaign(V1, 'content_ready'))
      if (url.endsWith('/approve')) return json({ campaign: campaign(V1, 'sending'), enqueued: { sms: 5, email: 6 } }, 202)
      return undefined
    })
    const { c } = setup()
    await settle()
    c.variant.value = 'b'
    expect(await c.approve()).toBe(true)
    expect(calls.find((x) => x.url.endsWith('/approve'))!.body).toEqual({ channels: ['sms', 'email'], content_version: 1, variant: 'b' })
  })
})

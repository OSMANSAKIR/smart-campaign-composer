import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, refineUrl } from './api'
import { approveGate } from './approveGate'
import { extractVariants } from './partialJson'
import { streamRefine } from './sse'
import type { Campaign, ContentVariant, ContentVersion, ValidationReport } from './types'
import { variantProblem, variantsOf } from './variants'

function report(sms: boolean, email: boolean): ValidationReport {
  return {
    valid: sms && email,
    valid_for: { sms, email },
    fields: {
      sms_text: { runes: 10, worst_case_runes: 20, limit: 160 },
      email_subject: { runes: 10, worst_case_runes: 20, limit: 80 },
      email_body: { runes: 100, limit: 5000 },
    },
    worst_case: { first_name: 'Abdurrahman', city: 'Eskişehir' },
    variables: [],
    errors: [],
    warnings: [],
  }
}

const variant = (label: string, rep: ValidationReport): ContentVariant => ({
  label,
  source: 'llm',
  instruction: null,
  sms_text: 's',
  email_subject: 'e',
  email_body: 'b',
  validation: rep,
})

function content(a: ValidationReport, b: ValidationReport | null): ContentVersion {
  return {
    version: 3,
    source: 'llm',
    sms_text: 's',
    email_subject: 'e',
    email_body: 'b',
    validation: a,
    created_at: '',
    variants: { a: variant('Friendly', a), b: b ? variant('Urgent', b) : null },
  }
}

function campaign(ct: ContentVersion): Campaign {
  return {
    id: 'c1',
    brief: 'b',
    status: 'content_ready',
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
    content: ct,
    channels: [],
    approved_content_version: null,
    created_at: '',
    approved_at: null,
    completed_at: null,
  }
}

describe('A/B send choice is gated per variant and channel (B-2)', () => {
  const ok = report(true, true)
  const smsBad = report(false, true)

  it.each([
    ['a', content(ok, ok), ['sms', 'email'], true],
    ['b', content(ok, ok), ['sms', 'email'], true],
    ['ab', content(ok, ok), ['sms', 'email'], true],
    ['b', content(ok, null), ['sms'], false], // no variant B
    ['ab', content(ok, null), ['sms'], false],
    ['a', content(ok, null), ['sms'], true],
    ['b', content(ok, smsBad), ['sms'], false], // B invalid for SMS
    ['b', content(ok, smsBad), ['email'], true], // …but fine for email only
    ['ab', content(ok, smsBad), ['sms', 'email'], false],
    ['a', content(ok, smsBad), ['sms', 'email'], true], // A is what is sent
  ] as const)('variant %s → enabled=%s', (v, ct, channels, enabled) => {
    expect(approveGate({ campaign: campaign(ct), channels: [...channels], hasUnsavedEdits: false, variant: v }).enabled).toBe(enabled)
  })

  it('explains a missing B', () => {
    expect(variantProblem(content(ok, null), 'b', ['sms'])).toMatch(/no variant B/)
  })

  it('a version without `variants` (older server) reads as a single variant A', () => {
    const old = { ...content(ok, ok), variants: undefined }
    expect(variantsOf(old).b).toBeNull()
    expect(variantsOf(old).a.sms_text).toBe('s')
  })

  it('a scheduled or cancelled campaign cannot be approved again (F4, F5)', () => {
    for (const status of ['scheduled', 'cancelled'] as const) {
      const c = { ...campaign(content(ok, ok)), status }
      expect(approveGate({ campaign: c, channels: ['sms'], hasUnsavedEdits: false }).enabled).toBe(false)
    }
  })

  it('a schedule problem disables approve', () => {
    const g = approveGate({
      campaign: campaign(content(ok, ok)),
      channels: ['sms'],
      hasUnsavedEdits: false,
      scheduleProblem: 'Pick a time at least one minute from now.',
    })
    expect(g.enabled).toBe(false)
    expect(g.reason).toMatch(/one minute/)
  })
})

describe('streaming display of two variants (B-2)', () => {
  const full = JSON.stringify({
    language: 'tr',
    variants: {
      a: { label: 'Samimi', sms: { text: 'Merhaba {{first_name}}' }, email: { subject: 'Selam', body: 'Gövde A' } },
      b: { label: 'Acil', sms: { text: 'Son gün!' }, email: { subject: 'Acele', body: 'Gövde B' } },
    },
    notes: 'x',
  })

  it('shows variant a first, then b as it arrives', () => {
    const early = extractVariants(full.slice(0, full.indexOf('"b"') - 2))
    expect(early.a?.smsText).toBe('Merhaba {{first_name}}')
    expect(early.b).toBeNull()
    const mid = extractVariants(full.slice(0, full.indexOf('Son gün') + 3))
    expect(mid.b?.smsText).toBe('Son')
    expect(mid.b?.label).toBe('Acil')
  })

  it('every prefix parses without throwing and ends with both variants', () => {
    for (let i = 0; i <= full.length; i++) extractVariants(full.slice(0, i))
    const x = extractVariants(full)
    expect(x.a?.emailBody).toBe('Gövde A')
    expect(x.b?.emailSubject).toBe('Acele')
  })

  it('a refine answer (no `variants`) reads as one variant', () => {
    const x = extractVariants('{"label":"Kısa","sms":{"text":"Hey"},"email":{"subject":"S","body":"B"}}')
    expect(x.a).toMatchObject({ label: 'Kısa', smsText: 'Hey', emailBody: 'B' })
    expect(x.b).toBeNull()
  })
})

describe('v2 client calls (stubbed fetch)', () => {
  afterEach(() => vi.unstubAllGlobals())
  const stub = (status = 200, body: unknown = {}) => {
    const f = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(body), { status })))
    vi.stubGlobal('fetch', f)
    return f
  }
  const sent = (f: ReturnType<typeof vi.fn>, i = 0) => {
    const [url, init] = f.mock.calls[i] as [string, RequestInit]
    return { url, method: init.method, body: init.body ? JSON.parse(init.body as string) : undefined }
  }

  it('saveContent sends the v2 body with both variants', async () => {
    const f = stub()
    const e = { label: 'A', sms_text: '1', email_subject: '2', email_body: '3' }
    await api.saveContent('c1', { variants: { a: e, b: { ...e, label: 'B' } } }, 4)
    expect(sent(f)).toEqual({ url: '/api/campaigns/c1/content', method: 'PUT', body: { base_version: 4, variants: { a: e, b: { ...e, label: 'B' } } } })
  })

  it('approve sends variant and send_at only when scheduling', async () => {
    const f = stub()
    await api.approve('c1', ['sms'], 3, 'k', { variant: 'ab', sendAt: '2026-11-11T09:00:00+03:00' })
    await api.approve('c1', ['sms'], 3, 'k', { variant: 'b' })
    expect(sent(f, 0).body).toEqual({ channels: ['sms'], content_version: 3, variant: 'ab', send_at: '2026-11-11T09:00:00+03:00' })
    expect(sent(f, 1).body).toEqual({ channels: ['sms'], content_version: 3, variant: 'b' })
  })

  it('previewSegment, cancel and catalog use the documented paths', async () => {
    const f = stub()
    await api.previewSegment({ groups: [] }, 'c1')
    await api.cancel('c1')
    await api.catalog()
    expect(sent(f, 0)).toEqual({ url: '/api/segment/preview', method: 'POST', body: { rule: { groups: [] }, campaign_id: 'c1' } })
    expect(sent(f, 1)).toMatchObject({ url: '/api/campaigns/c1/cancel', method: 'POST' })
    expect(sent(f, 2)).toMatchObject({ url: '/api/catalog', method: 'GET' })
  })

  it('streamRefine posts the instruction to the refine url and delivers events', async () => {
    const sse = 'event: meta\ndata: {"provider":"mock","model":"m","prompt_version":"refine_v1"}\n\nevent: delta\ndata: {"text":"{}"}\n\n'
    const f = vi.fn().mockResolvedValue(new Response(sse, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }))
    vi.stubGlobal('fetch', f)
    const seen: string[] = []
    await streamRefine('c1', { variant: 'b', instruction: 'daha kısa', base_version: 3 }, (e) => seen.push(e.type))
    expect(seen).toEqual(['meta', 'delta'])
    expect(sent(f)).toEqual({ url: refineUrl('c1'), method: 'POST', body: { variant: 'b', instruction: 'daha kısa', base_version: 3 } })
  })
})

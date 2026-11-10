// @vitest-environment jsdom
// ContentStep (two variant cards, refine) and AudienceStep (Edit rule, Edited badge, original rule).
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { CATALOG } from '../lib/catalogFixture'
import { resetCatalogCache } from '../lib/catalog'
import type { Campaign, ContentVariant, ValidationReport, VariantInput } from '../lib/types'
import AudienceStep from './AudienceStep.vue'
import ContentStep from './ContentStep.vue'

const rep = (): ValidationReport => ({
  valid: true,
  valid_for: { sms: true, email: true },
  fields: {
    sms_text: { runes: 5, worst_case_runes: 9, limit: 160, encoding: 'gsm7', utf16_units: 9, segments: 1 },
    email_subject: { runes: 5, worst_case_runes: 9, limit: 80 },
    email_body: { runes: 50, limit: 5000 },
  },
  worst_case: { first_name: 'Abdurrahman', city: 'Eskişehir' },
  variables: [],
  errors: [],
  warnings: [],
})
const row = (label: string, over: Partial<ContentVariant> = {}): ContentVariant => ({
  label, source: 'llm', instruction: null, sms_text: `${label} sms`, email_subject: `${label} subj`, email_body: `${label} body`, validation: rep(), ...over,
})
const input = (v: ContentVariant): VariantInput => ({ label: v.label, sms_text: v.sms_text, email_subject: v.email_subject, email_body: v.email_body })

function campaign(over: Partial<Campaign> = {}, b: ContentVariant | null = row('Urgent', { source: 'refined', instruction: 'daha kısa' })): Campaign {
  const a = row('Friendly')
  return {
    id: 'c1', brief: 'b', status: 'content_ready', language: 'tr',
    segment: {
      version: 2, source: 'edited', rule: { groups: [{ conditions: [{ field: 'city', op: 'eq', value: 'Ankara' }] }] },
      original_rule: { groups: [{ conditions: [{ field: 'city', op: 'eq', value: 'İstanbul' }] }] }, edited_at: '2026-11-10T20:00:00Z',
      rendered: { en: ['City is Ankara'], tr: ['Şehir: Ankara'] }, unsupported_criteria: [], assumptions: [], explanation: '',
      channels_requested: 'both', language: 'tr', warnings: [],
      audience: { total: 10, sms_reachable: 5, email_reachable: 6, both: 3, counted_at: '' },
    },
    content: { version: 4, source: 'llm', sms_text: a.sms_text, email_subject: a.email_subject, email_body: a.email_body, validation: a.validation, created_at: '', variants: { a, b } },
    channels: [], approved_content_version: null, created_at: '', approved_at: null, completed_at: null, ...over,
  }
}

function mountContent(c: Campaign, over: Record<string, unknown> = {}) {
  const v = c.content!.variants!
  const forms = reactive({ a: input(v.a), b: v.b ? input(v.b) : input(row('Variant B')) })
  const checks = reactive({ a: { report: rep(), key: 'k', validating: false }, b: { report: rep(), key: 'k', validating: false } })
  return mount(ContentStep, {
    props: {
      campaign: c, forms, hasB: !!v.b, checks, reportIsCurrent: () => true,
      stream: { active: false, repairing: false, attempt: 1, started: false, target: 'all' },
      validating: false, hasUnsavedEdits: false, saving: false, llm: null, ...over,
    },
  })
}

describe('ContentStep: two variant cards (B-2, F3)', () => {
  it('shows both variants with their own labelled fields and counters', () => {
    const w = mountContent(campaign())
    expect(w.findAll('section[data-variant]').map((s) => s.attributes('data-variant'))).toEqual(['a', 'b'])
    for (const k of ['A', 'B']) {
      for (const f of ['SMS text', 'Email subject', 'Email body']) {
        expect(w.find(`label[for="${f.toLowerCase().replace(' ', '-')}-${k.toLowerCase()}"]`).text()).toBe(`${f}, variant ${k}`)
      }
    }
    expect(w.get('#sms-text-b').element).toHaveProperty('value', 'Urgent sms')
    expect(w.text()).toContain('/ 160 with longest name and city')
    expect(w.text()).toContain('Both variants are valid for SMS and email.')
  })

  it('shows one card when there is no variant B', () => {
    const w = mountContent(campaign({}, null))
    expect(w.findAll('section[data-variant]')).toHaveLength(1)
  })

  it('shows the refine history hint "Version N (refined: instruction)"', () => {
    const w = mountContent(campaign())
    expect(w.get('section[data-variant="b"]').text()).toContain('Version 4 (refined: daha kısa)')
    expect(w.get('section[data-variant="a"]').text()).not.toContain('refined:')
  })

  it('refine: counts up to 500 characters, emits the variant and instruction', async () => {
    const w = mountContent(campaign())
    const card = w.get('section[data-variant="b"]')
    const btn = () => card.get('button[type="submit"]')
    expect(btn().attributes('disabled')).toBeDefined() // empty instruction
    await card.get('#refine-b').setValue('daha samimi')
    expect(card.text()).toContain('11 / 500')
    expect(btn().attributes('disabled')).toBeUndefined()
    await card.get('form').trigger('submit')
    expect(w.emitted('refine')).toEqual([['b', 'daha samimi']])

    await card.get('#refine-b').setValue('x'.repeat(501))
    expect(card.text()).toContain('501 / 500')
    expect(btn().attributes('disabled')).toBeDefined()
  })

  it('refine is blocked while there are unsaved edits, with the reason shown', () => {
    const w = mountContent(campaign(), { hasUnsavedEdits: true })
    const card = w.get('section[data-variant="a"]')
    expect(card.text()).toContain('Save your changes first; Claude rewrites the saved version.')
    expect(w.get('button.btn.primary').text()).toBe('Write a new draft with Claude')
  })

  it('after an audience change the content can be re-checked and saved without edits', () => {
    const w = mountContent(campaign({ status: 'segmented' }))
    const save = w.findAll('button').find((b) => b.text() === 'Re-check and save')!
    expect(save.attributes('disabled')).toBeUndefined()
  })
})

describe('AudienceStep: rule editor entry (F2)', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    resetCatalogCache()
  })

  function stubCatalog() {
    const f = vi.fn().mockImplementation((url: string) =>
      Promise.resolve(
        url === '/api/catalog'
          ? new Response(JSON.stringify(CATALOG), { status: 200 })
          : new Response(JSON.stringify({ valid: true, errors: [], warnings: [], rendered: { en: ['City is Ankara'], tr: ['Şehir: Ankara'] }, audience: { total: 3, sms_reachable: 1, email_reachable: 2, both: 1, counted_at: '' } }), { status: 200 }),
      ),
    )
    vi.stubGlobal('fetch', f)
    return f
  }

  it('shows the Edited badge and Claude’s original rule rendered from the catalog labels', async () => {
    stubCatalog()
    const w = mount(AudienceStep, { props: { campaign: campaign(), llm: null, busy: false, saveRule: async () => true, savingRule: false } })
    await flushPromises()
    expect(w.get('header .pill').text()).toBe('Edited')
    const d = w.get('details.original')
    expect(d.text()).toContain('Claude’s original rule')
    expect(d.get('li.chip').text()).toBe('Şehir is İstanbul')
  })

  it('has no Edited badge or original rule for a rule straight from Claude', async () => {
    stubCatalog()
    const c = campaign()
    c.segment = { ...c.segment!, source: 'llm', original_rule: null, edited_at: null }
    const w = mount(AudienceStep, { props: { campaign: c, llm: null, busy: false, saveRule: async () => true, savingRule: false } })
    await flushPromises()
    expect(w.get('header .pill').text()).toBe('Proposed by Claude')
    expect(w.find('details.original').exists()).toBe(false)
  })

  it('Edit rule loads the catalog and opens the editor; saving closes it', async () => {
    vi.useFakeTimers()
    const settle = async () => {
      for (let i = 0; i < 6; i++) await vi.advanceTimersByTimeAsync(0)
    }
    const f = stubCatalog()
    const save = vi.fn().mockResolvedValue(true)
    const w = mount(AudienceStep, { props: { campaign: campaign(), llm: null, busy: false, saveRule: save, savingRule: false } })
    await settle()
    await w.findAll('button').find((b) => b.text() === 'Edit rule')!.trigger('click')
    await settle()
    expect(w.find('[aria-labelledby="rule-edit-h"]').exists()).toBe(true)
    expect(f.mock.calls.filter((c) => (c as [string])[0] === '/api/catalog')).toHaveLength(1) // cached
    await w.get('select[aria-label="Condition 1 of group 1: value"]').setValue('İzmir')
    await vi.advanceTimersByTimeAsync(300) // the debounced preview settles before Save enables
    await settle()
    await w.findAll('button').find((b) => b.text() === 'Save rule')!.trigger('click')
    await settle()
    vi.useRealTimers()
    expect(save).toHaveBeenCalledWith({ groups: [{ conditions: [{ field: 'city', op: 'eq', value: 'İzmir' }] }] })
    expect(w.find('[aria-labelledby="rule-edit-h"]').exists()).toBe(false)
  })

  it('shows the cost notice for the audience when the cost was not recorded (F6)', async () => {
    stubCatalog()
    const llm = { provider: 'a', model: 'm', prompt_version: 'segment_v1', latency_ms: 1, attempts: 1, repaired: false, input_tokens: 1, output_tokens: 1, cache_read_tokens: 0, cache_write_tokens: 0, cost_usd: 0, cost_recorded: false }
    const w = mount(AudienceStep, { props: { campaign: campaign(), llm, busy: false, saveRule: async () => true, savingRule: false } })
    await flushPromises()
    expect(w.text()).toContain('The audience is ready, but its cost could not be recorded.')
  })
})

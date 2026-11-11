// @vitest-environment jsdom
// B-3 components: the language breakdown in the audience step, the "Write the English version" button and the
// TR | EN tabs with the stale notice in the content step, the language choice in the approve step.
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { approveGate } from '../lib/approveGate'
import { CATALOG } from '../lib/catalogFixture'
import { resetCatalogCache } from '../lib/catalog'
import type { Campaign, ContentVariant, LocalizedCopy, ValidationReport, VariantInput } from '../lib/types'
import ApproveStep from './ApproveStep.vue'
import AudienceStep from './AudienceStep.vue'
import ContentStep from './ContentStep.vue'

const rep = (smsOk = true): ValidationReport => ({
  valid: smsOk, valid_for: { sms: smsOk, email: true },
  fields: {
    sms_text: { runes: 5, worst_case_runes: 9, limit: 160, encoding: 'gsm7', utf16_units: 9, segments: 1 },
    email_subject: { runes: 5, worst_case_runes: 9, limit: 80 }, email_body: { runes: 50, limit: 5000 },
  },
  worst_case: { first_name: 'Abdurrahman', city: 'Eskişehir' }, variables: [], errors: [], warnings: [],
})
const loc = (label: string, over: Partial<LocalizedCopy> = {}): LocalizedCopy => ({
  label, source: 'llm', sms_text: `${label} sms`, email_subject: `${label} subj`, email_body: `${label} body`, validation: rep(), stale: false, ...over,
})
const row = (label: string, localized?: Partial<Record<'en' | 'tr', LocalizedCopy>>): ContentVariant => ({
  label, source: 'llm', instruction: null, sms_text: `${label} sms`, email_subject: `${label} subj`, email_body: `${label} body`,
  validation: rep(), locale: 'tr', localized,
})
const input = (v: { label: string; sms_text: string; email_subject: string; email_body: string }): VariantInput => ({
  label: v.label, sms_text: v.sms_text, email_subject: v.email_subject, email_body: v.email_body,
})

function campaign(a: ContentVariant, b: ContentVariant | null, status: Campaign['status'] = 'content_ready', enTotal = 10): Campaign {
  return {
    id: 'c1', brief: 'b', status, language: 'tr',
    segment: {
      version: 1, source: 'llm', rule: { groups: [] }, rendered: { en: ['City is Ankara'], tr: ['Şehir: Ankara'] }, unsupported_criteria: [],
      assumptions: [], explanation: '', channels_requested: 'both', language: 'tr', warnings: [],
      audience: {
        total: 20 + enTotal, sms_reachable: 25, email_reachable: 28, both: 24, counted_at: '',
        by_locale: { tr: { total: 20, sms_reachable: 18, email_reachable: 20, both: 18 }, en: { total: enTotal, sms_reachable: enTotal, email_reachable: enTotal, both: enTotal } },
      },
    },
    content: { version: 4, source: 'llm', sms_text: a.sms_text, email_subject: a.email_subject, email_body: a.email_body, validation: a.validation, created_at: '', variants: { a, b } },
    channels: [], approved_content_version: null, created_at: '', approved_at: null, completed_at: null,
  }
}

describe('AudienceStep: language breakdown (B-3)', () => {
  it('shows people and consented counts per language', () => {
    resetCatalogCache()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(CATALOG), { status: 200 })))
    const w = mount(AudienceStep, { props: { campaign: campaign(row('A'), row('B')), llm: null, busy: false, saveRule: async () => true, savingRule: false } })
    const rows = w.findAll('table[aria-label="People who can be reached, by the language they read"] tbody tr').map((r) => [r.get('th').text(), ...r.findAll('td').map((c) => c.text())])
    expect(rows).toEqual([['Turkish', '20', '18', '20'], ['English', '10', '10', '10']])
    vi.unstubAllGlobals()
  })

  it('shows no table for a campaign counted before B-3', () => {
    const c = campaign(row('A'), row('B'))
    delete c.segment!.audience.by_locale
    const w = mount(AudienceStep, { props: { campaign: c, llm: null, busy: false, saveRule: async () => true, savingRule: false } })
    expect(w.find('.languages').exists()).toBe(false)
  })
})

function mountContent(c: Campaign, over: Record<string, unknown> = {}) {
  const v = c.content!.variants!
  const forms = reactive({ a: input(v.a), b: input(v.b!) })
  const loc = (k: 'a' | 'b') => v[k]?.localized?.en ?? null
  const localForms = reactive({ a: loc('a') ? input(loc('a')!) : input({ label: '', sms_text: '', email_subject: '', email_body: '' }),
    b: loc('b') ? input(loc('b')!) : input({ label: '', sms_text: '', email_subject: '', email_body: '' }) })
  const checks = reactive({ a: { report: rep(), key: 'k', validating: false }, b: { report: rep(), key: 'k', validating: false } })
  const localChecks = reactive({ a: { report: loc('a')?.validation ?? null, key: 'k', validating: false }, b: { report: loc('b')?.validation ?? null, key: 'k', validating: false } })
  return mount(ContentStep, {
    props: {
      campaign: c, forms, hasB: true, checks, reportIsCurrent: () => true,
      stream: { active: false, repairing: false, attempt: 1, started: false, target: 'all' },
      validating: false, hasUnsavedEdits: false, saving: false, llm: null,
      localForms, localChecks, localReportIsCurrent: () => true, localStale: (k: 'a' | 'b') => !!loc(k)?.stale, savedLocalized: loc,
      otherRecipients: c.segment!.audience.by_locale!.en!.total, hasLocalized: !!loc('a'), ...over,
    },
  })
}
const buttonWith = (w: ReturnType<typeof mountContent>, text: string) => w.findAll('button').find((b) => b.text().includes(text))

describe('ContentStep: the other language (B-3)', () => {
  it('offers "Write the English version (10 recipients)" and emits localize', async () => {
    const w = mountContent(campaign(row('Friendly'), row('Urgent')))
    const b = buttonWith(w, 'Write the English version (10 recipients)')!
    expect(b).toBeTruthy()
    expect(b.attributes('disabled')).toBeUndefined()
    await b.trigger('click')
    expect(w.emitted('localize')).toHaveLength(1)
  })

  it('says "1 recipient" for one person and offers nothing when nobody reads English', () => {
    expect(buttonWith(mountContent(campaign(row('A'), row('B'), 'content_ready', 1)), 'Write the English version (1 recipient)')).toBeTruthy()
    const none = mountContent(campaign(row('A'), row('B'), 'content_ready', 0))
    expect(buttonWith(none, 'English version')).toBeUndefined()
    expect(none.find('[role="tablist"]').exists()).toBe(false) // no tabs without an adaptation or a reader
  })

  it('is blocked, with the reason, while there are unsaved edits (it adapts the saved version)', () => {
    const w = mountContent(campaign(row('A'), row('B')), { hasUnsavedEdits: true })
    const b = buttonWith(w, 'Write the English version')!
    expect(b.attributes('disabled')).toBeDefined()
    expect(b.attributes('title')).toContain('Save your changes first')
  })

  it('shows TR | EN tabs per variant; the EN tab holds the editable adaptation', async () => {
    const w = mountContent(campaign(row('Friendly', { en: loc('Friendly EN') }), row('Urgent', { en: loc('Urgent EN') })))
    const card = w.get('[data-variant="a"]')
    expect(card.findAll('[role="tab"]').map((t) => t.text())).toEqual(['TR', 'EN'])
    expect(card.get('[role="tab"][data-tab="tr"]').attributes('aria-selected')).toBe('true')
    expect((card.get('#sms-text-a').element as HTMLTextAreaElement).value).toBe('Friendly sms')
    await card.get('[role="tab"][data-tab="en"]').trigger('click')
    const en = card.get('#sms-text-a-en').element as HTMLTextAreaElement
    expect(en.value).toBe('Friendly EN sms')
    expect(en.readOnly).toBe(false)
    await card.get('#sms-text-a-en').setValue('Edited EN sms')
    expect((card.get('#sms-text-a-en').element as HTMLTextAreaElement).value).toBe('Edited EN sms')
    expect((card.get('#label-a-en').element as HTMLInputElement).value).toBe('Friendly EN')
    expect(card.text()).not.toContain('out of date')
    // The refine form belongs to the primary copy only.
    expect(card.find('form.refine').exists()).toBe(false)
    await card.get('[role="tab"][data-tab="tr"]').trigger('click')
    expect(card.find('form.refine').exists()).toBe(true)
  })

  it('shows the stale notice on the EN tab with a Re-adapt button that emits localize', async () => {
    const w = mountContent(campaign(row('Friendly', { en: loc('Friendly EN', { stale: true }) }), row('Urgent', { en: loc('Urgent EN') })))
    const card = w.get('[data-variant="a"]')
    expect(card.get('[data-tab="en"]').text()).toContain('out of date')
    expect(w.get('[data-variant="b"]').get('[data-tab="en"]').text()).not.toContain('out of date')
    await card.get('[data-tab="en"]').trigger('click')
    expect(card.get('.notice.warn').text()).toContain('The Turkish text changed')
    const re = card.findAll('button').find((b) => b.text() === 'Re-adapt to English')!
    await re.trigger('click')
    expect(w.emitted('localize')).toHaveLength(1)
  })

  it('shows an empty state when there is no adaptation yet, and shows the adaptation while it streams', async () => {
    const w = mountContent(campaign(row('Friendly'), row('Urgent')))
    const card = w.get('[data-variant="a"]')
    await card.get('[data-tab="en"]').trigger('click')
    expect(card.text()).toContain('There is no English version of variant A yet.')
    await w.setProps({ stream: { active: true, repairing: false, attempt: 1, started: true, target: 'localize' } })
    expect(w.text()).toContain('Claude is adapting both variants to English')
    expect(card.text()).not.toContain('There is no English version')
  })

  it('arrow keys move between the tabs', async () => {
    const w = mountContent(campaign(row('Friendly', { en: loc('Friendly EN') }), row('Urgent', { en: loc('Urgent EN') })), {})
    const card = w.get('[data-variant="a"]')
    await card.get('[data-tab="tr"]').trigger('keydown', { key: 'ArrowRight' })
    expect(card.get('[data-tab="en"]').attributes('aria-selected')).toBe('true')
  })
})

describe('ApproveStep: language choice (B-3)', () => {
  const base = (c: Campaign, over: Record<string, unknown> = {}) => ({
    campaign: c, channels: ['sms', 'email'], gate: approveGate({ campaign: c, channels: ['sms', 'email'], hasUnsavedEdits: false, variant: 'ab' }),
    recipients: { sms: 25, email: 28 }, approving: false, variant: 'ab', scheduleMode: 'now', scheduleAt: '', scheduleProblem: '',
    offersLanguage: true, languageMode: 'recipient', languageProblem: '', ...over,
  })

  it('offers both options, with each recipient in their language selected when valid', async () => {
    const w = mount(ApproveStep, { props: base(campaign(row('A', { en: loc('A') }), row('B', { en: loc('B') }))) as never })
    const radios = w.findAll('input[name="language-mode"]')
    expect(radios.map((r) => (r.element as HTMLInputElement).checked)).toEqual([false, true])
    expect(w.text()).toContain('Everyone in Turkish')
    expect(w.text()).toContain('Each recipient in their language')
    await radios[0]!.setValue()
    expect(w.emitted('update:languageMode')![0]).toEqual(['campaign'])
  })

  it('disables the recipient option and says why when an adaptation is missing', () => {
    const c = campaign(row('A'), row('B'))
    const problem = 'Variant A has no English version yet. Write it in the content step.'
    const w = mount(ApproveStep, { props: base(c, { languageMode: 'campaign', languageProblem: problem }) as never })
    const radios = w.findAll('input[name="language-mode"]')
    expect((radios[1]!.element as HTMLInputElement).disabled).toBe(true)
    expect((radios[0]!.element as HTMLInputElement).disabled).toBe(false)
    expect(w.get('#language-note').text()).toBe(problem)
  })

  it('has no language choice when nobody reads the other language, and the dialog names the choice', () => {
    const none = mount(ApproveStep, { props: base(campaign(row('A'), row('B')), { offersLanguage: false, languageMode: 'campaign' }) as never })
    expect(none.find('input[name="language-mode"]').exists()).toBe(false)
    expect(none.text()).not.toContain('Each recipient in their')
    const yes = mount(ApproveStep, { props: base(campaign(row('A', { en: loc('A') }), row('B', { en: loc('B') }))) as never })
    expect(yes.get('dialog').text()).toContain('Each recipient in their own language')
    const camp = mount(ApproveStep, { props: base(campaign(row('A'), row('B')), { languageMode: 'campaign' }) as never })
    expect(camp.get('dialog').text()).toContain('Everyone in Turkish')
  })
})

// @vitest-environment jsdom
// Small component checks: the cost notice and Cache write row (F6, V-022), the A/B choice and its note (B-2).
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { approveGate } from '../lib/approveGate'
import type { Campaign, ContentVariant, LlmMeta, ValidationReport } from '../lib/types'
import ApproveStep from './ApproveStep.vue'
import LlmPanel from './LlmPanel.vue'

const llm = (over: Partial<LlmMeta> = {}): LlmMeta => ({
  provider: 'anthropic',
  model: 'claude-sonnet-5-5',
  prompt_version: 'content_v2',
  latency_ms: 2000,
  attempts: 1,
  repaired: false,
  input_tokens: 1800,
  output_tokens: 200,
  cache_read_tokens: 1600,
  cache_write_tokens: 321,
  cost_usd: 0.0027,
  cost_recorded: true,
  ...over,
})

describe('LlmPanel (F6, V-022)', () => {
  it('shows Cache read and Cache write rows', () => {
    const w = mount(LlmPanel, { props: { llm: llm(), what: 'content' } })
    const rows = w.findAll('dl.meta > div').map((d) => d.text())
    expect(rows).toContain('Cache read1,600')
    expect(rows).toContain('Cache write321')
    expect(w.find('.notice').exists()).toBe(false)
  })

  it.each([
    ['content', 'The content is ready, but its cost could not be recorded.'],
    ['audience', 'The audience is ready, but its cost could not be recorded.'],
  ] as const)('shows a non-blocking notice for %s when the cost was not recorded', (what, text) => {
    const w = mount(LlmPanel, { props: { llm: llm({ cost_recorded: false }), what } })
    expect(w.get('.notice').text()).toBe(text)
    expect(w.get('.notice').attributes('role')).toBe('status') // announced, not an alert that blocks
  })
})

const rep = (sms: boolean, email: boolean): ValidationReport => ({
  valid: sms && email,
  valid_for: { sms, email },
  fields: {
    sms_text: { runes: 1, worst_case_runes: 1, limit: 160 },
    email_subject: { runes: 1, worst_case_runes: 1, limit: 80 },
    email_body: { runes: 1, limit: 5000 },
  },
  worst_case: { first_name: 'x', city: 'y' },
  variables: [],
  errors: [],
  warnings: [],
})
const row = (label: string, r: ValidationReport): ContentVariant => ({
  label, source: 'llm', instruction: null, sms_text: 's', email_subject: 'e', email_body: 'b', validation: r,
})

function campaign(b: ContentVariant | null): Campaign {
  const a = row('Friendly', rep(true, true))
  return {
    id: 'c1', brief: 'b', status: 'content_ready', language: 'tr',
    segment: {
      version: 1, source: 'llm', rule: { groups: [] }, rendered: { en: [], tr: [] }, unsupported_criteria: [], assumptions: [],
      explanation: '', channels_requested: 'both', language: 'tr', warnings: [],
      audience: { total: 10, sms_reachable: 5, email_reachable: 6, both: 3, counted_at: '' },
    },
    content: { version: 1, source: 'llm', sms_text: 's', email_subject: 'e', email_body: 'b', validation: a.validation, created_at: '', variants: { a, b } },
    channels: [], approved_content_version: null, created_at: '', approved_at: null, completed_at: null,
  }
}

function mountApprove(c: Campaign, variant: 'a' | 'b' | 'ab' = 'a', mode: 'now' | 'later' = 'now') {
  return mount(ApproveStep, {
    props: {
      campaign: c, channels: ['sms', 'email'], gate: approveGate({ campaign: c, channels: ['sms', 'email'], hasUnsavedEdits: false, variant }),
      recipients: { sms: 5, email: 6 }, approving: false, variant, scheduleMode: mode, scheduleAt: '', scheduleProblem: '',
    },
  })
}
const radio = (w: ReturnType<typeof mountApprove>, name: string) => w.findAll('input[name="send-variant"]').find((r) => r.element.parentElement?.textContent?.includes(name))!

describe('ApproveStep A/B choice (B-2)', () => {
  it('offers Send A, Send B and A/B test with the 50/50 note', () => {
    const w = mountApprove(campaign(row('Urgent', rep(true, true))))
    expect(w.text()).toContain('Send A')
    expect(w.text()).toContain('Send B')
    expect(w.text()).toContain('A/B test (50/50)')
    expect(w.text()).toContain('Delivery is split 50/50 per user; the mock provider has no open or click data.')
    expect(radio(w, 'Send B').attributes('disabled')).toBeUndefined()
  })

  it('disables the B options when there is no B', () => {
    const w = mountApprove(campaign(null))
    expect(radio(w, 'Send A').attributes('disabled')).toBeUndefined()
    expect(radio(w, 'Send B').attributes('disabled')).toBeDefined()
    expect(radio(w, 'A/B test').attributes('disabled')).toBeDefined()
  })

  it('disables the B options when B is invalid for a selected channel', () => {
    const w = mountApprove(campaign(row('Urgent', rep(false, true))))
    expect(radio(w, 'Send B').attributes('disabled')).toBeDefined()
    expect(radio(w, 'A/B test').attributes('disabled')).toBeDefined()
  })

  it('shows the schedule input only in Schedule mode, with a label', () => {
    expect(mountApprove(campaign(null)).find('#send-at').exists()).toBe(false)
    const w = mountApprove(campaign(null), 'a', 'later')
    expect(w.get('#send-at').attributes('type')).toBe('datetime-local')
    expect(w.get('label[for="send-at"]').text()).toContain('Send at')
  })
})

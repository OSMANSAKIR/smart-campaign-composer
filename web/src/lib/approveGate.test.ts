import { describe, expect, it } from 'vitest'
import { approveGate } from './approveGate'
import type { Campaign, Channel, ValidationReport } from './types'

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

function campaign(over: Partial<Campaign> = {}, rep = report(true, true), sms = 160, email = 185): Campaign {
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
      audience: { total: 231, sms_reachable: sms, email_reachable: email, both: 128, counted_at: '' },
    },
    content: { version: 2, source: 'edited', sms_text: 'a', email_subject: 'b', email_body: 'c', validation: rep, created_at: '' },
    channels: [],
    approved_content_version: null,
    created_at: '',
    approved_at: null,
    completed_at: null,
    ...over,
  }
}

const both: Channel[] = ['sms', 'email']

describe('approveGate (R-FLOW-3)', () => {
  it('is enabled for valid, saved content with a non-empty audience', () => {
    expect(approveGate({ campaign: campaign(), channels: both, hasUnsavedEdits: false })).toEqual({ enabled: true, reason: '' })
  })

  it('is disabled when the content is invalid for a chosen channel', () => {
    const c = campaign({}, report(false, true))
    expect(approveGate({ campaign: c, channels: both, hasUnsavedEdits: false }).enabled).toBe(false)
    expect(approveGate({ campaign: c, channels: ['sms'], hasUnsavedEdits: false }).enabled).toBe(false)
  })

  it('ignores a channel that is not chosen', () => {
    const c = campaign({}, report(false, true))
    expect(approveGate({ campaign: c, channels: ['email'], hasUnsavedEdits: false }).enabled).toBe(true)
  })

  it('is disabled for a stale campaign (audience changed after the content was saved)', () => {
    const g = approveGate({ campaign: campaign({ status: 'segmented' }), channels: both, hasUnsavedEdits: false })
    expect(g.enabled).toBe(false)
    expect(g.reason).toMatch(/audience changed/i)
  })

  it('is disabled while the editor holds unsaved edits', () => {
    expect(approveGate({ campaign: campaign(), channels: both, hasUnsavedEdits: true }).enabled).toBe(false)
  })

  it('is disabled for an empty audience on a chosen channel', () => {
    const c = campaign({}, report(true, true), 0, 185)
    expect(approveGate({ campaign: c, channels: both, hasUnsavedEdits: false }).enabled).toBe(false)
    expect(approveGate({ campaign: c, channels: ['email'], hasUnsavedEdits: false }).enabled).toBe(true)
  })

  it('is disabled with no channel, no content, no campaign, or after approval', () => {
    expect(approveGate({ campaign: campaign(), channels: [], hasUnsavedEdits: false }).enabled).toBe(false)
    expect(approveGate({ campaign: campaign({ content: null }), channels: both, hasUnsavedEdits: false }).enabled).toBe(false)
    expect(approveGate({ campaign: null, channels: both, hasUnsavedEdits: false }).enabled).toBe(false)
    expect(approveGate({ campaign: campaign({ status: 'sending' }), channels: both, hasUnsavedEdits: false }).enabled).toBe(false)
  })

  it('is disabled while a request is in flight', () => {
    expect(approveGate({ campaign: campaign(), channels: both, hasUnsavedEdits: false, busy: true }).enabled).toBe(false)
  })
})

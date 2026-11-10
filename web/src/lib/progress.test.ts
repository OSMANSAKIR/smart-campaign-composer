import { describe, expect, it } from 'vitest'
import { applyEvent, applySnapshot, channelFraction, emptyDashboard, errorLabel, MAX_RECENT_EVENTS, overallFraction, variantRows } from './progress'
import type { Progress, ProgressEvent } from './types'

function snap(sent: number, extra: Partial<Progress> = {}): Progress {
  return {
    campaign_id: 'c1',
    status: 'sending',
    channels: {
      sms: { total: 10, queued: 10 - sent, sending: 0, sent, retry: 0, failed: 0, skipped_no_consent: 0, throughput_per_s: 1, paused_until: null },
      email: { total: 10, queued: 10, sending: 0, sent: 0, retry: 0, failed: 0, skipped_no_consent: 0, throughput_per_s: 0, paused_until: null },
    },
    errors: { rate_limited: sent },
    eta_seconds: 4,
    started_at: null,
    updated_at: '',
    ...extra,
  }
}

const ev = (n: number): ProgressEvent => ({
  at: `2026-11-08T10:00:${String(n).padStart(2, '0')}Z`,
  message_id: `m${n}`,
  channel: 'sms',
  attempt: 1,
  outcome: 'sent',
  http_status: 200,
  error_class: null,
})

describe('dashboard state (R-FLOW-5)', () => {
  it('replaces the whole progress on a snapshot, even when counts went down after a reconnect', () => {
    let s = applySnapshot(emptyDashboard(), snap(8))
    s = applySnapshot(s, snap(3, { errors: {} }))
    expect(s.progress?.channels.sms?.sent).toBe(3)
    expect(s.progress?.errors).toEqual({}) // nothing merged from the earlier snapshot
  })

  it('keeps recent events across snapshots, newest first, capped, without duplicates', () => {
    let s = emptyDashboard()
    for (let i = 0; i < MAX_RECENT_EVENTS + 5; i++) s = applyEvent(s, ev(i % 60))
    expect(s.events.length).toBeLessThanOrEqual(MAX_RECENT_EVENTS)
    s = applySnapshot(s, snap(1))
    expect(s.events[0]?.message_id).toBe(`m${(MAX_RECENT_EVENTS + 4) % 60}`)
    const again = applyEvent(s, s.events[0] as ProgressEvent)
    expect(again.events).toHaveLength(s.events.length)
  })

  it('computes progress fractions from terminal states', () => {
    const p = snap(5)
    expect(channelFraction(p.channels.sms!)).toBe(0.5)
    expect(overallFraction(p)).toBe(0.25)
    expect(overallFraction({ ...p, channels: {} })).toBe(0)
  })

  it('labels the error classes', () => {
    expect(errorLabel('rate_limited')).toContain('429')
    expect(errorLabel('server_error')).toContain('5xx')
    expect(errorLabel('weird_thing')).toBe('weird thing')
  })
})

describe('cancelled messages and per-variant rows (F4, B-2)', () => {
  it('counts cancelled as finished, so a stopped campaign reaches 100%', () => {
    const c = { ...snap(0).channels.sms!, total: 10, queued: 0, sent: 6, failed: 1, skipped_no_consent: 1, cancelled: 2 }
    expect(channelFraction(c)).toBe(1)
  })

  it('lists only the variants that have messages, in A, B order', () => {
    const v = { total: 5, sent: 4, failed: 1, cancelled: 0, skipped_no_consent: 0 }
    const c = { ...snap(0).channels.sms!, variants: { b: v, a: { ...v, total: 6 } } }
    expect(variantRows(c).map((r) => [r.key, r.v.total])).toEqual([['a', 6], ['b', 5]])
    expect(variantRows({ ...c, variants: { a: v } }).map((r) => r.key)).toEqual(['a'])
    expect(variantRows(snap(0).channels.sms!)).toEqual([])
  })
})

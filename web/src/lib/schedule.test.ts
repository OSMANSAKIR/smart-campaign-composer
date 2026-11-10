import { describe, expect, it } from 'vitest'
import {
  MAX_LEAD_MS,
  MIN_LEAD_MS,
  formatCountdown,
  parseLocalInput,
  scheduleBounds,
  scheduleProblem,
  toLocalInputValue,
  toOffsetISO,
} from './schedule'

const now = new Date(2026, 10, 10, 23, 30, 15) // local time

describe('schedule (F5)', () => {
  it('round-trips a datetime-local value as local time', () => {
    const d = new Date(2026, 10, 11, 9, 5)
    expect(toLocalInputValue(d)).toBe('2026-11-11T09:05')
    expect(parseLocalInput('2026-11-11T09:05')?.getTime()).toBe(d.getTime())
    expect(parseLocalInput('')).toBeNull()
    expect(parseLocalInput('garbage')).toBeNull()
  })

  it('formats an ISO 8601 string with the local offset, not Z', () => {
    const iso = toOffsetISO(new Date(2026, 10, 11, 9, 5, 0))
    expect(iso).toMatch(/^2026-11-11T09:05:00[+-]\d{2}:\d{2}$/)
    expect(new Date(iso).getTime()).toBe(new Date(2026, 10, 11, 9, 5, 0).getTime())
  })

  it('bounds are now + 1 minute and now + 7 days', () => {
    const b = scheduleBounds(now)
    expect(parseLocalInput(b.min)!.getTime()).toBe(new Date(2026, 10, 10, 23, 31, 0).getTime())
    expect(parseLocalInput(b.max)!.getTime()).toBe(new Date(2026, 10, 17, 23, 30, 0).getTime())
  })

  it.each([
    ['', /pick a date/i],
    [toLocalInputValue(new Date(now.getTime() + 30_000)), /at least one minute/i],
    [toLocalInputValue(new Date(now.getTime() + MAX_LEAD_MS + 3_600_000)), /7 days/i],
  ])('rejects %j', (v, msg) => {
    expect(scheduleProblem(v, now)).toMatch(msg)
  })

  it('accepts a time inside the window', () => {
    const v = toLocalInputValue(new Date(now.getTime() + MIN_LEAD_MS + 90_000))
    expect(scheduleProblem(v, now)).toBe('')
    expect(scheduleProblem(toLocalInputValue(new Date(now.getTime() + 2 * 3_600_000)), now)).toBe('')
  })

  it.each([
    [0, 'starting now'],
    [-5, 'starting now'],
    [9_000, 'in 00 min 09 s'],
    [125_000, 'in 02 min 05 s'],
    [3_725_000, 'in 1 h 02 min 05 s'],
    [90_061_000, 'in 1 d 1 h 01 min 01 s'],
  ])('countdown %d ms → %s', (ms, text) => {
    expect(formatCountdown(ms)).toBe(text)
  })
})

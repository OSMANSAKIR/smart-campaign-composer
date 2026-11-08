import { describe, expect, it } from 'vitest'
import { appendSample, linePoints, WINDOW_MS } from './throughput'
import type { Progress } from './types'

const snap = (at: number, sms: number, email: number) =>
  ({
    updated_at: new Date(at).toISOString(),
    channels: { sms: { throughput_per_s: sms }, email: { throughput_per_s: email } },
  }) as unknown as Progress

describe('throughput history (R-OBS-1)', () => {
  it('appends in time order, ignores repeated or older snapshots after a reconnect', () => {
    let h = appendSample([], snap(1_000, 10, 20))
    h = appendSample(h, snap(1_500, 50, 60))
    h = appendSample(h, snap(1_500, 99, 99)) // same snapshot again
    h = appendSample(h, snap(1_200, 99, 99)) // older
    expect(h.map((s) => [s.sms, s.email])).toEqual([
      [10, 20],
      [50, 60],
    ])
  })

  it('keeps only the last window', () => {
    let h = appendSample([], snap(0, 1, 1))
    h = appendSample(h, snap(WINDOW_MS + 1, 2, 2))
    expect(h).toHaveLength(1)
  })

  it('maps the newest sample to the right edge and clips above yMax', () => {
    let h = appendSample([], snap(10_000, 50, 0))
    h = appendSample(h, snap(10_000 + WINDOW_MS / 2, 150, 0))
    expect(linePoints(h, 'sms', 200, 100, 120)).toBe('100.0,58.3 200.0,0.0')
  })
})

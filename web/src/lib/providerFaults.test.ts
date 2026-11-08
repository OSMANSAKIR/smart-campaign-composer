import { describe, expect, it } from 'vitest'
import { fromPercent, PRESETS, toPercent } from './providerFaults'

describe('provider fault presets (R-RES-3)', () => {
  it('every preset is a valid provider config patch', () => {
    for (const p of PRESETS) {
      for (const k of ['spike_rate', 'error_rate', 'rate_429', 'hang_rate'] as const) {
        expect(p.patch[k]).toBeGreaterThanOrEqual(0)
        expect(p.patch[k]).toBeLessThanOrEqual(1)
      }
      expect(p.patch.latency_min_ms).toBeLessThanOrEqual(p.patch.latency_max_ms)
    }
  })

  it('percent conversion round-trips without float noise', () => {
    for (const f of [0, 0.005, 0.01, 0.02, 0.1, 0.333, 1]) expect(fromPercent(toPercent(f))).toBe(Math.round(f * 10000) / 10000)
    expect(toPercent(0.005)).toBe(0.5)
  })
})

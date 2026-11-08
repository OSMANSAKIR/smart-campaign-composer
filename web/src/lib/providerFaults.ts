// Presets for the mock provider's fault controls (System page). Rates are fractions 0..1; the form shows %.
import type { ProviderConfig } from './types'

export type FaultPatch = Pick<ProviderConfig, 'latency_min_ms' | 'latency_max_ms' | 'spike_rate' | 'error_rate' | 'rate_429' | 'hang_rate'>

export const PRESETS: { id: string; label: string; hint: string; patch: FaultPatch }[] = [
  {
    id: 'calm',
    label: 'Calm',
    hint: 'No errors, no 429s, no hangs',
    patch: { latency_min_ms: 20, latency_max_ms: 300, spike_rate: 0, error_rate: 0, rate_429: 0, hang_rate: 0 },
  },
  {
    id: 'default',
    label: 'Default',
    hint: 'The .env.example values: 2% 5xx, 1% 429, 0.5% hangs',
    patch: { latency_min_ms: 20, latency_max_ms: 300, spike_rate: 0.01, error_rate: 0.02, rate_429: 0.01, hang_rate: 0.005 },
  },
  {
    id: 'chaos',
    label: 'Chaos',
    hint: 'Stress S2: 10% 5xx, 5% 429, 2% hangs, 20–800 ms',
    patch: { latency_min_ms: 20, latency_max_ms: 800, spike_rate: 0.01, error_rate: 0.1, rate_429: 0.05, hang_rate: 0.02 },
  },
]

// toPercent/fromPercent convert between the form (percent, up to two decimals) and the API (fraction).
export const toPercent = (f: number) => Math.round(f * 10000) / 100
export const fromPercent = (p: number) => Math.round(p * 100) / 10000

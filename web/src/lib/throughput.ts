// Throughput history for the dashboard chart. Each snapshot carries the server's per-channel rate over the last
// 5 s (throughput_per_s) and its database time (updated_at); the chart keeps the last WINDOW_MS of them.
import type { Channel, Progress } from './types'

export const WINDOW_MS = 120_000
export const LIMIT_PER_S = 100 // R-SND-2, drawn as the reference line

export interface Sample {
  t: number // ms since epoch, from updated_at
  sms: number
  email: number
}

export function appendSample(history: Sample[], p: Progress): Sample[] {
  const t = Date.parse(p.updated_at)
  if (Number.isNaN(t)) return history
  const last = history[history.length - 1]
  if (last && t <= last.t) return history // a reconnect can repeat or reorder snapshots
  const s: Sample = { t, sms: p.channels.sms?.throughput_per_s ?? 0, email: p.channels.email?.throughput_per_s ?? 0 }
  return [...history, s].filter((x) => x.t >= t - WINDOW_MS)
}

// SVG polyline points for one channel in a w×h box: x spans the window ending at the newest sample, y spans
// 0..yMax (values above are clipped to the top).
export function linePoints(history: Sample[], ch: Channel, w: number, h: number, yMax: number): string {
  const end = history[history.length - 1]?.t ?? 0
  return history
    .map((s) => {
      const x = w - ((end - s.t) / WINDOW_MS) * w
      const y = h - (Math.min(s[ch], yMax) / yMax) * h
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

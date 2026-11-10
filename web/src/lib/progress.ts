// Dashboard state. Every `snapshot` is the complete truth (api.md §7, need.md N-ORC-006): it replaces the
// previous counts instead of being merged into them, which makes reconnects trivial (R-FLOW-5).
import type { Channel, ChannelProgress, Progress, ProgressEvent, VariantProgress } from './types'

export const MAX_RECENT_EVENTS = 40

export interface DashboardState {
  progress: Progress | null
  events: ProgressEvent[] // newest first
}

export function emptyDashboard(): DashboardState {
  return { progress: null, events: [] }
}

export function applySnapshot(state: DashboardState, snapshot: Progress): DashboardState {
  return { progress: snapshot, events: state.events }
}

export function applyEvent(state: DashboardState, e: ProgressEvent): DashboardState {
  // Events can be replayed after a reconnect; the same attempt is shown once.
  if (state.events.some((x) => x.message_id === e.message_id && x.attempt === e.attempt && x.at === e.at)) return state
  return { progress: state.progress, events: [e, ...state.events].slice(0, MAX_RECENT_EVENTS) }
}

// Messages in a terminal state (sent, failed, skipped, cancelled) divided by the total, as a 0..1 fraction.
// A stopped campaign therefore ends at 100%: after `done`, sent + failed + cancelled + skipped = total (api.md §9.5).
export function finishedCount(c: ChannelProgress): number {
  return c.sent + c.failed + c.skipped_no_consent + (c.cancelled ?? 0)
}

// Per-variant rows for a channel, in A, B order, only for variants that have messages (api.md §9.3).
export function variantRows(c: ChannelProgress): { key: 'a' | 'b'; v: VariantProgress }[] {
  const out: { key: 'a' | 'b'; v: VariantProgress }[] = []
  for (const key of ['a', 'b'] as const) {
    const v = c.variants?.[key]
    if (v) out.push({ key, v })
  }
  return out
}

export function channelFraction(c: ChannelProgress): number {
  return c.total > 0 ? finishedCount(c) / c.total : 0
}

export function overallFraction(p: Progress): number {
  let total = 0
  let done = 0
  for (const c of Object.values(p.channels)) {
    if (!c) continue
    total += c.total
    done += finishedCount(c)
  }
  return total > 0 ? done / total : 0
}

export function channelEntries(p: Progress): [Channel, ChannelProgress][] {
  const out: [Channel, ChannelProgress][] = []
  for (const ch of ['sms', 'email'] as const) {
    const c = p.channels[ch]
    if (c) out.push([ch, c])
  }
  return out
}

const ERROR_LABELS: Record<string, string> = {
  rate_limited: 'Rate limited (429)',
  server_error: 'Server error (5xx)',
  timeout: 'Timeout',
  permanent: 'Permanent failure (4xx)',
}

export function errorLabel(key: string): string {
  return ERROR_LABELS[key] ?? key.replace(/_/g, ' ')
}

export function formatEta(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return 'estimating…'
  if (seconds < 1) return 'less than a second'
  if (seconds < 60) return `about ${Math.round(seconds)} s`
  const m = Math.floor(seconds / 60)
  const s = Math.round(seconds % 60)
  return `about ${m} min ${s} s`
}

export function formatInt(n: number): string {
  return new Intl.NumberFormat('en-US').format(n)
}

export function formatCost(usd: number): string {
  if (usd === 0) return '$0'
  return usd < 0.01 ? `$${usd.toFixed(5)}` : `$${usd.toFixed(3)}`
}

export function formatTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('en-GB', { dateStyle: 'medium', timeStyle: 'short' })
}

export function formatClock(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString('en-GB')
}

export const STATUS_LABEL: Record<string, string> = {
  draft: 'Draft',
  segmented: 'Audience ready',
  content_ready: 'Ready to approve',
  scheduled: 'Scheduled',
  cancelled: 'Stopped',
  sending: 'Sending',
  completed: 'Completed',
  completed_with_failures: 'Completed with failures',
}

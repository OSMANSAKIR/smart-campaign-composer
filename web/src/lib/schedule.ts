// Scheduled send (F5, api.md §9.6): the marketer picks a local time; the API gets an ISO 8601 string with the
// local UTC offset. Window: now + 1 minute .. now + 7 days.
export const MIN_LEAD_MS = 60_000
export const MAX_LEAD_MS = 7 * 24 * 3_600_000

const p2 = (n: number) => String(n).padStart(2, '0')

// `<input type="datetime-local">` value for a Date, in the browser's local time (minutes precision).
export function toLocalInputValue(d: Date): string {
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}T${p2(d.getHours())}:${p2(d.getMinutes())}`
}

// Parses a datetime-local value as local time. Returns null for an empty or malformed value.
export function parseLocalInput(v: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(v)
  if (!m) return null
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]), 0, 0)
  return Number.isNaN(d.getTime()) ? null : d
}

// ISO 8601 with the local offset, e.g. 2026-11-11T09:30:00+03:00 (never "Z", so the intent stays readable).
export function toOffsetISO(d: Date): string {
  const off = -d.getTimezoneOffset()
  const sign = off >= 0 ? '+' : '-'
  const abs = Math.abs(off)
  return (
    `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}T${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}` +
    `${sign}${p2(Math.floor(abs / 60))}:${p2(abs % 60)}`
  )
}

export function scheduleBounds(now: Date): { min: string; max: string } {
  return {
    min: toLocalInputValue(new Date(now.getTime() + MIN_LEAD_MS)),
    max: toLocalInputValue(new Date(now.getTime() + MAX_LEAD_MS)),
  }
}

// '' when the time is allowed; otherwise a sentence for the marketer. The server stays the authority.
export function scheduleProblem(local: string, now: Date): string {
  const d = parseLocalInput(local)
  if (!d) return 'Pick a date and time to schedule the send.'
  const lead = d.getTime() - now.getTime()
  if (lead < MIN_LEAD_MS) return 'Pick a time at least one minute from now.'
  if (lead > MAX_LEAD_MS) return 'Pick a time within the next 7 days.'
  return ''
}

// "in 2 h 05 min 09 s"; "starting now" once the time has passed.
export function formatCountdown(ms: number): string {
  if (ms <= 0) return 'starting now'
  const total = Math.floor(ms / 1000)
  const d = Math.floor(total / 86400)
  const h = Math.floor((total % 86400) / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const parts: string[] = []
  if (d > 0) parts.push(`${d} d`)
  if (d > 0 || h > 0) parts.push(`${h} h`)
  parts.push(`${p2(m)} min`, `${p2(s)} s`)
  return `in ${parts.join(' ')}`
}

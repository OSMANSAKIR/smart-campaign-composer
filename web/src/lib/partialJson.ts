// Tolerant reader for the model's JSON while it is still streaming (display only; the `result` event is
// canonical). It parses as far as the text goes and never throws:
//  - an unfinished string yields what has arrived so far;
//  - an unfinished escape (`\`, `\u00`, a lone high surrogate) is dropped until the rest arrives, so a
//    character split across chunks never shows as garbage.

type Json = string | number | boolean | null | Json[] | { [k: string]: Json }

interface Cursor {
  s: string
  i: number
}

function skipWs(c: Cursor) {
  while (c.i < c.s.length && /\s/.test(c.s.charAt(c.i))) c.i++
}

const SIMPLE_ESCAPES: Record<string, string> = {
  '"': '"',
  '\\': '\\',
  '/': '/',
  b: '\b',
  f: '\f',
  n: '\n',
  r: '\r',
  t: '\t',
}

// Returns the decoded string and whether the closing quote was seen.
function readString(c: Cursor): { value: string; closed: boolean } {
  c.i++ // opening quote
  let out = ''
  while (c.i < c.s.length) {
    const ch = c.s.charAt(c.i)
    if (ch === '"') {
      c.i++
      return { value: out, closed: true }
    }
    if (ch !== '\\') {
      out += ch
      c.i++
      continue
    }
    const next = c.s.charAt(c.i + 1)
    if (c.i + 1 >= c.s.length) break // lone backslash at the end of the buffer
    if (next === 'u') {
      const hex = c.s.slice(c.i + 2, c.i + 6)
      if (hex.length < 4 || !/^[0-9a-fA-F]{4}$/.test(hex)) {
        // Incomplete (still arriving) or invalid: stop here, show nothing for this escape yet.
        if (hex.length < 4) break
        c.i += 2 // invalid escape: skip "\u" and keep going
        continue
      }
      out += String.fromCharCode(parseInt(hex, 16))
      c.i += 6
      continue
    }
    out += SIMPLE_ESCAPES[next] ?? next
    c.i += 2
  }
  // Unfinished: do not show half of a surrogate pair.
  if (out.length > 0) {
    const last = out.charCodeAt(out.length - 1)
    if (last >= 0xd800 && last <= 0xdbff) out = out.slice(0, -1)
  }
  return { value: out, closed: false }
}

function readValue(c: Cursor): Json | undefined {
  skipWs(c)
  const ch = c.s.charAt(c.i)
  if (ch === '"') return readString(c).value
  if (ch === '{') return readObject(c)
  if (ch === '[') return readArray(c)
  const m = /^(?:-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null)/.exec(c.s.slice(c.i))
  if (m) {
    c.i += m[0].length
    return JSON.parse(m[0]) as Json
  }
  return undefined
}

function readObject(c: Cursor): { [k: string]: Json } {
  const obj: { [k: string]: Json } = {}
  c.i++ // {
  for (;;) {
    skipWs(c)
    if (c.i >= c.s.length) return obj
    if (c.s.charAt(c.i) === ',') {
      c.i++
      continue
    }
    if (c.s.charAt(c.i) === '}') {
      c.i++
      return obj
    }
    if (c.s.charAt(c.i) !== '"') return obj // garbage: stop rather than loop
    const key = readString(c)
    if (!key.closed) return obj // never use a half-received key
    skipWs(c)
    if (c.s.charAt(c.i) !== ':') return obj
    c.i++
    const v = readValue(c)
    if (v === undefined) return obj
    obj[key.value] = v
  }
}

function readArray(c: Cursor): Json[] {
  const arr: Json[] = []
  c.i++ // [
  for (;;) {
    skipWs(c)
    if (c.i >= c.s.length) return arr
    const ch = c.s.charAt(c.i)
    if (ch === ',') {
      c.i++
      continue
    }
    if (ch === ']') {
      c.i++
      return arr
    }
    const v = readValue(c)
    if (v === undefined) return arr
    arr.push(v)
  }
}

export function parsePartialJson(text: string): Json | undefined {
  const c: Cursor = { s: text, i: 0 }
  return readValue(c)
}

export interface StreamedContent {
  smsText: string
  emailSubject: string
  emailBody: string
}

function str(o: Json | undefined, ...path: string[]): string {
  let cur: Json | undefined = o
  for (const k of path) {
    if (cur === null || typeof cur !== 'object' || Array.isArray(cur)) return ''
    cur = cur[k]
  }
  return typeof cur === 'string' ? cur : ''
}

export interface StreamedVariant extends StreamedContent {
  label: string
}

export interface StreamedVariants {
  a: StreamedVariant | null
  b: StreamedVariant | null
}

function variantAt(v: Json | undefined, ...path: string[]): StreamedVariant | null {
  let cur: Json | undefined = v
  for (const k of path) {
    if (cur === null || typeof cur !== 'object' || Array.isArray(cur)) return null
    cur = cur[k]
  }
  if (cur === null || typeof cur !== 'object' || Array.isArray(cur)) return null
  return {
    label: str(cur, 'label'),
    smsText: str(cur, 'sms', 'text'),
    emailSubject: str(cur, 'email', 'subject'),
    emailBody: str(cur, 'email', 'body'),
  }
}

// Extracts variants.a / variants.b from a content_v2 proposal (api.md §9.3) as it streams. A key that has not
// started yet is null, so the caller can keep showing an empty card. A refine answer (refine_v1, api.md §9.4)
// has no `variants`; it reads as the single object it is, returned as `a`.
export function extractVariants(partial: string): StreamedVariants {
  const v = parsePartialJson(partial)
  if (v !== null && v !== undefined && typeof v === 'object' && !Array.isArray(v) && 'variants' in v) {
    return { a: variantAt(v, 'variants', 'a'), b: variantAt(v, 'variants', 'b') }
  }
  return { a: variantAt(v), b: null }
}

// Extracts sms.text, email.subject and email.body from the (possibly incomplete) proposal JSON.
export function extractContent(partial: string): StreamedContent {
  const v = parsePartialJson(partial)
  return {
    smsText: str(v, 'sms', 'text'),
    emailSubject: str(v, 'email', 'subject'),
    emailBody: str(v, 'email', 'body'),
  }
}

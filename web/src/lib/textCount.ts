// Character counting shared with the server (docs/contracts/testvectors/text_length.json).
// A character is a Unicode code point; encoding and segments are display-only (need.md N-PLAN-005).

export interface TextCounts {
  runes: number
  utf16Units: number
  encoding: 'gsm7' | 'ucs2'
  gsm7Septets: number
  segments: number
}

// GSM 03.38 basic table (Unicode GSM0338 mapping, escape excluded) and extension table (2 septets each).
const GSM_BASIC = new Set(
  '@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !"#¤%&\'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà',
)
const GSM_EXTENSION = new Set('\f^{}\\[~]|€')

function segments(n: number, single: number, part: number): number {
  return n <= single ? 1 : Math.ceil(n / part)
}

export function countText(s: string): TextCounts {
  const codePoints = [...s]
  let septets = 0
  let gsm = true
  for (const c of codePoints) {
    if (GSM_BASIC.has(c)) septets += 1
    else if (GSM_EXTENSION.has(c)) septets += 2
    else gsm = false
  }
  const counts: TextCounts = {
    runes: codePoints.length,
    utf16Units: s.length,
    encoding: gsm ? 'gsm7' : 'ucs2',
    gsm7Septets: gsm ? septets : 0,
    segments: 0,
  }
  if (counts.runes > 0) {
    counts.segments = gsm ? segments(septets, 160, 153) : segments(counts.utf16Units, 70, 67)
  }
  return counts
}

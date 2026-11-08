import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { countText } from './textCount'

interface Vector {
  name: string
  input: string
  repeat?: number
  runes: number
  utf16_units: number
  encoding: 'gsm7' | 'ucs2'
  gsm7_septets: number | null
  segments: number
}

const file = JSON.parse(
  readFileSync(new URL('../../../docs/contracts/testvectors/text_length.json', import.meta.url), 'utf8'),
) as { vectors: Vector[] }

describe('countText matches the shared vectors (R-CNT-1)', () => {
  it.each(file.vectors.map((v) => [v.name, v] as const))('%s', (_name, v) => {
    expect(countText(v.input.repeat(v.repeat ?? 1))).toEqual({
      runes: v.runes,
      utf16Units: v.utf16_units,
      encoding: v.encoding,
      gsm7Septets: v.gsm7_septets ?? 0,
      segments: v.segments,
    })
  })
})

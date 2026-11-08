import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { parseTemplate, renderPreview } from './templateVars'

interface ParseVector {
  name: string
  template: string
  variables: string[]
  errors: string[]
}

const file = JSON.parse(
  readFileSync(new URL('../../../docs/contracts/testvectors/template_vars.json', import.meta.url), 'utf8'),
) as { parse: ParseVector[] }

describe('parseTemplate matches the shared vectors (R-CNT-3)', () => {
  it.each(file.parse.map((v) => [v.name, v] as const))('%s', (_name, v) => {
    const got = parseTemplate(v.template)
    expect(got.variables).toEqual(v.variables)
    expect([...got.errors].sort()).toEqual([...v.errors].sort())
  })
})

describe('renderPreview is single-pass (R-CNT-3)', () => {
  it('does not re-scan substituted values', () => {
    expect(renderPreview('Hi {{first_name}}', { first_name: '{{city}}', city: 'Ankara' })).toBe('Hi {{city}}')
  })
  it('handles inner spaces', () => {
    expect(renderPreview('{{ city }} mağazası', { city: 'İzmir' })).toBe('İzmir mağazası')
  })
})

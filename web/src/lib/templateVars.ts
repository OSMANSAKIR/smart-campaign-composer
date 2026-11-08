// Client mirror of the personalization grammar (docs/contracts/testvectors/template_vars.json, R-CNT-3).
// UX only: the server's validation report is authoritative.

export const ALLOWED_VARIABLES = ['first_name', 'city'] as const

export type TemplateErrorCode = 'unknown_variable' | 'malformed_variable'

export interface ParsedTemplate {
  variables: string[]
  errors: TemplateErrorCode[]
}

// A variable is `{{`, optional whitespace, a name without braces or whitespace, optional whitespace, `}}`.
const VARIABLE = /\{\{\s*([^{}\s]+)\s*\}\}/g

export function parseTemplate(template: string): ParsedTemplate {
  const variables: string[] = []
  let unknown = false
  for (const m of template.matchAll(VARIABLE)) {
    const name = m[1] ?? ''
    if ((ALLOWED_VARIABLES as readonly string[]).includes(name)) {
      if (!variables.includes(name)) variables.push(name)
    } else {
      unknown = true
    }
  }
  // Braces are reserved for variables: whatever remains after removing well-formed ones is malformed.
  const malformed = /[{}]/.test(template.replace(VARIABLE, ''))
  const errors: TemplateErrorCode[] = []
  if (unknown) errors.push('unknown_variable')
  if (malformed) errors.push('malformed_variable')
  return { variables, errors }
}

// Single-pass substitution for the preview: values are never re-scanned.
export function renderPreview(template: string, values: Record<string, string>): string {
  return template.replace(VARIABLE, (whole, name: string) => values[name] ?? whole)
}

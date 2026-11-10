// Pure logic behind the segment-rule editor (F2, docs/contracts/segment-dsl.md, api.md §9.1/§9.2).
// Everything the editor offers (fields, operators, ranges, enum values, limits) comes from the catalog; the
// server validator stays the authority, so a bad value here only shows the server's error for that condition.
import type { Catalog, CatalogField, CatalogLimits, Language, RuleCondition, RuleValue, SegmentRule } from './types'

// How a condition's value is entered.
export type ValueKind = 'number' | 'range' | 'enum' | 'enum_list' | 'days' | 'date' | 'none'

// Plain-words operator labels (the catalog lists operator names only).
export const OP_LABEL: Record<string, string> = {
  eq: 'is',
  neq: 'is not',
  gt: 'is greater than',
  gte: 'is at least',
  lt: 'is less than',
  lte: 'is at most',
  between: 'is between',
  in: 'is any of',
  not_in: 'is none of',
  is_true: 'is true',
  is_false: 'is false',
  within_last_days: 'happened within the last (days)',
  not_within_last_days: 'did not happen within the last (days, never counts)',
  before: 'is before',
  after: 'is after',
  is_null: 'has never happened',
  is_not_null: 'has happened at least once',
}

export function opLabel(op: string): string {
  return OP_LABEL[op] ?? op.replace(/_/g, ' ')
}

export function fieldOf(catalog: Catalog, name: string): CatalogField | undefined {
  return catalog.fields.find((f) => f.name === name)
}

// Operators offered for a field: only the ones the catalog allows for its type.
export function opsFor(field: CatalogField): string[] {
  return [...field.ops]
}

export function valueKind(field: CatalogField, op: string): ValueKind {
  switch (op) {
    case 'is_true':
    case 'is_false':
    case 'is_null':
    case 'is_not_null':
      return 'none'
    case 'between':
      return 'range'
    case 'in':
    case 'not_in':
      return 'enum_list'
    case 'within_last_days':
    case 'not_within_last_days':
      return 'days'
    case 'before':
    case 'after':
      return 'date'
    default:
      return field.type === 'enum' ? 'enum' : 'number'
  }
}

export function labelOf(field: CatalogField | undefined, name: string, lang: Language): string {
  return field?.label[lang] || field?.label.en || name
}

const today = () => new Date().toISOString().slice(0, 10)

export function defaultValue(field: CatalogField, op: string): RuleValue {
  switch (valueKind(field, op)) {
    case 'none':
      return null
    case 'number':
      return field.min ?? 0
    case 'range':
      return [field.min ?? 0, field.max ?? field.min ?? 0]
    case 'enum':
      return field.values?.[0] ?? ''
    case 'enum_list':
      return field.values?.[0] ? [field.values[0]] : []
    case 'days':
      return 30
    case 'date':
      return today()
  }
}

export function newCondition(field: CatalogField): RuleCondition {
  const op = field.ops[0] ?? 'eq'
  return { field: field.name, op, value: defaultValue(field, op) }
}

// Changing the field resets operator and value; changing the operator keeps the value when it still fits.
export function changeField(catalog: Catalog, c: RuleCondition, name: string): RuleCondition {
  const f = fieldOf(catalog, name)
  return f ? newCondition(f) : c
}

export function changeOp(catalog: Catalog, c: RuleCondition, op: string): RuleCondition {
  const f = fieldOf(catalog, c.field)
  if (!f) return { ...c, op }
  return valueKind(f, c.op) === valueKind(f, op) ? { ...c, op } : { ...c, op, value: defaultValue(f, op) }
}

export function cloneRule(rule: SegmentRule | null | undefined): SegmentRule {
  return {
    groups: (rule?.groups ?? []).map((g) => ({
      conditions: g.conditions.map((c) => ({
        field: c.field,
        op: c.op,
        value: Array.isArray(c.value) ? [...c.value] : c.value,
      })),
    })),
  }
}

export function conditionCount(rule: SegmentRule): number {
  return rule.groups.reduce((n, g) => n + g.conditions.length, 0)
}

export function canAddGroup(rule: SegmentRule, l: CatalogLimits): boolean {
  return rule.groups.length < l.max_groups && conditionCount(rule) < l.max_conditions_total
}

export function canAddCondition(rule: SegmentRule, group: number, l: CatalogLimits): boolean {
  const g = rule.groups[group]
  return !!g && g.conditions.length < l.max_conditions_per_group && conditionCount(rule) < l.max_conditions_total
}

export function addGroup(rule: SegmentRule, catalog: Catalog): SegmentRule {
  const first = catalog.fields[0]
  if (!first || !canAddGroup(rule, catalog.limits)) return rule
  return { groups: [...rule.groups, { conditions: [newCondition(first)] }] }
}

export function addCondition(rule: SegmentRule, group: number, catalog: Catalog): SegmentRule {
  const first = catalog.fields[0]
  if (!first || !canAddCondition(rule, group, catalog.limits)) return rule
  const next = cloneRule(rule)
  next.groups[group]!.conditions.push(newCondition(first))
  return next
}

// An emptied group is dropped (a group needs at least one condition: `empty_group`).
export function removeCondition(rule: SegmentRule, group: number, index: number): SegmentRule {
  const next = cloneRule(rule)
  const g = next.groups[group]
  if (!g) return rule
  g.conditions.splice(index, 1)
  if (g.conditions.length === 0) next.groups.splice(group, 1)
  return next
}

export function removeGroup(rule: SegmentRule, group: number): SegmentRule {
  const next = cloneRule(rule)
  next.groups.splice(group, 1)
  return next
}

// Where a server issue belongs: `groups[1].conditions[0].value` (optionally prefixed by `rule.`/`segment.`).
export function issueLocation(path: string): { group: number; condition: number | null } | null {
  const m = /groups\[(\d+)\](?:\.conditions\[(\d+)\])?/.exec(path)
  if (!m) return null
  return { group: Number(m[1]), condition: m[2] === undefined ? null : Number(m[2]) }
}

export function sameRule(a: SegmentRule, b: SegmentRule): boolean {
  return JSON.stringify(cloneRule(a)) === JSON.stringify(cloneRule(b))
}

function valueText(v: RuleValue): string {
  if (Array.isArray(v)) return v.map((x) => String(x)).join(', ')
  return v === null ? '' : String(v)
}

// Client-side rendering for Claude's original rule: the server only renders the current rule.
export function renderCondition(c: RuleCondition, catalog: Catalog, lang: Language): string {
  const f = fieldOf(catalog, c.field)
  const name = labelOf(f, c.field, lang)
  const op = opLabel(c.op)
  switch (f ? valueKind(f, c.op) : 'none') {
    case 'none':
      return `${name} ${op}`
    case 'range': {
      const [lo, hi] = Array.isArray(c.value) ? c.value : [c.value, null]
      return `${name} ${op} ${valueText(lo ?? null)} and ${valueText(hi ?? null)}`
    }
    case 'days':
      return `${name} ${op} ${valueText(c.value)}`
    default:
      return `${name} ${op} ${valueText(c.value)}`
  }
}

// One line per group; conditions inside a group are ORed, groups are ANDed (segment-dsl.md §1).
export function renderRule(rule: SegmentRule, catalog: Catalog, lang: Language): string[] {
  return rule.groups.map((g) => g.conditions.map((c) => renderCondition(c, catalog, lang)).join(lang === 'tr' ? ' veya ' : ' or '))
}

// Trailing-edge debounce (F2: the preview call waits 300 ms after the last edit).
export function debounce<A extends unknown[]>(fn: (...args: A) => void, ms: number) {
  let timer: ReturnType<typeof setTimeout> | undefined
  return {
    call(...args: A) {
      clearTimeout(timer)
      timer = setTimeout(() => fn(...args), ms)
    },
    cancel() {
      clearTimeout(timer)
    },
  }
}

export const PREVIEW_DEBOUNCE_MS = 300

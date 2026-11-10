import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  addCondition,
  addGroup,
  canAddCondition,
  canAddGroup,
  changeField,
  changeOp,
  debounce,
  defaultValue,
  fieldOf,
  issueLocation,
  newCondition,
  opsFor,
  removeCondition,
  renderRule,
  valueKind,
} from './ruleEditor'
import type { Catalog, SegmentRule } from './types'

export const CATALOG: Catalog = {
  fields: [
    { name: 'city', type: 'enum', label: { en: 'City', tr: 'Şehir' }, values: ['İstanbul', 'Ankara', 'İzmir'], ops: ['eq', 'neq', 'in', 'not_in'] },
    { name: 'age', type: 'number', label: { en: 'Age', tr: 'Yaş' }, min: 18, max: 100, integer: true, ops: ['eq', 'neq', 'gt', 'gte', 'lt', 'lte', 'between'] },
    { name: 'cart_abandoned', type: 'bool', label: { en: 'Abandoned cart', tr: 'Terk edilmiş sepet' }, ops: ['is_true', 'is_false'] },
    {
      name: 'last_purchase_at',
      type: 'date',
      nullable: true,
      label: { en: 'Last purchase', tr: 'Son alışveriş' },
      ops: ['within_last_days', 'not_within_last_days', 'before', 'after', 'is_null', 'is_not_null'],
    },
  ],
  limits: {
    max_groups: 2,
    max_conditions_per_group: 2,
    max_conditions_total: 3,
    max_list_items: 50,
    min_days: 1,
    max_days: 3650,
    max_string_runes: 100,
    min_date: '2000-01-01',
    max_date: '2100-12-31',
  },
}

const f = (name: string) => fieldOf(CATALOG, name)!

describe('rule editor: operators per field type (F2)', () => {
  it.each([
    ['city', ['eq', 'neq', 'in', 'not_in']],
    ['age', ['eq', 'neq', 'gt', 'gte', 'lt', 'lte', 'between']],
    ['cart_abandoned', ['is_true', 'is_false']],
    ['last_purchase_at', ['within_last_days', 'not_within_last_days', 'before', 'after', 'is_null', 'is_not_null']],
  ])('%s offers only the catalog operators', (name, ops) => {
    expect(opsFor(f(name))).toEqual(ops)
  })
})

describe('rule editor: value shape per operator (segment-dsl.md §3)', () => {
  it.each([
    ['age', 'eq', 'number', 18],
    ['age', 'between', 'range', [18, 100]],
    ['city', 'eq', 'enum', 'İstanbul'],
    ['city', 'in', 'enum_list', ['İstanbul']],
    ['city', 'not_in', 'enum_list', ['İstanbul']],
    ['cart_abandoned', 'is_true', 'none', null],
    ['last_purchase_at', 'within_last_days', 'days', 30],
    ['last_purchase_at', 'not_within_last_days', 'days', 30],
    ['last_purchase_at', 'is_null', 'none', null],
    ['last_purchase_at', 'is_not_null', 'none', null],
  ] as const)('%s %s is entered as %s and starts as %j', (name, op, kind, value) => {
    expect(valueKind(f(name), op)).toBe(kind)
    expect(defaultValue(f(name), op)).toEqual(value)
  })

  it('a date operator before/after starts as a YYYY-MM-DD string', () => {
    expect(valueKind(f('last_purchase_at'), 'before')).toBe('date')
    expect(defaultValue(f('last_purchase_at'), 'after')).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  })

  it('changing the operator keeps a value that still fits and resets one that does not', () => {
    const c = { field: 'age', op: 'gt', value: 30 }
    expect(changeOp(CATALOG, c, 'lte').value).toBe(30)
    expect(changeOp(CATALOG, c, 'between').value).toEqual([18, 100])
    const d = { field: 'city', op: 'eq', value: 'Ankara' }
    expect(changeOp(CATALOG, d, 'neq').value).toBe('Ankara')
    expect(changeOp(CATALOG, d, 'in').value).toEqual(['İstanbul'])
  })

  it('changing the field starts over with that field’s first operator', () => {
    const c = changeField(CATALOG, { field: 'age', op: 'gt', value: 30 }, 'cart_abandoned')
    expect(c).toEqual({ field: 'cart_abandoned', op: 'is_true', value: null })
    expect(newCondition(f('city'))).toEqual({ field: 'city', op: 'eq', value: 'İstanbul' })
  })
})

describe('rule editor: group and condition limits come from the catalog (F2)', () => {
  const one: SegmentRule = { groups: [{ conditions: [{ field: 'city', op: 'eq', value: 'Ankara' }] }] }

  it('adds groups and conditions up to the limits, never beyond', () => {
    let r = one
    expect(canAddGroup(r, CATALOG.limits)).toBe(true)
    r = addGroup(r, CATALOG)
    expect(r.groups).toHaveLength(2)
    expect(canAddGroup(r, CATALOG.limits)).toBe(false) // max_groups = 2
    expect(addGroup(r, CATALOG)).toBe(r)
    r = addCondition(r, 0, CATALOG)
    expect(r.groups[0]!.conditions).toHaveLength(2)
    expect(canAddCondition(r, 0, CATALOG.limits)).toBe(false) // per group and total (3) reached
    expect(canAddCondition(r, 1, CATALOG.limits)).toBe(false) // total limit
    expect(addCondition(r, 1, CATALOG)).toBe(r)
  })

  it('removing the last condition removes its group', () => {
    const r = removeCondition(one, 0, 0)
    expect(r.groups).toEqual([])
  })

  it('does not mutate the input', () => {
    const before = JSON.stringify(one)
    addCondition(one, 0, CATALOG)
    removeCondition(one, 0, 0)
    expect(JSON.stringify(one)).toBe(before)
  })
})

describe('rule editor: rendering and issue paths', () => {
  it('renders Claude’s original rule from catalog labels (AND between groups, OR inside)', () => {
    const rule: SegmentRule = {
      groups: [
        {
          conditions: [
            { field: 'city', op: 'in', value: ['İstanbul', 'Ankara'] },
            { field: 'age', op: 'between', value: [25, 40] },
          ],
        },
        { conditions: [{ field: 'cart_abandoned', op: 'is_true', value: null }] },
      ],
    }
    expect(renderRule(rule, CATALOG, 'tr')).toEqual([
      'Şehir is any of İstanbul, Ankara veya Yaş is between 25 and 40',
      'Terk edilmiş sepet is true',
    ])
    expect(renderRule(rule, CATALOG, 'en')[0]).toContain('City is any of İstanbul, Ankara or Age')
  })

  it.each([
    ['groups[1].conditions[0].value', { group: 1, condition: 0 }],
    ['segment.groups[0].conditions[2].op', { group: 0, condition: 2 }],
    ['rule.groups[3]', { group: 3, condition: null }],
    ['segment', null],
  ])('issueLocation(%s)', (path, want) => {
    expect(issueLocation(path)).toEqual(want)
  })
})

describe('rule editor: preview debounce (F2, 300 ms)', () => {
  afterEach(() => vi.useRealTimers())

  it('calls once, 300 ms after the last edit', () => {
    vi.useFakeTimers()
    const fn = vi.fn()
    const d = debounce(fn, 300)
    d.call('a')
    vi.advanceTimersByTime(299)
    d.call('b')
    vi.advanceTimersByTime(299)
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith('b')
  })

  it('cancel drops the pending call', () => {
    vi.useFakeTimers()
    const fn = vi.fn()
    const d = debounce(fn, 300)
    d.call()
    d.cancel()
    vi.advanceTimersByTime(1000)
    expect(fn).not.toHaveBeenCalled()
  })
})

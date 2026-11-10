// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { CATALOG } from '../lib/catalogFixture'
import type { SegmentPreview, SegmentRule } from '../lib/types'
import RuleEditor from './RuleEditor.vue'

const RULE: SegmentRule = { groups: [{ conditions: [{ field: 'city', op: 'eq', value: 'İstanbul' }] }] }

const okPreview: SegmentPreview = {
  valid: true,
  errors: [],
  warnings: [],
  rendered: { en: ['City is İstanbul'], tr: ['Şehir: İstanbul'] },
  audience: { total: 12, sms_reachable: 7, email_reachable: 9, both: 4, counted_at: '' },
}

function stubFetch(answer: () => SegmentPreview) {
  const f = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(answer()), { status: 200 })))
  vi.stubGlobal('fetch', f)
  return f
}

const settle = () => vi.advanceTimersByTimeAsync(0)

function mountEditor() {
  return mount(RuleEditor, {
    props: { catalog: CATALOG, rule: RULE, language: 'tr', campaignId: 'c1', saving: false },
  })
}

describe('RuleEditor (F2)', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('previews the loaded rule at once, then debounces edits by 300 ms', async () => {
    const f = stubFetch(() => okPreview)
    const w = mountEditor()
    await settle()
    expect(f).toHaveBeenCalledTimes(1)
    expect(w.get('[data-testid="preview-count"]').text()).toContain('12')

    // Two quick edits become one preview call, 300 ms after the last one.
    await w.get('select[aria-label="Condition 1 of group 1: value"]').setValue('Ankara')
    await vi.advanceTimersByTimeAsync(200)
    await w.get('select[aria-label="Condition 1 of group 1: value"]').setValue('İzmir')
    await vi.advanceTimersByTimeAsync(299)
    expect(f).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(f).toHaveBeenCalledTimes(2)
    const body = JSON.parse((f.mock.calls[1] as [string, RequestInit])[1].body as string)
    expect(body).toEqual({ rule: { groups: [{ conditions: [{ field: 'city', op: 'eq', value: 'İzmir' }] }] }, campaign_id: 'c1' })
    w.unmount()
  })

  it('offers only the field’s operators and a value input that fits the operator', async () => {
    stubFetch(() => okPreview)
    const w = mountEditor()
    await settle()
    const ops = w.findAll('select[aria-label="Condition 1 of group 1: operator"] option').map((o) => o.attributes('value'))
    expect(ops).toEqual(['eq', 'neq', 'in', 'not_in'])

    await w.get('select[aria-label="Condition 1 of group 1: operator"]').setValue('in')
    // "is any of" is a multi-select (checkboxes), not a single select.
    expect(w.find('select[aria-label="Condition 1 of group 1: value"]').exists()).toBe(false)
    expect(w.findAll('fieldset[aria-label="Condition 1 of group 1: values"] input[type="checkbox"]')).toHaveLength(3)

    await w.get('select[aria-label="Condition 1 of group 1: field"]').setValue('age')
    await w.get('select[aria-label="Condition 1 of group 1: operator"]').setValue('between')
    expect(w.find('input[aria-label="Condition 1 of group 1: lower bound"]').exists()).toBe(true)
    expect(w.get('input[aria-label="Condition 1 of group 1: upper bound"]').attributes('max')).toBe('100')
    w.unmount()
  })

  it('shows the server error under the condition it belongs to and blocks saving', async () => {
    const bad: SegmentPreview = {
      valid: false,
      errors: [{ path: 'groups[0].conditions[0].value', code: 'invalid_enum_value', message: 'city must be one of: İstanbul, Ankara' }],
      warnings: [{ path: 'segment', code: 'targets_everyone', message: 'This targets everyone.' }],
      rendered: { en: [], tr: [] },
      audience: null,
    }
    stubFetch(() => bad)
    const w = mountEditor()
    await settle()
    expect(w.get('.cond').text()).toContain('city must be one of: İstanbul, Ankara')
    expect(w.text()).toContain('This targets everyone.')
    const save = w.findAll('button').find((b) => b.text() === 'Save rule')!
    expect(save.attributes('disabled')).toBeDefined()
    w.unmount()
  })

  it('saves an edited, valid rule', async () => {
    stubFetch(() => okPreview)
    const w = mountEditor()
    await settle()
    await w.get('select[aria-label="Condition 1 of group 1: value"]').setValue('Ankara')
    await vi.advanceTimersByTimeAsync(300)
    const save = w.findAll('button').find((b) => b.text() === 'Save rule')!
    expect(save.attributes('disabled')).toBeUndefined()
    await save.trigger('click')
    await nextTick()
    expect(w.emitted('save')?.[0]?.[0]).toEqual({ groups: [{ conditions: [{ field: 'city', op: 'eq', value: 'Ankara' }] }] })
    w.unmount()
  })

  it('shows a network failure instead of a stale preview', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    const w = mountEditor()
    await settle()
    expect(w.text()).toContain('did not get through')
    w.unmount()
  })
})

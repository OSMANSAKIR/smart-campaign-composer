// A small catalog in the shape of GET /api/catalog (api.md §9.1), shared by the rule-editor tests.
import type { Catalog } from './types'

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

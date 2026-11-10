// @vitest-environment jsdom
// Campaign list: a scheduled campaign shows its send time in the browser's local time (B-1).
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { formatTime } from '../lib/format'
import type { CampaignSummary } from '../lib/types'
import CampaignListView from './CampaignListView.vue'

function item(id: string, status: CampaignSummary['status'], scheduled_at: string | null): CampaignSummary {
  return {
    id, brief: `Brief ${id}`, status, language: 'tr', channels: ['sms'], audience_total: 10,
    created_at: '2026-11-10T20:00:00Z', approved_at: null, scheduled_at, completed_at: null,
  }
}

afterEach(() => vi.unstubAllGlobals())

describe('CampaignListView', () => {
  it('shows "Scheduled for <local time>" on scheduled campaigns only (B-1)', async () => {
    const at = '2099-01-01T09:30:00Z'
    const page = { items: [item('a', 'scheduled', at), item('b', 'draft', null), item('c', 'sending', at)], next_cursor: null }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(page), { status: 200 })))
    const w = mount(CampaignListView)
    await flushPromises()
    const rows = w.findAll('tbody tr')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.get('[data-testid="scheduled-at"]').text()).toBe(`Scheduled for ${formatTime(at)}`)
    expect(rows[1]!.find('[data-testid="scheduled-at"]').exists()).toBe(false)
    expect(rows[2]!.find('[data-testid="scheduled-at"]').exists()).toBe(false) // already sending: no longer "scheduled for"
    w.unmount()
  })
})

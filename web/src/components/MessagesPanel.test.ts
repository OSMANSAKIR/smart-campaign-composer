// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import MessagesPanel from './MessagesPanel.vue'

const page = {
  items: [
    { id: 'm1', user_id: 7, channel: 'sms', status: 'sent', attempts: 1, last_error: null, sent_at: '2026-11-10T20:00:00Z', updated_at: '', variant: 'a' },
    { id: 'm2', user_id: 8, channel: 'email', status: 'cancelled', attempts: 0, last_error: null, sent_at: null, updated_at: '', variant: 'b' },
  ],
  next_cursor: null,
}

describe('MessagesPanel (B-2: the messages list shows the variant)', () => {
  afterEach(() => vi.unstubAllGlobals())

  async function open(hasVariants: boolean) {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify(page), { status: 200 }))
    vi.stubGlobal('fetch', f)
    const w = mount(MessagesPanel, { props: { campaignId: 'c1', hasVariants } })
    const d = w.get('details')
    ;(d.element as HTMLDetailsElement).open = true
    await d.trigger('toggle')
    await flushPromises()
    return { w, f }
  }

  it('loads on open and shows a Variant column for A/B campaigns', async () => {
    const { w, f } = await open(true)
    expect((f.mock.calls[0] as [string])[0]).toBe('/api/campaigns/c1/messages?limit=50')
    const rows = w.findAll('tbody tr').map((r) => r.findAll('td').map((c) => c.text()))
    expect(w.findAll('thead th').map((h) => h.text())).toContain('Variant')
    expect(rows[0]).toEqual(['7', 'SMS', 'A', 'sent', '1', expect.any(String), ''])
    expect(rows[1]!.slice(0, 4)).toEqual(['8', 'Email', 'B', 'cancelled'])
  })

  it('hides the Variant column when the campaign has no variants', async () => {
    const { w } = await open(false)
    expect(w.findAll('thead th').map((h) => h.text())).not.toContain('Variant')
  })
})

// @vitest-environment jsdom
// Dashboard behaviour added in phase 2: per-variant delivery (B-2), scheduled countdown (F5), stop sending (F4).
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Campaign, Progress } from '../lib/types'
import DashboardView from './DashboardView.vue'

class FakeEventSource {
  static last: FakeEventSource | null = null
  readyState = 1
  onopen: ((e: Event) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  listeners: Record<string, ((e: MessageEvent<string>) => void)[]> = {}
  constructor(public url: string) {
    FakeEventSource.last = this
  }
  addEventListener(type: string, l: (e: MessageEvent<string>) => void) {
    ;(this.listeners[type] ??= []).push(l)
  }
  close() {}
  emit(type: string, data: unknown) {
    for (const l of this.listeners[type] ?? []) l({ data: JSON.stringify(data) } as MessageEvent<string>)
  }
}

const NOW = new Date('2026-11-10T20:00:00Z')

function campaign(status: Campaign['status'], over: Partial<Campaign> = {}): Campaign {
  return {
    id: 'c1', brief: 'Brief', status, language: 'tr', segment: null, content: null, channels: ['sms', 'email'],
    approved_content_version: 1, send_variant: 'ab', scheduled_at: null, cancelled_at: null,
    created_at: '', approved_at: '', completed_at: null, ...over,
  }
}

function progress(status: Progress['status'], over: Partial<Progress['channels']['sms'] & object> = {}): Progress {
  const v = { total: 5, sent: 4, failed: 1, cancelled: 0, skipped_no_consent: 0 }
  return {
    campaign_id: 'c1', status,
    channels: {
      sms: {
        total: 10, queued: 0, sending: 0, sent: 8, retry: 0, failed: 2, skipped_no_consent: 0, cancelled: 0,
        throughput_per_s: 0, paused_until: null, variants: { a: v, b: { ...v, sent: 4, failed: 1 } }, ...over,
      },
    },
    errors: {}, eta_seconds: null, started_at: null, updated_at: NOW.toISOString(),
  }
}

let calls: { url: string; method: string }[]
function stub(get: () => Campaign, cancelResult?: { campaign: Campaign; cancelled: number }) {
  calls = []
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init: RequestInit) => {
    calls.push({ url, method: init.method ?? 'GET' })
    if (url.endsWith('/cancel')) return Promise.resolve(new Response(JSON.stringify(cancelResult), { status: 200 }))
    if (url.includes('/messages')) return Promise.resolve(new Response(JSON.stringify({ items: [], next_cursor: null }), { status: 200 }))
    return Promise.resolve(new Response(JSON.stringify(get()), { status: 200 }))
  }))
}
const settle = async () => {
  for (let i = 0; i < 6; i++) await vi.advanceTimersByTimeAsync(0)
}

describe('DashboardView phase 2', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    vi.stubGlobal('EventSource', FakeEventSource)
    // jsdom has no <dialog> modal support.
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('shows per-variant sent and failed per channel (B-2)', async () => {
    stub(() => campaign('completed'))
    const w = mount(DashboardView, { props: { campaignId: 'c1' } })
    await settle()
    FakeEventSource.last!.emit('snapshot', progress('completed'))
    await settle()
    const rows = w.findAll('table[aria-label="SMS delivery by variant"] tbody tr').map((r) => [r.attributes('data-variant'), ...r.findAll('td').map((c) => c.text())])
    expect(rows).toEqual([['a', '5', '4', '1', '0'], ['b', '5', '4', '1', '0']])
    expect(w.findAll('.page-head button')).toHaveLength(0) // finished: nothing to stop
    w.unmount()
  })

  it('shows the local send time and a live countdown for a scheduled campaign (F5)', async () => {
    const at = new Date(NOW.getTime() + 2 * 3_600_000 + 5 * 60_000 + 9_000).toISOString()
    stub(() => campaign('scheduled', { scheduled_at: at }))
    const w = mount(DashboardView, { props: { campaignId: 'c1' } })
    await settle()
    const box = w.get('[data-testid="scheduled"]')
    expect(box.text()).toContain('Sending starts at')
    expect(box.get('[role="timer"]').text()).toBe('in 2 h 05 min 09 s')
    await vi.advanceTimersByTimeAsync(10_000)
    expect(box.get('[role="timer"]').text()).toBe('in 2 h 04 min 59 s')
    expect(w.get('.page-head button').text()).toBe('Stop sending') // cancel is available while scheduled
    w.unmount()
  })

  it('stops sending behind an in-page confirmation and shows the stopped status (F4)', async () => {
    const stopped = campaign('cancelled', { cancelled_at: NOW.toISOString() })
    stub(() => campaign('sending'), { campaign: stopped, cancelled: 3 })
    const confirmSpy = vi.fn()
    vi.stubGlobal('confirm', confirmSpy)
    const w = mount(DashboardView, { props: { campaignId: 'c1' }, attachTo: document.body })
    await settle()
    FakeEventSource.last!.emit('snapshot', progress('sending', { queued: 3, sent: 5, failed: 2 }))
    await settle()

    await w.findAll('button').find((b) => b.text() === 'Stop sending')!.trigger('click')
    expect(calls.some((c) => c.url.endsWith('/cancel'))).toBe(false) // nothing happens before the confirmation
    const dialog = w.get('dialog')
    expect(dialog.attributes('open')).toBeDefined()
    expect(dialog.text()).toContain('Stop sending?')

    await dialog.findAll('button').find((b) => b.text() === 'Stop sending')!.trigger('click')
    await settle()
    expect(calls.filter((c) => c.url.endsWith('/cancel'))).toEqual([{ url: '/api/campaigns/c1/cancel', method: 'POST' }])
    expect(w.text()).toContain('Stopped')
    expect(w.get('[data-testid="cancelled"]').text()).toContain('3 waiting messages were cancelled')

    // The stream's `done` ends live updates; cancelled messages count as finished, so the bar reaches 100%.
    FakeEventSource.last!.emit('snapshot', progress('cancelled', { queued: 0, cancelled: 3, sent: 5, failed: 2 }))
    FakeEventSource.last!.emit('done', { status: 'cancelled' })
    await settle()
    expect(w.text()).toContain('100%')
    expect(confirmSpy).not.toHaveBeenCalled() // never window.confirm
    w.unmount()
  })
})

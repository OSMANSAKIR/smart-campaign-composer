import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from './errors'
import {
  connectProgress,
  createSseParser,
  streamGenerate,
  type EventSourceLike,
  type RawSseEvent,
} from './sse'
import type { GenerateEvent, Progress } from './types'

const wire =
  'event: meta\ndata: {"provider":"mock","model":"m","prompt_version":"content_v1"}\n\n' +
  ': ping\n\n' +
  'event: delta\ndata: {"text":"{\\"sms\\":{\\"text\\":\\"Merhaba Ş"}\n\n' +
  'event: repairing\r\ndata: {"attempt":2,"errors":[]}\r\n\r\n' +
  'event: result\ndata: {"a":\ndata: 1}\n\n'

function collect(chunks: string[]): RawSseEvent[] {
  const out: RawSseEvent[] = []
  const p = createSseParser((e) => out.push(e))
  for (const c of chunks) p.push(c)
  p.end()
  return out
}

describe('createSseParser (R-FLOW-2)', () => {
  const whole = collect([wire])

  it('parses events, ignores comments, joins multi-line data', () => {
    expect(whole.map((e) => e.event)).toEqual(['meta', 'delta', 'repairing', 'result'])
    expect(whole[3]?.data).toBe('{"a":\n1}')
  })

  it('gives the same events when split at every possible position', () => {
    for (let cut = 1; cut < wire.length; cut++) {
      expect(collect([wire.slice(0, cut), wire.slice(cut)])).toEqual(whole)
    }
  })

  it('gives the same events when delivered one character at a time', () => {
    expect(collect([...wire])).toEqual(whole)
  })

  it('handles CRLF split between chunks', () => {
    expect(collect(['event: a\r', '\ndata: x\r', '\n\r', '\n'])).toEqual([{ event: 'a', data: 'x' }])
  })

  it('drops an event that never completed', () => {
    expect(collect(['event: a\ndata: partial'])).toEqual([])
  })
})

function sseResponse(chunks: Uint8Array[], status = 200): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      for (const ch of chunks) c.enqueue(ch)
      c.close()
    },
  })
  return new Response(stream, { status, headers: { 'Content-Type': 'text/event-stream' } })
}

afterEach(() => vi.unstubAllGlobals())

describe('streamGenerate (R-FLOW-2)', () => {
  it('reassembles a multibyte character split across network chunks', async () => {
    const payload = 'event: delta\ndata: {"text":"Merhaba Şükrü, İstanbul 😀"}\n\n'
    const bytes = new TextEncoder().encode(payload)
    const res = new TextEncoder().encode('event: result\ndata: {"content":{"version":1},"llm":{}}\n\n')
    // Cut inside the 2-byte "Ş" and again inside the 4-byte emoji.
    const idxS = bytes.indexOf(0xc5) + 1
    const idxEmoji = bytes.length - 8
    const chunks = [bytes.slice(0, idxS), bytes.slice(idxS, idxEmoji), bytes.slice(idxEmoji), res]
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sseResponse(chunks)))

    const got: GenerateEvent[] = []
    await streamGenerate('c1', (e) => got.push(e))
    expect(got.map((e) => e.type)).toEqual(['delta', 'result'])
    expect(got[0]).toEqual({ type: 'delta', data: { text: 'Merhaba Şükrü, İstanbul 😀' } })
  })

  it('posts to the generate URL with an empty JSON body', async () => {
    const f = vi.fn().mockResolvedValue(sseResponse([]))
    vi.stubGlobal('fetch', f)
    await streamGenerate('abc 1', () => {})
    expect(f).toHaveBeenCalledOnce()
    const [url, init] = f.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/campaigns/abc%201/content/generate')
    expect(init.method).toBe('POST')
    expect(init.body).toBe('{}')
  })

  it('delivers a terminal error event', async () => {
    const body = JSON.stringify({ error: { code: 'llm_refused', details: [{ code: 'cyber' }] } })
    const chunks = [new TextEncoder().encode(`event: error\ndata: ${body}\n\n`)]
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sseResponse(chunks)))
    const got: GenerateEvent[] = []
    await streamGenerate('c1', (e) => got.push(e))
    expect(got).toHaveLength(1)
    expect(got[0]?.type).toBe('error')
  })

  it('rejects with the API error for pre-stream failures', async () => {
    const res = new Response(JSON.stringify({ error: { code: 'invalid_state', message: 'no' } }), { status: 409 })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(res))
    await expect(streamGenerate('c1', () => {})).rejects.toMatchObject({ code: 'invalid_state', status: 409 })
  })

  it('maps a failed fetch to a network error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    const err = await streamGenerate('c1', () => {}).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ code: 'network' })
  })
})

class FakeEventSource implements EventSourceLike {
  readyState = 0
  onopen: ((e: Event) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  closed = false
  private listeners = new Map<string, ((e: MessageEvent<string>) => void)[]>()
  addEventListener(type: string, l: (e: MessageEvent<string>) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), l])
  }
  close() {
    this.closed = true
    this.readyState = 2
  }
  emit(type: string, data: unknown) {
    for (const l of this.listeners.get(type) ?? []) l({ data: JSON.stringify(data) } as MessageEvent<string>)
  }
}

function snap(sent: number): Progress {
  return {
    campaign_id: 'c1',
    status: 'sending',
    channels: {
      sms: { total: 10, queued: 10 - sent, sending: 0, sent, retry: 0, failed: 0, skipped_no_consent: 0, throughput_per_s: 1, paused_until: null },
    },
    errors: {},
    eta_seconds: 3,
    started_at: null,
    updated_at: '2026-11-08T10:00:00Z',
  }
}

describe('connectProgress (R-FLOW-5)', () => {
  it('hands every snapshot through and tracks connection state across a reconnect', () => {
    const es = new FakeEventSource()
    const states: string[] = []
    const snaps: Progress[] = []
    connectProgress('c1', { onSnapshot: (p) => snaps.push(p), onState: (s) => states.push(s) }, () => es)
    es.onopen?.(new Event('open'))
    es.emit('snapshot', snap(5))
    es.onerror?.(new Event('error')) // browser is retrying (readyState 0)
    es.emit('snapshot', snap(7)) // first message after reconnect is a full snapshot
    expect(snaps.map((s) => s.channels.sms?.sent)).toEqual([5, 7])
    expect(states).toEqual(['connecting', 'live', 'live', 'reconnecting', 'live'])
  })

  it('stops after done and reports the final status', () => {
    const es = new FakeEventSource()
    let status = ''
    connectProgress('c1', { onSnapshot: () => {}, onDone: (s) => (status = s) }, () => es)
    es.emit('done', { status: 'completed' })
    expect(status).toBe('completed')
    expect(es.closed).toBe(true)
  })

  it('reports closed when the browser gives up, and the disposer closes the source', () => {
    const es = new FakeEventSource()
    const states: string[] = []
    const stop = connectProgress('c1', { onSnapshot: () => {}, onState: (s) => states.push(s) }, () => es)
    es.readyState = 2
    es.onerror?.(new Event('error'))
    expect(states.at(-1)).toBe('closed')
    stop()
    expect(es.closed).toBe(true)
  })
})

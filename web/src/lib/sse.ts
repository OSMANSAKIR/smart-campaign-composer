// Server-sent events, two ways:
//  - createSseParser + streamGenerate: POST + fetch stream (EventSource is GET-only; need.md N-ORC-002).
//  - connectProgress: EventSource wrapper for GET .../events, where every `snapshot` is the full state.
import { generateUrl, refineUrl, eventsUrl, readError, sendRequest } from './api'
import { ApiError } from './errors'
import type { ErrorBody, GenerateEvent, Progress, ProgressEvent } from './types'

export interface RawSseEvent {
  event: string
  data: string
}

// Incremental SSE parser (WHATWG "event stream" format). Chunks may split anywhere: inside a line, between
// "\r" and "\n", or between the fields of one event. Comment lines (": ping") are ignored.
export function createSseParser(onEvent: (e: RawSseEvent) => void) {
  let buffer = ''
  let skipLeadingLF = false // previous chunk ended in "\r"
  let event = ''
  let data: string[] = []

  function dispatch() {
    if (data.length > 0 || event !== '') onEvent({ event: event || 'message', data: data.join('\n') })
    event = ''
    data = []
  }

  function line(l: string) {
    if (l === '') return dispatch()
    if (l.startsWith(':')) return
    const i = l.indexOf(':')
    const field = i === -1 ? l : l.slice(0, i)
    let value = i === -1 ? '' : l.slice(i + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') event = value
    else if (field === 'data') data.push(value)
    // id / retry are not used by this client.
  }

  return {
    push(chunk: string) {
      if (skipLeadingLF && chunk.startsWith('\n')) chunk = chunk.slice(1)
      if (chunk.length > 0) skipLeadingLF = false
      buffer += chunk
      let start = 0
      for (let i = 0; i < buffer.length; i++) {
        const c = buffer[i]
        if (c === '\n' || c === '\r') {
          line(buffer.slice(start, i))
          if (c === '\r') {
            if (buffer[i + 1] === '\n') i++
            else if (i + 1 === buffer.length) skipLeadingLF = true
          }
          start = i + 1
        }
      }
      buffer = buffer.slice(start)
    },
    // A stream that ends mid-event discards the incomplete event, as browsers do.
    end() {
      buffer = ''
      event = ''
      data = []
    },
  }
}

const GENERATE_EVENTS = new Set(['meta', 'delta', 'repairing', 'result', 'error'])

export function toGenerateEvent(raw: RawSseEvent): GenerateEvent | null {
  if (!GENERATE_EVENTS.has(raw.event)) return null
  try {
    return { type: raw.event, data: JSON.parse(raw.data) } as GenerateEvent
  } catch {
    return null
  }
}

// Reads the generation stream. Resolves when the stream ends; rejects with ApiError for pre-stream failures
// (404/409/network). A terminal `error` event is delivered through onEvent like any other event.
export function streamGenerate(
  campaignId: string,
  onEvent: (e: GenerateEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  return streamPost(generateUrl(campaignId), {}, onEvent, signal)
}

// Refine one variant with an instruction (api.md §9.4); same stream as generate.
export function streamRefine(
  campaignId: string,
  body: { variant: 'a' | 'b'; instruction: string; base_version: number },
  onEvent: (e: GenerateEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  return streamPost(refineUrl(campaignId), body, onEvent, signal)
}

async function streamPost(
  url: string,
  body: unknown,
  onEvent: (e: GenerateEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const res = await sendRequest('POST', url, {
    body,
    headers: { Accept: 'text/event-stream' },
    signal,
  })
  if (!res.ok) throw await readError(res)
  if (!res.body) throw new ApiError(0, 'network', 'The server sent no stream')

  const parser = createSseParser((raw) => {
    const ev = toGenerateEvent(raw)
    if (ev) onEvent(ev)
  })
  // stream:true keeps a multibyte character that is split across network chunks intact.
  const decoder = new TextDecoder('utf-8')
  const reader = res.body.getReader()
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      parser.push(decoder.decode(value, { stream: true }))
    }
    parser.push(decoder.decode())
  } catch (e) {
    if (e instanceof Error && e.name === 'AbortError') throw e
    throw new ApiError(0, 'network', e instanceof Error ? e.message : 'stream interrupted')
  } finally {
    parser.end()
    reader.releaseLock()
  }
}

export function errorFromBody(body: ErrorBody): ApiError {
  return ApiError.fromBody(0, body)
}

// --- Progress stream (GET .../events) -------------------------------------------------------------------

export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'closed'

export interface ProgressHandlers {
  onSnapshot: (p: Progress) => void
  onEvent?: (e: ProgressEvent) => void
  onDone?: (status: string) => void
  onState?: (s: ConnectionState) => void
}

// The subset of EventSource this wrapper needs (also what tests fake).
export interface EventSourceLike {
  readyState: number
  addEventListener(type: string, listener: (e: MessageEvent<string>) => void): void
  close(): void
  onopen: ((e: Event) => void) | null
  onerror: ((e: Event) => void) | null
}

export type EventSourceFactory = (url: string) => EventSourceLike

const defaultFactory: EventSourceFactory = (url) => new EventSource(url) as unknown as EventSourceLike

function parse<T>(raw: string): T | null {
  try {
    return JSON.parse(raw) as T
  } catch {
    return null
  }
}

// EventSource reconnects by itself; the server opens every connection with a full `snapshot`, so the handler
// simply replaces its state. After `done` the server closes the stream and we stop reconnecting.
export function connectProgress(
  campaignId: string,
  h: ProgressHandlers,
  factory: EventSourceFactory = defaultFactory,
): () => void {
  const es = factory(eventsUrl(campaignId))
  let finished = false
  h.onState?.('connecting')
  es.onopen = () => h.onState?.('live')
  es.addEventListener('snapshot', (m) => {
    const p = parse<Progress>(m.data)
    if (p) {
      h.onState?.('live')
      h.onSnapshot(p)
    }
  })
  es.addEventListener('event', (m) => {
    const e = parse<ProgressEvent>(m.data)
    if (e) h.onEvent?.(e)
  })
  es.addEventListener('done', (m) => {
    finished = true
    es.close()
    h.onState?.('closed')
    h.onDone?.(parse<{ status: string }>(m.data)?.status ?? '')
  })
  es.onerror = () => {
    if (finished) return
    // readyState 2 = CLOSED: the browser gave up (e.g. HTTP error); 0 = CONNECTING: it is retrying.
    h.onState?.(es.readyState === 2 ? 'closed' : 'reconnecting')
  }
  return () => {
    finished = true
    es.close()
  }
}

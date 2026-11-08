import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, newIdempotencyKey } from './api'
import { ApiError, describeError, KNOWN_ERROR_CODES } from './errors'

const CONTRACT_CODES = [
  'bad_request',
  'idempotency_key_required',
  'not_found',
  'invalid_state',
  'stale_content_version',
  'approve_in_progress',
  'already_approved',
  'validation_failed',
  'llm_invalid_output',
  'llm_refused',
  'content_invalid',
  'empty_audience',
  'llm_failed',
  'unavailable',
  'internal',
  'network',
]

describe('describeError maps every API error code (R-FLOW-1..3)', () => {
  it('covers every code in api.md §1', () => {
    expect([...KNOWN_ERROR_CODES].sort()).toEqual([...CONTRACT_CODES].sort())
  })

  it.each(CONTRACT_CODES)('%s has a human title and message', (code) => {
    const m = describeError(new ApiError(422, code, 'raw server text'))
    expect(m.title.length).toBeGreaterThan(5)
    expect(m.message.length).toBeGreaterThan(10)
    expect(m.message).not.toContain(code) // no machine codes in user-facing text
  })

  it('lists 422 details one per line with their path', () => {
    const m = describeError(
      new ApiError(422, 'validation_failed', 'x', [
        { path: 'sms_text', code: 'too_long', message: 'SMS is 169 characters, limit 160' },
        { code: 'required' },
      ]),
    )
    expect(m.details).toEqual(['sms_text: SMS is 169 characters, limit 160', 'required'])
  })

  it('names the refusal category, or says none was given', () => {
    expect(describeError(new ApiError(422, 'llm_refused', '', [{ code: 'cyber' }])).message).toContain('cyber')
    expect(describeError(new ApiError(422, 'llm_refused', '', [{ code: 'unknown' }])).message).toContain('no reason given')
  })

  it('marks LLM and network failures as retryable and state conflicts as not', () => {
    expect(describeError(new ApiError(502, 'llm_failed', '')).retryable).toBe(true)
    expect(describeError(new ApiError(0, 'network', '')).retryable).toBe(true)
    expect(describeError(new ApiError(409, 'stale_content_version', '')).retryable).toBe(false)
  })

  it('falls back to the server message for unknown codes and plain errors', () => {
    expect(describeError(new ApiError(501, 'not_implemented', 'stub')).message).toBe('stub')
    expect(describeError(new Error('boom')).message).toBe('boom')
  })
})

afterEach(() => vi.unstubAllGlobals())

describe('api client', () => {
  it('turns a JSON error body into an ApiError', async () => {
    const body = { error: { code: 'stale_content_version', message: 'm' } }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status: 409 })))
    await expect(api.getCampaign('c1')).rejects.toMatchObject({ status: 409, code: 'stale_content_version' })
  })

  it('survives a non-JSON error body', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>bad gateway</html>', { status: 502 })))
    await expect(api.systemStatus()).rejects.toMatchObject({ status: 502, code: 'http_502' })
  })

  it('maps a fetch failure to a network error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    await expect(api.listCampaigns()).rejects.toMatchObject({ code: 'network' })
  })

  it('sends the Idempotency-Key header and the approve body', async () => {
    const f = vi.fn().mockResolvedValue(new Response('{}', { status: 202 }))
    vi.stubGlobal('fetch', f)
    await api.approve('c1', ['sms'], 3, 'key-1')
    const [url, init] = f.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/campaigns/c1/approve')
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toBe('key-1')
    expect(JSON.parse(init.body as string)).toEqual({ channels: ['sms'], content_version: 3 })
  })

  it('sends base_version with a content save and builds list queries', async () => {
    const f = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', f)
    await api.saveContent('c1', { sms_text: 'a', email_subject: 'b', email_body: 'c' }, 0)
    expect(JSON.parse((f.mock.calls[0] as [string, RequestInit])[1].body as string)).toMatchObject({ base_version: 0 })
    await api.listCampaigns({ limit: 5, cursor: 'x y' })
    expect((f.mock.calls[1] as [string])[0]).toBe('/api/campaigns?limit=5&cursor=x%20y')
  })

  it('generates a fresh idempotency key each time', () => {
    expect(newIdempotencyKey()).not.toBe(newIdempotencyKey())
  })
})

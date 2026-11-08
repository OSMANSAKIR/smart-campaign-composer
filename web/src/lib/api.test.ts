import { describe, expect, it } from 'vitest'
import { webcrypto } from 'node:crypto'
import { newIdempotencyKey } from './api'

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

describe('newIdempotencyKey (R-SND-3)', () => {
  it('uses randomUUID when the context is secure', () => {
    const fixed = '00000000-0000-4000-8000-000000000000'
    const secure = {
      getRandomValues: () => {
        throw new Error('unused')
      },
      randomUUID: () => fixed as `${string}-${string}-${string}-${string}-${string}`,
    }
    expect(newIdempotencyKey(secure)).toBe(fixed)
  })

  it('falls back to getRandomValues over plain http (no randomUUID)', () => {
    const insecure = { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) as Crypto['getRandomValues'] }
    const keys = new Set(Array.from({ length: 200 }, () => newIdempotencyKey(insecure)))
    expect(keys.size).toBe(200)
    for (const k of keys) expect(k).toMatch(UUID_V4)
  })
})

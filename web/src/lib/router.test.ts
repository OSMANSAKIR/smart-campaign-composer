import { describe, expect, it } from 'vitest'
import { links, parseHash } from './router'

describe('parseHash', () => {
  it('routes the known screens', () => {
    expect(parseHash('')).toEqual({ name: 'list' })
    expect(parseHash('#/')).toEqual({ name: 'list' })
    expect(parseHash('#/new')).toEqual({ name: 'new' })
    expect(parseHash('#/system')).toEqual({ name: 'system' })
    expect(parseHash('#/c/abc')).toEqual({ name: 'composer', id: 'abc' })
    expect(parseHash('#/c/abc/live')).toEqual({ name: 'live', id: 'abc' })
  })
  it('round-trips ids and rejects unknown paths', () => {
    expect(parseHash(links.live('a b'))).toEqual({ name: 'live', id: 'a b' })
    expect(parseHash('#/c')).toEqual({ name: 'notfound' })
    expect(parseHash('#/c/x/y/z')).toEqual({ name: 'notfound' })
  })
})

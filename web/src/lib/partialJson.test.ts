import { describe, expect, it } from 'vitest'
import { extractContent, parsePartialJson } from './partialJson'

const full = JSON.stringify({
  language: 'tr',
  sms: { text: 'Merhaba {{first_name}}, İstanbul’da %30 indirim seni bekliyor! Şimdi gör.' },
  email: { subject: 'Sepetin seni bekliyor', body: 'Merhaba {{first_name}},\n\nSepetindeki ürünlerde %30 indirim.\n"Tırnaklı" satır' },
  notes: 'ok',
})

describe('extractContent reads incomplete JSON (R-FLOW-2)', () => {
  it('returns empty fields for an empty or garbage buffer', () => {
    expect(extractContent('')).toEqual({ smsText: '', emailSubject: '', emailBody: '' })
    expect(extractContent('not json')).toEqual({ smsText: '', emailSubject: '', emailBody: '' })
  })

  it('shows a half-written string', () => {
    expect(extractContent('{"language":"tr","sms":{"text":"Merhaba {{first_na').smsText).toBe('Merhaba {{first_na')
  })

  it('fills fields as the buffer grows and equals the final values once complete', () => {
    const final = extractContent(full)
    expect(final.smsText).toBe('Merhaba {{first_name}}, İstanbul’da %30 indirim seni bekliyor! Şimdi gör.')
    expect(final.emailSubject).toBe('Sepetin seni bekliyor')
    expect(final.emailBody).toBe('Merhaba {{first_name}},\n\nSepetindeki ürünlerde %30 indirim.\n"Tırnaklı" satır')
    // Every prefix parses without throwing, and the SMS text only ever grows (prefix property).
    let prev = ''
    for (let i = 0; i <= full.length; i++) {
      const cur = extractContent(full.slice(0, i)).smsText
      expect(final.smsText.startsWith(cur)).toBe(true)
      expect(cur.length).toBeGreaterThanOrEqual(prev.length)
      prev = cur
    }
  })

  it('does not use a half-received key or later siblings of an unfinished value', () => {
    const c = extractContent('{"sms":{"text":"abc"},"email":{"subj')
    expect(c).toEqual({ smsText: 'abc', emailSubject: '', emailBody: '' })
  })

  it('holds back a split \\u escape until it is complete', () => {
    // "ş" written as an escape, cut after 2 hex digits, then completed.
    expect(extractContent('{"sms":{"text":"Mer\\u01').smsText).toBe('Mer')
    expect(extractContent('{"sms":{"text":"Mer\\u015').smsText).toBe('Mer')
    expect(extractContent('{"sms":{"text":"Mer\\u015f').smsText).toBe('Merş')
    expect(extractContent('{"sms":{"text":"Mer\\').smsText).toBe('Mer')
    expect(extractContent('{"sms":{"text":"Mer\\u').smsText).toBe('Mer')
  })

  it('holds back half of a surrogate pair written as two escapes', () => {
    expect(extractContent('{"sms":{"text":"Hi \\ud83d').smsText).toBe('Hi ')
    expect(extractContent('{"sms":{"text":"Hi \\ud83d\\ude00').smsText).toBe('Hi 😀')
  })

  it('decodes simple escapes', () => {
    expect(extractContent('{"email":{"body":"a\\nb\\"c\\\\d').emailBody).toBe('a\nb"c\\d')
  })

  it('keeps multibyte characters at a chunk boundary intact (chunks are decoded strings)', () => {
    const text = '{"sms":{"text":"Çağrı: şüphesiz İstanbul"}}'
    for (let cut = 1; cut < text.length; cut++) {
      const buf = text.slice(0, cut) + text.slice(cut) // chunks concatenated by the caller
      expect(extractContent(buf).smsText).toBe('Çağrı: şüphesiz İstanbul')
      expect('Çağrı: şüphesiz İstanbul'.startsWith(extractContent(text.slice(0, cut)).smsText)).toBe(true)
    }
  })

  it('tolerates numbers, booleans, nulls and arrays in other keys', () => {
    expect(parsePartialJson('{"a":[1,2,{"b":null}],"c":true,"d":-1.5e3')).toEqual({
      a: [1, 2, { b: null }],
      c: true,
      d: -1500,
    })
  })
})

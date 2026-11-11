// Content in the recipient's language (B-3, api.md §9.9). Pure helpers: the server stays the authority (a 422
// missing_localization says the same), these only tell the UI early and in words.
import type {
  Audience,
  Campaign,
  Channel,
  ChannelProgress,
  ContentVariant,
  ContentVersion,
  Language,
  LocalizedCopy,
  SendVariant,
  VariantProgress,
} from './types'
import { variantsOf, variantsToSend } from './variants'

export const LOCALES: Language[] = ['tr', 'en']
export const LOCALE_NAME: Record<Language, string> = { tr: 'Turkish', en: 'English' }
const CHANNEL_NAME: Record<Channel, string> = { sms: 'SMS', email: 'email' }

export const otherLocale = (l: Language): Language => (l === 'tr' ? 'en' : 'tr')

// People of one locale that match the audience (before consent), or 0 when the server sent no breakdown.
export function localeTotal(a: Audience | null | undefined, l: Language): number {
  return a?.by_locale?.[l]?.total ?? 0
}

// Consented people of one locale on one channel.
export function localeReachable(a: Audience | null | undefined, l: Language, ch: Channel): number {
  const x = a?.by_locale?.[l]
  return ch === 'sms' ? (x?.sms_reachable ?? 0) : (x?.email_reachable ?? 0)
}

// The campaign's other locale, when somebody in the audience reads it (so an adaptation has a point).
export function audienceHasOtherLocale(c: Campaign): boolean {
  return localeTotal(c.segment?.audience, otherLocale(c.language)) > 0
}

export interface LocaleLine {
  locale: Language
  name: string
  total: number
  sms: number
  email: number
}

// Rows of the language breakdown in the audience step; empty when the server sent none.
export function audienceLocaleLines(a: Audience | null | undefined): LocaleLine[] {
  if (!a?.by_locale) return []
  return LOCALES.filter((l) => a.by_locale?.[l]).map((l) => ({
    locale: l,
    name: LOCALE_NAME[l],
    total: a.by_locale![l]!.total,
    sms: a.by_locale![l]!.sms_reachable,
    email: a.by_locale![l]!.email_reachable,
  }))
}

// True when the adaptation no longer answers to the primary copy: the server says so after a save, and unsaved
// edits of the primary texts make it so immediately (the label is not part of the copy).
export function isStale(row: LocalizedCopy | null | undefined, primary: ContentVariant | null | undefined, form: { sms_text: string; email_subject: string; email_body: string } | null): boolean {
  if (!row) return false
  if (row.stale) return true
  if (!primary || !form) return false
  return form.sms_text !== primary.sms_text || form.email_subject !== primary.email_subject || form.email_body !== primary.email_body
}

// Why "Each recipient in their language" cannot be approved yet, or '' when it can. It mirrors the server gate:
// for every sent variant and channel, every other locale with consented recipients on that channel needs an
// adaptation that exists, is current and is valid for the channel.
export function localizationProblem(
  content: ContentVersion | null,
  campaign: Campaign,
  channels: Channel[],
  send: SendVariant,
): string {
  if (!content) return 'There is no content yet.'
  const other = otherLocale(campaign.language)
  const aud = campaign.segment?.audience
  const vs = variantsOf(content)
  const name = LOCALE_NAME[other]
  for (const ch of channels) {
    if (localeReachable(aud, other, ch) === 0) continue
    for (const k of variantsToSend(send)) {
      const row = vs[k]?.localized?.[other]
      if (!row) return `Variant ${k.toUpperCase()} has no ${name} version yet. Write it in the content step.`
      if (row.stale) return `The ${name} version of variant ${k.toUpperCase()} is out of date: the ${LOCALE_NAME[campaign.language]} text changed. Re-adapt it.`
      if (!row.validation.valid_for[ch])
        return `The ${name} version of variant ${k.toUpperCase()} is not valid for ${CHANNEL_NAME[ch]} yet. Fix the errors shown.`
    }
  }
  return ''
}

export interface LocaleDeliveryRow {
  locale: Language
  name: string
  v: VariantProgress
}

// Per-locale rows for a channel in the dashboard, only for locales that have messages (api.md §9.9).
export function localeRows(c: ChannelProgress): LocaleDeliveryRow[] {
  const out: LocaleDeliveryRow[] = []
  for (const l of LOCALES) {
    const v = c.locales?.[l]
    if (v) out.push({ locale: l, name: LOCALE_NAME[l], v })
  }
  return out
}

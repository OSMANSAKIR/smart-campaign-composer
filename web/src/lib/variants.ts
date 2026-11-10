// Helpers for A/B content (api.md §9.3). A version from an older server has no `variants`; it reads as a
// one-variant version built from the top-level fields.
import type {
  Campaign,
  Channel,
  ContentVariant,
  ContentVersion,
  SendVariant,
  VariantInput,
  VariantKey,
} from './types'

export const VARIANT_KEYS: VariantKey[] = ['a', 'b']

export function variantsOf(c: ContentVersion): { a: ContentVariant; b: ContentVariant | null } {
  if (c.variants) return { a: c.variants.a, b: c.variants.b ?? null }
  return {
    a: {
      label: 'Variant A',
      source: c.source,
      instruction: null,
      sms_text: c.sms_text,
      email_subject: c.email_subject,
      email_body: c.email_body,
      validation: c.validation,
    },
    b: null,
  }
}

export function toInput(v: ContentVariant): VariantInput {
  return { label: v.label, sms_text: v.sms_text, email_subject: v.email_subject, email_body: v.email_body }
}

// Variants that a send choice will deliver.
export function variantsToSend(choice: SendVariant): VariantKey[] {
  return choice === 'ab' ? ['a', 'b'] : [choice]
}

// Why a send choice cannot be used on this content for these channels, or '' when it can.
export function variantProblem(content: ContentVersion, choice: SendVariant, channels: Channel[]): string {
  const vs = variantsOf(content)
  for (const k of variantsToSend(choice)) {
    const v = vs[k]
    if (!v) return 'There is no variant B in this version. Write a new draft with Claude to get two variants.'
    for (const ch of channels) {
      if (!v.validation.valid_for[ch]) {
        return `Variant ${k.toUpperCase()} is not valid for ${ch === 'sms' ? 'SMS' : 'email'} yet. Fix the errors shown.`
      }
    }
  }
  return ''
}

export function campaignHasB(c: Campaign | null): boolean {
  return !!c?.content && variantsOf(c.content).b !== null
}

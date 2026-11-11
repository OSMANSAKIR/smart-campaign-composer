// Approve gating (R-FLOW-3): the button is enabled only when the server's report for the *latest saved*
// content version says every variant that will be sent is valid for every chosen channel, and each chosen
// channel has recipients.
import { localizationProblem } from './localization'
import type { Campaign, Channel, LanguageMode, SendVariant } from './types'
import { variantProblem } from './variants'

export interface GateInput {
  campaign: Campaign | null
  channels: Channel[]
  // True when the editor holds text that is not the latest saved version.
  hasUnsavedEdits: boolean
  // True while a save or validation request is in flight.
  busy?: boolean
  // Which variant(s) will be sent (B-2). Defaults to a.
  variant?: SendVariant
  // 'recipient' (B-3) also needs a current, valid copy for every language among the recipients.
  languageMode?: LanguageMode
  // A reason the chosen send time is not allowed (F5), or '' / undefined when it is fine.
  scheduleProblem?: string
}

export interface Gate {
  enabled: boolean
  // Human reason shown next to a disabled button.
  reason: string
}

const NAMES: Record<Channel, string> = { sms: 'SMS', email: 'email' }

export function approveGate(g: GateInput): Gate {
  const no = (reason: string): Gate => ({ enabled: false, reason })
  const c = g.campaign
  if (!c) return no('Campaign is not loaded yet.')
  if (
    c.status === 'sending' ||
    c.status === 'scheduled' ||
    c.status === 'cancelled' ||
    c.status === 'completed' ||
    c.status === 'completed_with_failures'
  ) {
    return no('This campaign has already been approved.')
  }
  if (!c.segment) return no('Find the audience first.')
  if (c.status === 'draft') return no('Find the audience first.')
  if (g.channels.length === 0) return no('Choose at least one channel.')
  if (!c.content) return no('Generate or write the content first.')
  // After a segment change the campaign drops back to `segmented`: the content must be saved or regenerated.
  if (c.status !== 'content_ready') return no('The audience changed. Regenerate or save the content again.')
  if (g.hasUnsavedEdits) return no('Save your edits first; approval uses the saved version.')
  if (g.busy) return no('Waiting for the server…')

  for (const ch of g.channels) {
    const reachable = ch === 'sms' ? c.segment.audience.sms_reachable : c.segment.audience.email_reachable
    if (reachable <= 0) return no(`No one in this audience has consented to ${NAMES[ch]}.`)
  }
  const problem = variantProblem(c.content, g.variant ?? 'a', g.channels)
  if (problem) return no(problem)
  if (g.languageMode === 'recipient') {
    const lp = localizationProblem(c.content, c, g.channels, g.variant ?? 'a')
    if (lp) return no(lp)
  }
  if (g.scheduleProblem) return no(g.scheduleProblem)
  return { enabled: true, reason: '' }
}

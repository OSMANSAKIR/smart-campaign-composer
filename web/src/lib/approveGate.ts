// Approve gating (R-FLOW-3): the button is enabled only when the server's report for the *latest saved*
// content version says it is valid for every chosen channel, and each chosen channel has recipients.
import type { Campaign, Channel } from './types'

export interface GateInput {
  campaign: Campaign | null
  channels: Channel[]
  // True when the editor holds text that is not the latest saved version.
  hasUnsavedEdits: boolean
  // True while a save or validation request is in flight.
  busy?: boolean
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
  if (c.status === 'sending' || c.status === 'completed' || c.status === 'completed_with_failures') {
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
  const report = c.content.validation
  for (const ch of g.channels) {
    if (!report.valid_for[ch]) return no(`The ${NAMES[ch]} content is not valid yet. Fix the errors shown.`)
  }
  return { enabled: true, reason: '' }
}

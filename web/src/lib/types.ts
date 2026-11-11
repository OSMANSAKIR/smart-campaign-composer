// Wire types for docs/contracts/api.md. Fields the contract does not define are not invented here.

export type Channel = 'sms' | 'email'
export type ChannelsRequested = Channel | 'both'
export type Language = 'tr' | 'en'

export type CampaignStatus =
  | 'draft'
  | 'segmented'
  | 'content_ready'
  | 'scheduled'
  | 'sending'
  | 'completed'
  | 'completed_with_failures'
  | 'cancelled'

export interface ErrorDetail {
  path?: string
  code: string
  message?: string
}

export interface ErrorBody {
  error: { code: string; message?: string; details?: ErrorDetail[] }
}

export interface LlmMeta {
  provider: string
  model: string
  prompt_version: string
  latency_ms: number
  attempts: number
  repaired: boolean
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  cost_usd: number
  // v2 (api.md §9.7): false when the cost row could not be stored; the generation itself succeeded.
  cost_recorded?: boolean
}

// Consented counts of one recipient locale (B-3, api.md §9.10).
export interface LocaleAudience {
  total: number
  sms_reachable: number
  email_reachable: number
  both: number
}

export interface Audience {
  total: number
  sms_reachable: number
  email_reachable: number
  both: number
  // B-3: the same counts per recipient locale. Missing on a campaign counted before B-3.
  by_locale?: Partial<Record<Language, LocaleAudience>>
  counted_at: string
}

export interface Issue {
  path: string
  code: string
  message: string
}

// Segment DSL (docs/contracts/segment-dsl.md): groups are ANDed, conditions inside a group are ORed.
export type RuleValue = number | string | null | (number | string | null)[]
export interface RuleCondition {
  field: string
  op: string
  value: RuleValue
}
export interface RuleGroup {
  conditions: RuleCondition[]
}
export interface SegmentRule {
  groups: RuleGroup[]
}

export interface Segment {
  version: number
  source: 'llm' | 'edited'
  rule: SegmentRule
  // v2 (api.md §9.2): Claude's last rule, kept after manual edits; null when never edited.
  original_rule?: SegmentRule | null
  edited_at?: string | null
  rendered: { en: string[]; tr: string[] }
  unsupported_criteria: string[]
  assumptions: string[]
  explanation: string
  channels_requested: ChannelsRequested
  language: Language
  warnings: Issue[]
  audience: Audience
}

export interface TextFieldReport {
  runes: number
  worst_case_runes: number
  limit: number
  encoding?: 'gsm7' | 'ucs2'
  utf16_units?: number
  segments?: number
}

export interface ValidationReport {
  valid: boolean
  valid_for: Record<Channel, boolean>
  fields: {
    sms_text: TextFieldReport
    email_subject: TextFieldReport
    email_body: { runes: number; limit: number }
  }
  worst_case: { first_name: string; city: string }
  variables: string[]
  errors: Issue[]
  warnings: Issue[]
}

export type VariantKey = 'a' | 'b'
export type SendVariant = 'a' | 'b' | 'ab'
export type LanguageMode = 'campaign' | 'recipient'

// The adaptation of a primary copy to the other locale (B-3, api.md §9.10).
export interface LocalizedCopy {
  label: string
  source: 'llm' | 'edited'
  sms_text: string
  email_subject: string
  email_body: string
  validation: ValidationReport
  // The primary copy changed after this adaptation was made or last edited.
  stale: boolean
}

export interface ContentVariant {
  label: string
  source: 'llm' | 'edited' | 'refined'
  instruction: string | null
  sms_text: string
  email_subject: string
  email_body: string
  validation: ValidationReport
  // B-3: the language of this primary copy and its adaptations by locale. Absent on an older server.
  locale?: Language
  localized?: Partial<Record<Language, LocalizedCopy>>
}

export interface ContentVersion {
  version: number
  source: 'llm' | 'edited' | 'refined'
  // Mirrors variant a (v1 compatibility).
  sms_text: string
  email_subject: string
  email_body: string
  validation: ValidationReport
  created_at: string
  // v2 (api.md §9.3). Optional so an older server's answer still reads; see variantsOf().
  variants?: { a: ContentVariant; b: ContentVariant | null }
}

// What the editor sends for one variant (api.md §9.3 "E").
export interface VariantInput {
  label: string
  sms_text: string
  email_subject: string
  email_body: string
}

// One variant in a save: the primary copy plus the adaptations being replaced (B-3).
export interface VariantSave extends VariantInput {
  localized?: Partial<Record<Language, VariantInput>>
}

export interface ChannelProgress {
  total: number
  queued: number
  sending: number
  sent: number
  retry: number
  failed: number
  skipped_no_consent: number
  // v2: cancelled messages (api.md §9.5) and the per-variant split (§9.3).
  cancelled?: number
  variants?: Partial<Record<VariantKey, VariantProgress>>
  // B-3: terminal counts per recipient locale (api.md §9.10).
  locales?: Partial<Record<Language, VariantProgress>>
  throughput_per_s: number
  paused_until: string | null
}

export interface VariantProgress {
  total: number
  sent: number
  failed: number
  cancelled: number
  skipped_no_consent: number
}

export interface Progress {
  campaign_id: string
  status: CampaignStatus
  channels: Partial<Record<Channel, ChannelProgress>>
  errors: Record<string, number>
  eta_seconds: number | null
  started_at: string | null
  updated_at: string
}

export interface Campaign {
  id: string
  brief: string
  status: CampaignStatus
  language: Language
  segment: Segment | null
  content: ContentVersion | null
  channels: Channel[]
  approved_content_version: number | null
  // v2 (api.md §2, §9).
  send_variant?: SendVariant | null
  // B-3: 'campaign' sends everyone the campaign-language copy, 'recipient' each recipient's own language.
  language_mode?: LanguageMode
  scheduled_at?: string | null
  cancelled_at?: string | null
  created_at: string
  approved_at: string | null
  completed_at: string | null
  progress?: Progress
}

// Campaign list item (api.md, orchestrator update de22d3a).
export interface CampaignSummary {
  id: string
  brief: string
  status: CampaignStatus
  language: Language
  channels: Channel[]
  audience_total: number | null
  created_at: string
  approved_at: string | null
  scheduled_at: string | null
  completed_at: string | null
}

export interface Page<T> {
  items: T[]
  next_cursor: string | null
}

export interface MessageItem {
  id: string
  user_id: number
  channel: Channel
  status: string
  attempts: number
  last_error: string | null
  sent_at: string | null
  updated_at: string
  variant?: VariantKey
}

export interface ContentInput {
  sms_text: string
  email_subject: string
  email_body: string
}

// Body of PUT .../content in v2 (api.md §9.3).
export interface SaveContentBody {
  variants: { a: VariantSave; b: VariantSave | null }
}

export interface ValidateRequest extends ContentInput {
  campaign_id?: string
  language?: Language
}

export interface ApproveResult {
  campaign: Campaign
  enqueued: { sms: number; email: number }
}

export type WorkerStatus = 'ok' | 'stale' | 'down'

export interface SystemStatus {
  api: { status: string; version: string }
  db: { status: string; latency_ms?: number }
  worker: {
    status: WorkerStatus
    dispatchers?: {
      worker_id: string
      channel: Channel
      holds_lock: boolean
      paused_until: string | null
      last_seen: string
      stale?: boolean
    }[]
  }
  provider: { status: 'ok' | 'down'; url?: string }
  llm: { provider: string; model: string; mock: boolean }
}

// GET /api/catalog (api.md §9.1). The server validator stays the authority; this drives the rule editor.
export type FieldType = 'number' | 'enum' | 'bool' | 'date'
export interface CatalogField {
  name: string
  type: FieldType
  label: { en: string; tr: string }
  description?: string
  ops: string[]
  nullable?: boolean
  values?: string[]
  min?: number
  max?: number
  integer?: boolean
  unit?: string
}
export interface CatalogLimits {
  max_groups: number
  max_conditions_per_group: number
  max_conditions_total: number
  max_list_items: number
  min_days: number
  max_days: number
  max_string_runes: number
  min_date: string
  max_date: string
}
export interface Catalog {
  fields: CatalogField[]
  operators?: Partial<Record<FieldType, string[]>>
  limits: CatalogLimits
  variables?: string[]
  content_limits?: { sms_text: number; email_subject: number; email_body: number; variable_value: number }
  channels?: Channel[]
}

// POST /api/segment/preview (api.md §9.2).
export interface SegmentPreview {
  valid: boolean
  errors: Issue[]
  warnings: Issue[]
  rendered: { en: string[]; tr: string[] }
  audience: Audience | null
}

export interface CancelResult {
  campaign: Campaign
  cancelled: number
}

export interface ProgressEvent {
  at: string
  message_id: string
  channel: Channel
  attempt: number
  outcome: string
  http_status: number | null
  error_class: string | null
}

// Generation stream events (api.md §5).
export type GenerateEvent =
  | { type: 'meta'; data: { provider: string; model: string; prompt_version: string } }
  | { type: 'delta'; data: { text: string } }
  | { type: 'repairing'; data: { attempt: number; errors: Issue[] } }
  | { type: 'result'; data: { content: ContentVersion; llm: LlmMeta } }
  | { type: 'error'; data: ErrorBody }

// Mock provider fault configuration (docs/contracts/provider-api.md §3), edited from the System page.
export interface ProviderConfig {
  latency_min_ms: number
  latency_max_ms: number
  spike_rate: number
  spike_ms: number
  error_rate: number
  rate_429: number
  retry_after_s: number
  hang_rate: number
  rate_limit: number
  seed: number
}

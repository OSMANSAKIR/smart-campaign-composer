// Wire types for docs/contracts/api.md. Fields the contract does not define are not invented here.

export type Channel = 'sms' | 'email'
export type ChannelsRequested = Channel | 'both'
export type Language = 'tr' | 'en'

export type CampaignStatus =
  | 'draft'
  | 'segmented'
  | 'content_ready'
  | 'sending'
  | 'completed'
  | 'completed_with_failures'

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
}

export interface Audience {
  total: number
  sms_reachable: number
  email_reachable: number
  both: number
  counted_at: string
}

export interface Issue {
  path: string
  code: string
  message: string
}

export interface Segment {
  version: number
  source: 'llm' | 'edited'
  // The rule is the segment DSL (docs/contracts/segment-dsl.md); the UI shows it as JSON and never interprets it.
  rule: unknown
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

export interface ContentVersion {
  version: number
  source: 'llm' | 'edited'
  sms_text: string
  email_subject: string
  email_body: string
  validation: ValidationReport
  created_at: string
}

export interface ChannelProgress {
  total: number
  queued: number
  sending: number
  sent: number
  retry: number
  failed: number
  skipped_no_consent: number
  throughput_per_s: number
  paused_until: string | null
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
}

export interface ContentInput {
  sms_text: string
  email_subject: string
  email_body: string
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

// Catalog shape is not specified in api.md; kept opaque.
export type Catalog = Record<string, unknown>

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

// Typed client for docs/contracts/api.md. Same-origin; `fetch` is looked up at call time so tests can stub it.
import { ApiError } from './errors'
import type {
  ApproveResult,
  CancelResult,
  Campaign,
  CampaignSummary,
  Catalog,
  Channel,
  ContentInput,
  ContentVersion,
  SaveContentBody,
  SegmentPreview,
  SendVariant,
  VariantKey,
  ErrorBody,
  LlmMeta,
  MessageItem,
  Page,
  ProviderConfig,
  SegmentRule,
  SystemStatus,
  ValidateRequest,
  ValidationReport,
} from './types'

interface RequestOptions {
  body?: unknown
  headers?: Record<string, string>
  signal?: AbortSignal
}

export async function readError(res: Response): Promise<ApiError> {
  let body: ErrorBody | null = null
  try {
    body = (await res.json()) as ErrorBody
  } catch {
    // Non-JSON error body (proxy error page, 501 stub, ...): fall back to the status code.
  }
  return ApiError.fromBody(res.status, body)
}

export async function sendRequest(method: string, path: string, opts: RequestOptions = {}): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json', ...opts.headers }
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'
  try {
    return await fetch(path, {
      method,
      headers,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: opts.signal,
    })
  } catch (e) {
    if (e instanceof Error && e.name === 'AbortError') throw e
    throw new ApiError(0, 'network', e instanceof Error ? e.message : 'network error')
  }
}

async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await sendRequest(method, path, opts)
  if (!res.ok) throw await readError(res)
  return (await res.json()) as T
}

const enc = encodeURIComponent

function query(params: Record<string, string | number | undefined>): string {
  const q = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== '')
    .map(([k, v]) => `${enc(k)}=${enc(String(v))}`)
    .join('&')
  return q ? `?${q}` : ''
}

export const api = {
  catalog: (signal?: AbortSignal) => request<Catalog>('GET', '/api/catalog', { signal }),

  createCampaign: (brief: string) => request<Campaign>('POST', '/api/campaigns', { body: { brief } }),

  listCampaigns: (opts: { limit?: number; cursor?: string | null } = {}, signal?: AbortSignal) =>
    request<Page<CampaignSummary>>('GET', `/api/campaigns${query({ limit: opts.limit ?? 20, cursor: opts.cursor ?? undefined })}`, {
      signal,
    }),

  getCampaign: (id: string, signal?: AbortSignal) => request<Campaign>('GET', `/api/campaigns/${enc(id)}`, { signal }),

  generateSegment: (id: string, signal?: AbortSignal) =>
    request<{ campaign: Campaign; llm: LlmMeta }>('POST', `/api/campaigns/${enc(id)}/segment/generate`, { signal }),

  // Stateless: never saves (api.md §9.2). Called debounced by the rule editor.
  previewSegment: (rule: SegmentRule, campaignId?: string, signal?: AbortSignal) =>
    request<SegmentPreview>('POST', '/api/segment/preview', {
      body: campaignId ? { rule, campaign_id: campaignId } : { rule },
      signal,
    }),

  saveSegment: (id: string, rule: SegmentRule) =>
    request<{ campaign: Campaign }>('PUT', `/api/campaigns/${enc(id)}/segment`, { body: { rule } }),

  // v2 body (api.md §9.3): both variants in one save; `b` is null for a one-variant version.
  saveContent: (id: string, body: SaveContentBody, baseVersion: number) =>
    request<{ content: ContentVersion }>('PUT', `/api/campaigns/${enc(id)}/content`, {
      body: { base_version: baseVersion, variants: body.variants },
    }),

  validateContent: (req: ValidateRequest, signal?: AbortSignal) =>
    request<ValidationReport>('POST', '/api/content/validate', { body: req, signal }),

  // `variant` defaults to a on the server; `sendAt` (ISO 8601 with offset) schedules the send (api.md §9.6).
  approve: (
    id: string,
    channels: Channel[],
    contentVersion: number,
    idempotencyKey: string,
    opts: { variant?: SendVariant; sendAt?: string | null } = {},
  ) =>
    request<ApproveResult>('POST', `/api/campaigns/${enc(id)}/approve`, {
      body: {
        channels,
        content_version: contentVersion,
        variant: opts.variant ?? 'a',
        ...(opts.sendAt ? { send_at: opts.sendAt } : {}),
      },
      headers: { 'Idempotency-Key': idempotencyKey },
    }),

  cancel: (id: string) => request<CancelResult>('POST', `/api/campaigns/${enc(id)}/cancel`, { body: {} }),

  listMessages: (
    id: string,
    opts: { status?: string; channel?: Channel; limit?: number; cursor?: string | null } = {},
    signal?: AbortSignal,
  ) =>
    request<Page<MessageItem>>(
      'GET',
      `/api/campaigns/${enc(id)}/messages${query({
        status: opts.status,
        channel: opts.channel,
        limit: opts.limit ?? 50,
        cursor: opts.cursor ?? undefined,
      })}`,
      { signal },
    ),

  systemStatus: (signal?: AbortSignal) => request<SystemStatus>('GET', '/api/system/status', { signal }),

  providerConfig: (signal?: AbortSignal) => request<ProviderConfig>('GET', '/api/system/provider-config', { signal }),

  saveProviderConfig: (patch: Partial<ProviderConfig>) =>
    request<ProviderConfig>('PUT', '/api/system/provider-config', { body: patch }),
}

// crypto.randomUUID exists only in secure contexts (https, localhost); over plain http on another host
// (http://api:8080 in the e2e container, a LAN address) approve would throw. getRandomValues works everywhere.
export function newIdempotencyKey(c: Pick<Crypto, 'getRandomValues'> & Partial<Pick<Crypto, 'randomUUID'>> = globalThis.crypto): string {
  if (typeof c.randomUUID === 'function') return c.randomUUID()
  const b = c.getRandomValues(new Uint8Array(16))
  b[6] = (b[6]! & 0x0f) | 0x40 // version 4
  b[8] = (b[8]! & 0x3f) | 0x80 // RFC 4122 variant
  const h = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}

export function eventsUrl(campaignId: string): string {
  return `/api/campaigns/${enc(campaignId)}/events`
}

export function generateUrl(campaignId: string): string {
  return `/api/campaigns/${enc(campaignId)}/content/generate`
}

export function refineUrl(campaignId: string): string {
  return `/api/campaigns/${enc(campaignId)}/content/refine`
}

export type { VariantKey }

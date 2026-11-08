// Typed client for docs/contracts/api.md. Same-origin; `fetch` is looked up at call time so tests can stub it.
import { ApiError } from './errors'
import type {
  ApproveResult,
  Campaign,
  CampaignSummary,
  Catalog,
  Channel,
  ContentInput,
  ContentVersion,
  ErrorBody,
  LlmMeta,
  MessageItem,
  Page,
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

  saveSegment: (id: string, rule: unknown) =>
    request<{ campaign: Campaign }>('PUT', `/api/campaigns/${enc(id)}/segment`, { body: { rule } }),

  saveContent: (id: string, input: ContentInput, baseVersion: number) =>
    request<{ content: ContentVersion }>('PUT', `/api/campaigns/${enc(id)}/content`, {
      body: { ...input, base_version: baseVersion },
    }),

  validateContent: (req: ValidateRequest, signal?: AbortSignal) =>
    request<ValidationReport>('POST', '/api/content/validate', { body: req, signal }),

  approve: (id: string, channels: Channel[], contentVersion: number, idempotencyKey: string) =>
    request<ApproveResult>('POST', `/api/campaigns/${enc(id)}/approve`, {
      body: { channels, content_version: contentVersion },
      headers: { 'Idempotency-Key': idempotencyKey },
    }),

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
}

export function newIdempotencyKey(): string {
  return globalThis.crypto.randomUUID()
}

export function eventsUrl(campaignId: string): string {
  return `/api/campaigns/${enc(campaignId)}/events`
}

export function generateUrl(campaignId: string): string {
  return `/api/campaigns/${enc(campaignId)}/content/generate`
}

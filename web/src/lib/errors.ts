// Maps the API error model (api.md §1) and transport failures to words a marketer can act on.
import type { ErrorBody, ErrorDetail } from './types'

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: ErrorDetail[]

  constructor(status: number, code: string, message: string, details: ErrorDetail[] = []) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
  }

  static fromBody(status: number, body: ErrorBody | null): ApiError {
    const e = body?.error
    return new ApiError(status, e?.code ?? `http_${status}`, e?.message ?? '', e?.details ?? [])
  }
}

export interface UserMessage {
  // The API error code (or `network`), for callers that offer a code-specific action.
  code: string
  title: string
  message: string
  // One human-readable line per detail from a 422.
  details: string[]
  // Whether trying the same action again can help.
  retryable: boolean
}

function detailLine(d: ErrorDetail): string {
  const where = d.path ? `${d.path}: ` : ''
  return `${where}${d.message ?? d.code}`
}

const MAP: Record<string, (e: ApiError) => Omit<UserMessage, 'details' | 'code'>> = {
  bad_request: () => ({
    title: 'The request was not valid',
    message: 'Something in the request was missing or malformed. Check the fields and try again.',
    retryable: false,
  }),
  idempotency_key_required: () => ({
    title: 'Approval could not start',
    message: 'The approval request was missing its safety key. Reload the page and approve again.',
    retryable: true,
  }),
  not_found: () => ({
    title: 'Campaign not found',
    message: 'This campaign does not exist or was removed. Go back to the campaign list.',
    retryable: false,
  }),
  invalid_state: () => ({
    title: 'Not allowed in the current status',
    message: 'The campaign is in a status where this action is not possible. Reload to see where it is now.',
    retryable: false,
  }),
  stale_content_version: () => ({
    title: 'The content changed in the meantime',
    message: 'A newer version of the content exists. Reload the campaign to see it, then re-apply your edits.',
    retryable: false,
  }),
  approve_in_progress: () => ({
    title: 'Approval already in progress',
    message: 'Another approval for this campaign is running right now. Wait a moment and reload.',
    retryable: true,
  }),
  already_approved: () => ({
    title: 'Already approved',
    message: 'This campaign was approved earlier. Open its dashboard to follow delivery.',
    retryable: false,
  }),
  validation_failed: () => ({
    title: 'Some input is not valid',
    message: 'Fix the problems listed below and try again.',
    retryable: false,
  }),
  llm_invalid_output: () => ({
    title: 'The AI answer could not be used',
    message:
      'Claude’s answer still failed our checks after automatic repairs, so nothing was saved. Try again, or write the text yourself.',
    retryable: true,
  }),
  llm_refused: (e) => {
    const cat = e.details[0]?.code ?? 'unknown'
    const reason = cat === 'unknown' ? 'no reason given' : cat
    return {
      title: 'Claude declined this request',
      message: `The model refused (${reason}). Rephrase the brief and try again.`,
      retryable: false,
    }
  },
  content_invalid: () => ({
    title: 'The content is not valid for a selected channel',
    message: 'Fix the content errors (or deselect the channel) before approving.',
    retryable: false,
  }),
  empty_audience: () => ({
    title: 'A selected channel has nobody to send to',
    message: 'No one in this audience has given consent for a selected channel. Deselect it or widen the audience.',
    retryable: false,
  }),
  llm_failed: () => ({
    title: 'The AI service did not answer',
    message: 'The language model failed or timed out. Nothing was changed; try again in a moment.',
    retryable: true,
  }),
  unavailable: () => ({
    title: 'The database is unavailable',
    message: 'The service cannot reach its database right now. Try again shortly.',
    retryable: true,
  }),
  internal: () => ({
    title: 'Something broke on our side',
    message: 'The server hit an unexpected error. Try again; if it persists, check the API logs.',
    retryable: true,
  }),
  network: () => ({
    title: 'Cannot reach the server',
    message: 'The request did not get through. Check that the API is running and your connection is up.',
    retryable: true,
  }),
}

export function describeError(err: unknown): UserMessage {
  if (err instanceof ApiError) {
    const build = MAP[err.code]
    const details = err.details.map(detailLine)
    if (build) return { ...build(err), code: err.code, details }
    return {
      code: err.code,
      title: `Request failed (${err.status})`,
      message: err.message || 'The server rejected the request.',
      details,
      retryable: err.status >= 500,
    }
  }
  if (err instanceof Error && err.name === 'AbortError') {
    return { code: 'aborted', title: 'Cancelled', message: 'The request was cancelled.', details: [], retryable: true }
  }
  return {
    code: 'unexpected',
    title: 'Unexpected error',
    message: err instanceof Error ? err.message : String(err),
    details: [],
    retryable: false,
  }
}

export const KNOWN_ERROR_CODES = Object.keys(MAP)

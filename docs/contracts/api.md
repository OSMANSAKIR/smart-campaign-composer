# Contract: HTTP API and SSE events

Status: v1 · 2026-11-08 · owner: orchestrator · implements: R-FLOW-1..5, R-OBS-1, R-OBS-3, R-SND-1, R-SND-3

Base: same origin as the SPA (`http://localhost:8080`), JSON in UTF-8, no CORS. Timestamps are RFC 3339 UTC.
IDs: campaigns and messages are UUIDs, users are integers.

## 1. Error model

Every non-2xx JSON response:

```json
{ "error": { "code": "validation_failed", "message": "Human-readable summary.",
             "details": [ { "path": "sms_text", "code": "too_long", "message": "…" } ] } }
```

| HTTP | `code` | When |
|---|---|---|
| 400 | `bad_request` | Malformed JSON, missing fields, bad query params |
| 400 | `idempotency_key_required` | Approve without `Idempotency-Key` |
| 404 | `not_found` | Unknown campaign |
| 409 | `invalid_state` | Action not allowed in the campaign's current status |
| 409 | `stale_content_version` | `base_version` / `content_version` is not the latest |
| 409 | `approve_in_progress` | A concurrent approve holds the campaign |
| 409 | `already_approved` | Approved earlier with a different `Idempotency-Key` |
| 422 | `validation_failed` | Brief, rule or content invalid (`details` lists every problem) |
| 422 | `llm_invalid_output` | Model output still invalid after 2 repairs (`details` = last errors) |
| 422 | `llm_refused` | Model refused (`details[0].code` = refusal category or `unknown`) |
| 422 | `content_invalid` | Approve with content not valid for a selected channel |
| 422 | `empty_audience` | Approve when a selected channel has 0 reachable recipients |
| 502 | `llm_failed` | Provider error or timeout after SDK retries |
| 503 | `unavailable` | Database unavailable |
| 500 | `internal` | Bug; logged with a request id |

## 2. Objects

**LLM metadata** (`llm`), returned by every generation:

```json
{ "provider": "anthropic", "model": "claude-sonnet-5-5", "prompt_version": "segment_v1",
  "latency_ms": 2140, "attempts": 1, "repaired": false,
  "input_tokens": 1830, "output_tokens": 212, "cache_read_tokens": 1600, "cache_write_tokens": 0,
  "cost_usd": 0.00271 }
```

**Audience:** `{ "total": 231, "sms_reachable": 160, "email_reachable": 185, "both": 128, "counted_at": "…" }`
— `*_reachable` counts only users with consent for that channel.

**Segment** (on the campaign):

```json
{ "version": 1, "source": "llm", "rule": { "groups": [ … ] },
  "rendered": { "en": ["City is İstanbul", "…"], "tr": ["Şehir: İstanbul", "…"] },
  "unsupported_criteria": [], "assumptions": ["…"], "explanation": "…",
  "channels_requested": "both", "language": "tr",
  "warnings": [ { "path": "segment", "code": "targets_everyone", "message": "…" } ],
  "audience": { … } }
```

`source` is `llm` or `edited`.

**Validation report** (content):

```json
{ "valid": false,
  "valid_for": { "sms": false, "email": true },
  "fields": {
    "sms_text": { "runes": 151, "worst_case_runes": 169, "limit": 160,
                  "encoding": "ucs2", "utf16_units": 169, "segments": 3 },
    "email_subject": { "runes": 41, "worst_case_runes": 59, "limit": 80 },
    "email_body": { "runes": 640, "limit": 5000 } },
  "worst_case": { "first_name": "Abdurrahman", "city": "Eskişehir" },
  "variables": ["first_name"],
  "errors": [ { "path": "sms_text", "code": "too_long", "message": "…" } ],
  "warnings": [ { "path": "email_body", "code": "contains_url", "message": "…" } ] }
```

- `encoding`, `utf16_units`, `segments` describe the **worst-case rendered** SMS (display only).
- Error codes: `required`, `too_long`, `multiline_subject`, `control_character`, `unknown_variable`,
  `malformed_variable`. Warning codes: `contains_url`, `contains_email`, `contains_phone`.
- Worst case: each variable is replaced by the longest value among the audience's consented users for that
  channel, capped by the fallback rule (need.md N-ORC-005).

**Content version:** `{ "version": 2, "source": "llm"|"edited", "sms_text": "…", "email_subject": "…",
"email_body": "…", "validation": { report }, "created_at": "…" }`

**Campaign:**

```json
{ "id": "…", "brief": "…", "status": "content_ready", "language": "tr",
  "segment": { … } | null, "content": { latest content version } | null,
  "channels": [], "approved_content_version": null,
  "created_at": "…", "approved_at": null, "completed_at": null }
```

**Campaign summary** (list item): `{ "id", "brief", "status", "language", "channels": [], "audience_total": 231 | null,
"created_at", "approved_at", "completed_at" }`.

## 3. State machines

Every transition is a guarded `UPDATE … WHERE status = $expected`; 0 rows → 409 `invalid_state`.

Campaign: `draft → segmented → content_ready → sending → completed | completed_with_failures`.
- A new or edited segment from `segmented` or `content_ready` moves the campaign to `segmented`; the latest
  content must then be re-validated (new version via PUT, or regenerated) before approve.
- `sending → completed*` is done by the worker's reconciler once no message is in a non-terminal state;
  `completed_with_failures` when at least one message is `failed`.

Message: `queued → sending → sent | retry | failed`; `retry → sending → …`; at claim time a message whose
user no longer has consent becomes `skipped_no_consent`. A `sending` message whose lease expired is reclaimable.
Terminal: `sent`, `failed`, `skipped_no_consent`.

## 4. Endpoints

| Method + path | Request | Success | Notes |
|---|---|---|---|
| `GET /api/catalog` | — | 200 catalog | Fields, types, ops, enum values, labels, limits, variables, content limits |
| `POST /api/campaigns` | `{ "brief": "…" }` (1..4000 runes) | 201 campaign | status `draft` |
| `GET /api/campaigns` | `?limit=20&cursor=` (limit 1..100) | 200 `{ items: [summary], next_cursor }` | Newest first; `next_cursor` is `null` on the last page |
| `GET /api/campaigns/{id}` | — | 200 campaign (+ `progress` snapshot when sending or done) | |
| `POST /api/campaigns/{id}/segment/generate` | — | 200 `{ campaign, llm }` | Allowed in `draft`, `segmented`, `content_ready` |
| `PUT /api/campaigns/{id}/segment` | `{ "rule": { "groups": [...] } }` | 200 `{ campaign }` | Manual edit; same validator |
| `POST /api/campaigns/{id}/content/generate` | `{}` | 200 `text/event-stream` (§5) | Allowed in `segmented`, `content_ready` |
| `PUT /api/campaigns/{id}/content` | `{ sms_text, email_subject, email_body, base_version }` | 200 `{ content }` | Saves a new `edited` version even if invalid; `base_version` must be the latest (0 when none) |
| `POST /api/content/validate` | `{ sms_text, email_subject, email_body, campaign_id? , language? }` | 200 report | Stateless; worst case from the campaign's audience, else from all users |
| `POST /api/campaigns/{id}/approve` | header `Idempotency-Key`; `{ "channels": ["sms","email"], "content_version": 2 }` | 202 `{ campaign, enqueued: { sms, email } }` | §6 |
| `GET /api/campaigns/{id}/messages` | `?status=&channel=&limit=50&cursor=` (limit 1..500) | 200 `{ items, next_cursor }` | Items: `id, user_id, channel, status, attempts, last_error, sent_at, updated_at` |
| `GET /api/campaigns/{id}/events` | — | 200 `text/event-stream` (§7) | Live progress |
| `GET /api/system/status` | — | 200 status | §8 |
| `GET /healthz`, `GET /readyz`, `GET /metrics` | — | | See `observability.md` |

## 5. Generation stream (`POST …/content/generate`)

Pre-stream failures (404, 409) are normal JSON errors. Once the stream starts (HTTP 200,
`Content-Type: text/event-stream`), events are (need.md N-ORC-002):

| Event | `data` | Meaning |
|---|---|---|
| `meta` | `{ provider, model, prompt_version }` | First event |
| `delta` | `{ "text": "<fragment>" }` | Fragments of the model's JSON text for the current attempt, concatenated in order. Display only; may end mid-token |
| `repairing` | `{ attempt, errors: [...] }` | The previous attempt was invalid; the client clears its partial buffer and a new attempt streams |
| `result` | `{ content: { content version incl. validation }, llm }` | Canonical, server-validated, saved as a new `llm` version. Last event |
| `error` | `{ error: { error model } }` | Terminal failure (`llm_refused`, `llm_invalid_output`, `llm_failed`). Last event |

Closing the request cancels the upstream LLM call. The browser reads the stream with `fetch`; `EventSource`
is not used here because it only supports GET.

## 6. Approve (R-FLOW-3, R-SND-1, R-SND-3, R-AUD-2)

- `Idempotency-Key` header (1..200 chars) is required → 400 otherwise.
- One transaction: guarded `content_ready → sending`, store key and response, then
  `INSERT INTO messages … SELECT … FROM users u WHERE <compiled rule> AND u.<channel>_consent
  ON CONFLICT (campaign_id, user_id, channel) DO NOTHING` per selected channel.
- `content_version` must be the latest version (409 `stale_content_version`) and valid for every selected
  channel (422 `content_invalid`). A selected channel with 0 reachable users → 422 `empty_audience`.
- Same key again → the stored 202 response. Different key after approval → 409 `already_approved`.
  Concurrent approve → exactly one wins; the others get 409.
- Target: < 200 ms for 5k recipients; sending happens in the worker.

## 7. Campaign progress stream (`GET …/events`)

| Event | `data` |
|---|---|
| `snapshot` | Full progress object (below). Sent first on every connection, then whenever it changes (≤ every 500 ms) |
| `event` | `{ at, message_id, channel, attempt, outcome, http_status, error_class }` — recent attempts, best effort |
| `done` | `{ status }` — campaign reached a terminal status; the server closes the stream |

Progress object:

```json
{ "campaign_id": "…", "status": "sending",
  "channels": {
    "sms":   { "total": 160, "queued": 40, "sending": 12, "sent": 100, "retry": 5, "failed": 1, "skipped_no_consent": 2,
               "throughput_per_s": 98.4, "paused_until": null },
    "email": { … } },
  "errors": { "rate_limited": 3, "server_error": 7, "timeout": 1, "permanent": 1, "auth_error": 0 },
  "eta_seconds": 12, "started_at": "…", "updated_at": "…" }
```

`errors` counts failed attempts by class: 429 → `rate_limited`, 5xx → `server_error`, timeouts and connection
errors → `timeout`, other 4xx → `permanent`, 401/403 (channel paused, retried) → `auth_error`.

Every `snapshot` is complete, so a reconnecting client simply replaces its state (need.md N-ORC-006).
Counts are monotonic in the sense that terminal counts never decrease. A `: ping` comment is sent every 15 s.

## 8. System status (`GET /api/system/status`)

```json
{ "api": { "status": "ok", "version": "dev" },
  "db": { "status": "ok", "latency_ms": 2 },
  "worker": { "status": "ok" | "stale" | "down",
              "dispatchers": [ { "worker_id": "…", "channel": "sms", "holds_lock": true, "paused_until": null, "last_seen": "…" } ] },
  "provider": { "status": "ok" | "down", "url": "http://mockprovider:8090" },
  "llm": { "provider": "mock", "model": "mock-demo-v1", "mock": true } }
```

`worker.status`: `ok` when each channel has a lock holder seen in the last 10 s, `stale` when the last heartbeat
is older, `down` when there is none.

# Contract: HTTP API and SSE events

Status: v2 · 2026-11-10 · owner: orchestrator · implements: R-FLOW-1..5, R-OBS-1, R-OBS-3, R-SND-1, R-SND-3,
B-1 (scheduled send), B-2 (A/B variants). v2 adds §9 (phase 2); v1 shapes stay valid unless §9 says otherwise.

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
| 422 | `variant_missing` | Approve or refine names variant `b` (or `ab`) on a version that has no variant B |
| 422 | `invalid_send_at` | `send_at` outside now + 1 min .. now + 7 days (§9.6) |
| 502 | `llm_failed` | Provider error or timeout after SDK retries |
| 502 | `llm_provider_rejected` | The LLM provider answered 4xx other than 408/409/429 (key, permissions, credits/billing). Fixed message, never the provider's body (§9.8) |
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
  "send_variant": null, "scheduled_at": null, "cancelled_at": null,
  "created_at": "…", "approved_at": null, "completed_at": null }
```

`send_variant` (`a` | `b` | `ab` | null before approve), `scheduled_at`, `cancelled_at`: v2, §9.

**Campaign summary** (list item): `{ "id", "brief", "status", "language", "channels": [], "audience_total": 231 | null,
"created_at", "approved_at", "completed_at" }`.

## 3. State machines

Every transition is a guarded `UPDATE … WHERE status = $expected`; 0 rows → 409 `invalid_state`.

Campaign: `draft → segmented → content_ready → sending → completed | completed_with_failures`.
v2: `content_ready → scheduled → sending` when approved with `send_at` (§9.6); `scheduled | sending →
cancelled` (§9.5). Terminal: `completed`, `completed_with_failures`, `cancelled`.
- A new or edited segment from `segmented` or `content_ready` moves the campaign to `segmented`; the latest
  content must then be re-validated (new version via PUT, or regenerated) before approve.
- `sending → completed*` is done by the worker's reconciler once no message is in a non-terminal state;
  `completed_with_failures` when at least one message is `failed`.

Message: `queued → sending → sent | retry | failed`; `retry → sending → …`; at claim time a message whose
user no longer has consent becomes `skipped_no_consent`. A `sending` message whose lease expired is reclaimable.
Terminal: `sent`, `failed`, `skipped_no_consent`, and (v2) `cancelled`: `queued | retry → cancelled` when the
campaign is cancelled.

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
| `GET /api/system/provider-config` | — | 200 the mock provider's fault config (provider-api.md §3) | 502 `provider_unavailable` when the provider does not answer |
| `PUT /api/system/provider-config` | any subset of the config fields | 200 merged config | Forwarded to the provider's `/admin/config`; unknown fields or bad values → 400 `validation_failed` (need.md N-WEB-003) |
| `POST /api/segment/preview` | `{ "rule": {…} }` | 200 preview (§9.2) | v2. Stateless; never saves |
| `POST /api/campaigns/{id}/content/refine` | `{ "variant": "a"\|"b", "instruction": "…", "base_version": 3 }` | 200 `text/event-stream` (§9.4) | v2. Allowed in `segmented`, `content_ready` |
| `POST /api/campaigns/{id}/cancel` | `{}` | 200 `{ campaign, cancelled }` (§9.5) | v2. Allowed in `scheduled`, `sending`; idempotent in `cancelled` |
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
errors → `timeout` (including `unreachable`: no connection was made, the channel pauses and the try is not
counted), other 4xx → `permanent`, 401/403 (channel paused, retried) → `auth_error`.

Every `snapshot` is complete, so a reconnecting client simply replaces its state (need.md N-ORC-006).
Counts are monotonic in the sense that terminal counts never decrease. A `: ping` comment is sent every 15 s.

## 8. System status (`GET /api/system/status`)

```json
{ "api": { "status": "ok", "version": "dev" },
  "db": { "status": "ok", "latency_ms": 2 },
  "worker": { "status": "ok" | "stale" | "down",
              "dispatchers": [ { "worker_id": "…", "channel": "sms", "holds_lock": true, "paused_until": null, "last_seen": "…", "stale": false } ] },
  "provider": { "status": "ok" | "down", "url": "http://mockprovider:8090" },
  "llm": { "provider": "mock", "model": "mock-demo-v1", "mock": true } }
```

`worker.status`: `ok` when each channel has a lock holder seen in the last 10 s, `stale` when the last heartbeat
is older, `down` when there is none. A dispatcher row has `stale: true` when its heartbeat is older than 10 s;
rows older than 5 minutes belong to a worker that is gone (a crash skips the delete on shutdown) and are omitted.
Ages use the database clock.

## 9. Phase 2 (v2, owner decisions 2026-11-10)

Migration: `internal/store/migrations/20261110230000_phase2.sql`. Statuses, columns and purposes below exist
there; nothing else changes in the schema.

### 9.1 Catalog (`GET /api/catalog`, need.md N-WEB-001 → B)

The response is `catalog.Doc()` (internal/catalog); every value the rule editor offers comes from it:

```json
{ "fields": [
    { "name": "city", "type": "enum", "label": { "en": "City", "tr": "Şehir" }, "description": "…",
      "values": ["İstanbul", "…"], "nullable": false, "ops": ["eq", "neq", "in", "not_in"] },
    { "name": "age", "type": "number", "label": {…}, "description": "…", "min": 18, "max": 100,
      "integer": true, "nullable": false, "unit": "years", "ops": ["eq", "neq", "gt", "gte", "lt", "lte", "between"] },
    { "name": "cart_abandoned", "type": "bool", "ops": ["is_true", "is_false"], … },
    { "name": "last_purchase_at", "type": "date", "nullable": true,
      "ops": ["within_last_days", "not_within_last_days", "before", "after", "is_null", "is_not_null"], … } ],
  "operators": { "number": […], "enum": […], "bool": […], "date": […] },
  "limits": { "max_groups": 5, "max_conditions_per_group": 10, "max_conditions_total": 20, "max_list_items": 50,
              "min_days": 1, "max_days": 3650, "max_string_runes": 100, "min_date": "…", "max_date": "…" },
  "variables": ["first_name", "city"],
  "content_limits": { "sms_text": 160, "email_subject": 80, "email_body": 5000, "variable_value": 20 },
  "channels": ["sms", "email"] }
```

Field keys: `name`, `type` (`number` | `enum` | `bool` | `date`), `label` {en, tr}, `description`, `ops`,
`nullable`; `values` (enum only); `min`, `max`, `integer`, `unit` (number only, when set). Value shape per
operator is segment-dsl.md: scalar for `eq/neq/gt/gte/lt/lte`, `[lo, hi]` for `between`, a list for
`in/not_in`, an integer day count for `*_within_last_days`, `YYYY-MM-DD` for `before/after`, `null` for
`is_true/is_false/is_null/is_not_null`. The catalog is the editor's convenience only; the server validator is
the authority.

### 9.2 Manual rule editing (F2)

- `POST /api/segment/preview` `{ "rule": { "groups": […] } }` → 200, never saves, never 422:
  `{ "valid": true, "errors": [], "warnings": [], "rendered": { "en": […], "tr": […] }, "audience": { … } | null }`.
  Same validator as model output (`segment.Validate`); `audience` is null when invalid. The UI calls it
  debounced (≥ 300 ms) while the marketer edits.
- `PUT /api/campaigns/{id}/segment` (v1 endpoint) saves the edit. v2 additions on the stored segment:
  `"source": "edited"`, `"original_rule": { Claude's rule } | null` and `"edited_at": "…" | null`.
  `original_rule` is the last model-generated rule and survives further edits; a new generation clears it.
- Content generation and approve use the edited rule (they read the stored rule).

### 9.3 A/B variants (F1, B-2)

**Content version** (v2): every version has variant `a`; a two-variant version also has `b`.

```json
{ "version": 3, "created_at": "…",
  "variants": {
    "a": { "label": "Friendly reminder", "source": "llm", "instruction": null,
           "sms_text": "…", "email_subject": "…", "email_body": "…", "validation": { report } },
    "b": { "label": "Urgency / benefit", "source": "refined", "instruction": "daha kısa",
           "sms_text": "…", "email_subject": "…", "email_body": "…", "validation": { report } } | null },
  "source": "llm", "sms_text": "…", "email_subject": "…", "email_body": "…", "validation": { report } }
```

The top-level `source`, `sms_text`, `email_subject`, `email_body`, `validation` mirror variant `a` (v1
compatibility). `source` per variant: `llm` | `edited` | `refined`.

- **Generation** (`POST …/content/generate`) returns exactly two variants in one streamed call, prompt
  `content_v2`. Each variant is validated on its own with the v1 rules (160 worst case, subject ≤ 80 single
  line, variables); any invalid variant makes the whole answer invalid and goes through the repair loop. The
  `result` event carries the saved two-variant version.
- **Wire schema `content_v2`** (generated into `docs/contracts/content.wire.schema.json`):
  `{ "language": "tr"|"en", "variants": { "a": V, "b": V }, "notes": "…" }` with
  `V = { "label": "…", "sms": { "text": "…" }, "email": { "subject": "…", "body": "…" } }`, all required,
  `additionalProperties: false`. Fixed keys `a`/`b` make "exactly two" a schema property. `label` ≤ 40 code
  points (validator; `label_too_long` error).
- **Saving** (`PUT …/content`): v2 body `{ "base_version": 3, "variants": { "a": E, "b": E | null } }` with
  `E = { label, sms_text, email_subject, email_body }`; saves a new version (both rows), even when invalid,
  `source: "edited"` on a row whose text or label changed, else the row keeps its source and instruction.
  The v1 body (`sms_text`, `email_subject`, `email_body`, `base_version`) still works and saves a one-variant
  version. Sending both shapes → 400.
- **Approve** (v2 body): `{ "channels": […], "content_version": 3, "variant": "a" | "b" | "ab",
  "send_at"?: "…" }`; `variant` defaults to `a`. Every variant that will be sent must be valid for every
  selected channel (422 `content_invalid`, detail paths prefixed `variants.b.`). `b`/`ab` on a version without
  B → 422 `variant_missing`. Stored as `campaigns.send_variant`.
- **Split** (`ab`): variant of a user = `get_byte(decode(md5(campaign_id::text || ':' || user_id::text), 'hex'), 0) % 2`
  (0 → `a`, 1 → `b`), computed in the enqueue `INSERT … SELECT`. Deterministic, independent of the channel
  (a user gets the same variant on SMS and email) and recomputable outside Postgres. Consent rules unchanged.
  Every message records `variant`; the worker renders the message's variant (`campaign_content` row
  `(campaign_id, content_version, variant)`).
- **Message list items** gain `variant`.
- **Progress** (§7) gains per-variant counts per channel, present for every variant that has messages:

  ```json
  "channels": { "sms": { …v1 counters…, "cancelled": 0,
                         "variants": { "a": { "total": 80, "sent": 70, "failed": 1, "cancelled": 0, "skipped_no_consent": 0 },
                                       "b": { … } } } }
  ```

  For each channel, the per-variant `total`s add up to the channel `total`, and so do `sent`, `failed`,
  `cancelled`, `skipped_no_consent`. There are no open or click metrics: the mock provider has none; this is
  the split and per-variant delivery foundation.

### 9.4 Refine with an instruction (F3)

`POST /api/campaigns/{id}/content/refine` `{ "variant": "a"|"b", "instruction": "…", "base_version": 3 }`.

- `instruction`: 1..500 code points, control characters rejected (400 `validation_failed`, path
  `instruction`). `base_version` must be the latest (409 `stale_content_version`).
- Same stream as §5 (`meta`, `delta`, `repairing`, `result` | `error`); prompt `refine_v1`; `llm_calls.purpose`
  `refine` (its repairs `repair`).
- Claude rewrites the named variant only. The instruction is data inside the prompt (escaped, in its own
  tag) and cannot relax any rule: the answer goes through the same validation and repair loop, so "ignore the
  160 limit" still yields ≤ 160 or an error.
- Wire schema `refine_v1`: `{ "language", "label", "sms": { "text" }, "email": { "subject", "body" }, "notes" }`.
- Result: a new version where the named variant is the rewrite (`source: "refined"`, `instruction` stored)
  and the other variant is copied unchanged. Earlier versions are kept (history).

### 9.5 Cancel (F4)

`POST /api/campaigns/{id}/cancel` `{}` → 200 `{ "campaign": {…}, "cancelled": 42 }`.

- One transaction: guarded `scheduled | sending → cancelled` (sets `cancelled_at`), then
  `UPDATE messages SET status = 'cancelled' WHERE campaign_id = $1 AND status IN ('queued', 'retry')`.
  `cancelled` is the number of messages moved.
- Already `cancelled` → 200 with `cancelled: 0` (idempotent). Other statuses → 409 `invalid_state`.
- The worker never claims a message of a campaign that is not `sending`. Messages already claimed
  (`sending`) at the cancel finish and record their outcome; a claimed message whose result is "retry", or
  whose lease expires, becomes `cancelled` instead of retrying. The bound (how many can still be sent after
  the cancel commits, per channel) is stated in DECISIONS.md and tested.
- The campaign `events` stream sends `done` once the campaign is `cancelled` and none of its messages is
  `sending`. At that point `sent + failed + cancelled + skipped_no_consent = total` per channel.

### 9.6 Scheduled send (F5, B-1)

- Approve with `send_at` (RFC 3339, any offset; stored UTC in `campaigns.scheduled_at`). Allowed window:
  server now + 50 s .. now + 7 days (the "1 minute" minimum minus 10 s for clock skew and request latency).
  Outside → 422 `invalid_send_at`.
- The approve transaction enqueues exactly as without `send_at` (audience frozen at approve) but moves the
  campaign `content_ready → scheduled`. A scheduler in the worker moves `scheduled → sending` when
  `scheduled_at <= now()` (database clock). Nothing is claimed before (the claim requires `sending`). Consent
  is re-checked at claim time as always (R-AUD-2), so a user who revokes consent while scheduled is skipped.
- Cancel works on `scheduled` campaigns (all their messages are still `queued`).
- The UI shows `scheduled_at` in the browser's local time with a countdown.

### 9.7 Cost-recorded flag (F6, need.md N-LLM-005 → CHANGE)

`llm` metadata (§2) gains `"cost_recorded": true | false`. It is `false` when any `llm_calls` insert of that
generation failed; the generation itself still succeeds. The UI shows a non-blocking notice: "The content
is ready, but its cost could not be recorded." (segment: "The audience is ready, but …").

### 9.8 Clear provider rejection (F7)

An LLM provider answer 4xx other than 408/409/429 (401/403, billing or credit errors, a rejected request)
ends the generation with 502 `llm_provider_rejected`, message "The AI provider rejected the request (check
the API key or credits).", no `details`. The raw provider body is never sent to the client (it can contain
account identifiers); it goes to the server log at warn level. 408/409/429/5xx/timeouts stay `llm_failed`.
The UI shows the message as is instead of "failed or timed out".

### 9.9 Acceptance suite (F8)

`make acceptance` runs `go test -tags acceptance ./acceptance/...` against the API, worker and mock provider
on a throwaway database, mock LLM by default (CI runs it). Live mode for the owner only:
`make acceptance PROVIDER=anthropic` with the spend flag, guarded like `make eval`; it writes
`docs/acceptance/REPORT-<provider>-<date>.md` and stops when the cumulative cost passes its cap. Scenarios
A1–A13 are listed in DECISIONS.md and in the suite.

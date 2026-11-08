# Contract: mock SMS/email provider

Status: v1 · 2026-11-08 · owner: orchestrator (implemented by delivery) · implements: R-RES-3, R-SND-2..4

Imitates a real messaging API. It injects latency, 5xx, 429 and hangs so retry and rate-limit logic can be
tested for real, and it independently measures what it received so invariants I1–I3 can be proven.
Server code: `internal/mockprovider` (usable in-process via `httptest`); binary: `cmd/mockprovider`, port 8090.

## 1. Send endpoints

`POST /v1/sms` and `POST /v1/email`, `Content-Type: application/json`, header `Idempotency-Key` (required).

```json
// /v1/sms
{ "to": "+905321234567", "text": "…", "campaign_id": "uuid", "user_id": 42, "message_id": "uuid" }
// /v1/email
{ "to": "ayse.yilmaz.42@example.com", "subject": "…", "html": "…", "text": "…",
  "campaign_id": "uuid", "user_id": 42, "message_id": "uuid" }
```

| Status | Body | Meaning | Client class (KICKOFF §7.3) |
|---|---|---|---|
| 200 | `{ "provider_message_id": "pm_…", "accepted_at": "…" }` | Accepted | success |
| 400 | `{ "error": { "code": "invalid_recipient" \| "missing_idempotency_key" \| "bad_request" } }` | `to` is not E.164 (`^\+[1-9][0-9]{7,14}$`) / not an email; header missing; bad JSON | permanent |
| 422 | `{ "error": { "code": "empty_content" } }` | Empty text / subject / body | permanent |
| 429 | `{ "error": { "code": "rate_limited" } }` + `Retry-After: <seconds>` | Random 429, or the provider's own limiter | throttled |
| 500 / 502 / 503 | `{ "error": { "code": "server_error" } }` | Random transient failure | transient |
| (none) | — | Hang: the response never comes; the client times out | ambiguous |

## 2. Idempotency

- Responses with status 200, 400 and 422 are stored per `Idempotency-Key`. The same key again returns the
  stored status and body and counts as a **replay**, not a new delivery.
- 429, 5xx and hangs are not stored; a retry with the same key is processed anew.
- Half of all hangs happen **after** the delivery was recorded (accepted, but the response is lost), so a
  retry must be deduplicated by the key. This is the "crash after send" case of KICKOFF §7.4.

## 3. Fault configuration

Env vars at start (see `.env.example`), changeable at runtime:

| Env | JSON field | Default | Meaning |
|---|---|---|---|
| `PROVIDER_LATENCY_MIN_MS` / `_MAX_MS` | `latency_min_ms` / `latency_max_ms` | 20 / 300 | Uniform latency |
| `PROVIDER_SPIKE_RATE` / `PROVIDER_SPIKE_MS` | `spike_rate` / `spike_ms` | 0.01 / 2000 | Occasional extra delay |
| `PROVIDER_ERROR_RATE` | `error_rate` | 0.02 | Fraction answered 500/502/503 |
| `PROVIDER_429_RATE` | `rate_429` | 0.01 | Fraction answered 429 |
| `PROVIDER_RETRY_AFTER_S` | `retry_after_s` | 1 | `Retry-After` value |
| `PROVIDER_HANG_RATE` | `hang_rate` | 0.005 | Fraction never answered |
| `PROVIDER_RATE_LIMIT` | `rate_limit` | 100 | Own limiter: max requests per channel in any 1 s window, excess → 429 (0 = off) |
| `PROVIDER_SEED` | `seed` | 0 | Fault RNG seed (0 = random) |

- `GET /admin/config` → current config. `POST /admin/config` with any subset of the JSON fields → merged
  config. Invalid values → 400.
- `POST /admin/reset` → clears stats, the idempotency store and limiter state (config is kept). For tests.

## 4. Stats (`GET /stats`)

```json
{ "since": "…",
  "channels": {
    "sms": {
      "requests": 5310,                      // every request received
      "accepted": 5000,                      // new deliveries (200, not replay)
      "unique_keys": 5000,
      "replayed_keys": 12,
      "duplicate_recipient_deliveries": 0,   // accepted deliveries beyond the first per (campaign_id, user_id)
      "max_requests_in_any_1s_window": 100,  // arrivals, incl. retries — invariant I3
      "max_deliveries_in_any_1s_window": 97, // accepted deliveries only
      "rejected_by_limiter": 0,
      "hangs": 25,
      "status_counts": { "200": 5012, "400": 3, "429": 51, "500": 40, "503": 12 } },
    "email": { … } },
  "total": { same fields summed; max_* are the max over channels } }
```

`GET /stats/delivered?channel=sms&campaign_id=…` → `{ "items": [ { "campaign_id", "user_id", "message_id",
"at" } ] }` — every accepted delivery, used for the consent cross-check (invariant I2).

## 5. Health and metrics

`/healthz`, `/readyz` (always ready once listening), `/metrics` (see `observability.md`).

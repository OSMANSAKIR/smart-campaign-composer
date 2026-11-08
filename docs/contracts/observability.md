# Contract: health, readiness, metrics, logs

Status: v1 · 2026-11-08 · owner: orchestrator · implements: R-OBS-1..3

## 1. Health and readiness

| Service | `/healthz` (liveness) | `/readyz` (readiness) |
|---|---|---|
| api :8080 | 200 while the process serves HTTP | 200 when the DB answers a ping within 1 s **and** all embedded migrations are applied; else 503 |
| worker :9091 | 200 while the process serves HTTP | 200 when the DB answers **and** migrations are applied **and** the dispatcher loop heartbeat is < 10 s old; else 503 |
| mockprovider :8090 | 200 | 200 |

Body: `{ "status": "ok" }` or `{ "status": "unavailable", "checks": { "db": "…", "migrations": "…", "dispatcher": "…" } }`.

The worker writes `worker_heartbeats(worker_id, channel, holds_lock, paused_until, at)` every 2 s;
`GET /api/system/status` reads it.

## 2. Metrics (Prometheus, `GET /metrics` on every service)

Definitions live in `internal/metrics`; services only use them. Names and labels are part of the contract
(a test scrapes `/metrics` and asserts them, R-OBS-2).

| Metric | Type | Labels | Emitted by |
|---|---|---|---|
| `messages_total` | counter | `channel`, `status` (sent, failed, skipped_no_consent, retry) | worker |
| `queue_depth` | gauge | `channel`, `status` (queued, retry, sending) | worker (sampled every 2 s) |
| `retries_total` | counter | `channel`, `class` (throttled, transient, ambiguous, unreachable) | worker |
| `provider_responses_total` | counter | `channel`, `code` (HTTP status, or `timeout`, `conn_error`, `unreachable` = no connection made) | worker |
| `send_latency_seconds` | histogram | `channel` | worker |
| `rate_limiter_wait_seconds` | histogram | `channel` | worker |
| `channel_paused` | gauge (0/1) | `channel` | worker |
| `content_too_long_total` | counter | `channel` | worker |
| `campaigns_approved_total` | counter | — | api |
| `messages_enqueued_total` | counter | `channel` | api |
| `llm_requests_total` | counter | `purpose`, `provider`, `model`, `outcome` (valid, repaired, invalid, refused, error) | api |
| `llm_tokens_total` | counter | `type` (input, output, cache_read, cache_write) | api |
| `llm_cost_usd_total` | counter | — | api |
| `llm_latency_seconds` | histogram | `purpose` | api |
| `llm_validation_failures_total` | counter | `reason` (error code) | api |
| `llm_repairs_total` | counter | — | api |
| `http_requests_total` | counter | `route`, `code` | api |
| `mockprovider_requests_total` | counter | `channel`, `code` | mockprovider |
| `mockprovider_duplicate_deliveries_total` | counter | `channel` | mockprovider |

Go runtime and process collectors are registered on every service.

## 3. Logs

`log/slog` JSON to stdout. Standard keys: `service`, `campaign_id`, `message_id`, `channel`, `attempt`,
`request_id`. Message bodies, names, emails and phone numbers are never logged at info level or above.
The API key is never logged at any level.

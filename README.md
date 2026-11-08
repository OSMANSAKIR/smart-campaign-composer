# Smart Campaign Composer

A marketer writes a campaign brief in plain language. Claude turns it into a **validated audience segment**
(with live counts from the database) and **streams** SMS and email copy. The marketer edits, previews with the
longest real names, and approves. Messages then go through a **Postgres queue** to a **mock SMS/email provider**
at no more than 100 per second per channel, and a dashboard shows delivery live.

Go backend · Vue 3 + TypeScript frontend · Postgres (datastore and queue) · synthetic users only · mock
provider only.

- Decisions and their reasons: [`DECISIONS.md`](DECISIONS.md)
- Decision log with the owner's answers: [`need.md`](need.md)
- API, SSE, DSL, provider and metric contracts: [`docs/contracts/`](docs/contracts/)
- How the AI agents were used: [`ai-sessions/README.md`](ai-sessions/README.md)

---

## Quick start

Requirements: Docker with Compose v2. Nothing else is needed for the app and its tests.

### Without an API key (mock LLM)

```bash
docker compose up        # or: make up   (always forces the mock LLM)
```

Open <http://localhost:8080>. The first build takes a few minutes (Go and Node images, multi-stage builds).
Compose runs migrations and seeds 5000 synthetic users, then starts the API, the worker and the mock provider.
With no key the app uses a deterministic **mock LLM** and shows a banner saying so; the whole flow still works.

### With a Claude API key

```bash
cp .env.example .env
# set ANTHROPIC_API_KEY=… in .env (optionally ANTHROPIC_MODEL; default claude-sonnet-5-5)
docker compose up
```

`LLM_PROVIDER=auto` (the default) uses the Claude API whenever a key is present. To check the real request
shape with one call per task before anything else (a fraction of a cent), run with the key in your shell:

```bash
set -a; . ./.env; set +a
make smoke-live CONFIRM_SPEND=yes
```

### Ports

| Service | URL |
|---|---|
| Web UI + API | <http://localhost:8080> (`/healthz`, `/readyz`, `/metrics`, `/api/…`) |
| Worker | <http://localhost:9091> or 9092 (`/healthz`, `/readyz`, `/metrics`); a port range so it can run as two replicas — `docker compose port worker 9091` shows which |
| Mock provider | <http://localhost:8090> (`/stats`, `/admin/config`, `/admin/reset`) |
| Postgres | `localhost:55432`, user/password/db `scc` |

### The demo in five steps

1. **New campaign** — the official example brief is prefilled
   (“Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul’daki kullanıcılara %30 indirim
   hatırlatması gönder. Samimi ama kısa olsun.”). Click **Find audience**.
2. **Audience** — the rule as the server understood it, in words (Şehir: İstanbul · Son 30 günde sepete ürün
   ekledi · Sepeti terk edilmiş), the counts per channel with consent, Claude's assumptions, and the LLM call
   details (model, latency, tokens, cost, prompt version).
3. **Content** — **Write with Claude** streams the SMS and email into the editor. Counters show the length
   with the longest name and city in this audience, the SMS encoding and segments. Edits are re-validated by
   the server and saved as new versions.
4. **Approve** — pick channels, confirm in the dialog. Approval is idempotent and enqueues in one transaction.
5. **Dashboard** — live progress per channel, throughput, ETA, provider errors by kind, and recent attempts.
   The **System** page shows the health of every component.

---

## Architecture

```
 Browser (Vue 3 SPA, served by the API)
   │  REST + SSE (generation stream, dashboard events)
   ▼
 api (Go)  ──────────────►  LLM provider interface ──► anthropic (Claude API, delivered)
   │  validate → compile        (prompts, validation,    mock (deterministic; tests, no key)
   │  ($n params only)           repair, llm_calls)      claudecode (dev-only, host)
   │
   │  approve: ONE transaction = guarded status change + INSERT … SELECT (rule AND consent)
   ▼
 Postgres ── campaigns · content_versions · users · messages (the queue) · delivery_attempts · llm_calls
   ▲
   │  claim: UPDATE … FOR UPDATE SKIP LOCKED, 30 s lease, consent re-checked
 worker (Go), one active dispatcher per channel (advisory lock; other replicas are hot standbys)
   │  sliding-window limiter ≤ 100 / channel / any 1 s · retry policy · Idempotency-Key = message id
   ▼
 mockprovider (Go) ── latency, 5xx, 429 + Retry-After, hangs, invalid numbers, own rate limiter,
                      idempotent by key, /stats proves duplicates = 0 and max per 1 s window
```

| Path | What lives there |
|---|---|
| `cmd/api`, `internal/api`, `internal/campaign` | HTTP API, campaign state machine, approve transaction, dashboard hub |
| `internal/llm` (+ `anthropic`, `mock`, `claudecode`, `prompts`, `eval`) | LLM service: prompts, validation pipeline, repair loop, call accounting |
| `internal/segment`, `internal/catalog` | Segment DSL: strict parse, validate, whitelist compile, render, count |
| `internal/content` | Character counting, variables, worst-case personalization, HTML rendering |
| `cmd/worker`, `internal/delivery` | Dispatcher, limiter, retry policy, leases, shutdown, heartbeats |
| `cmd/mockprovider`, `internal/mockprovider` | The fault-injecting SMS/email provider |
| `internal/store` | Migrations (embedded), per-test databases |
| `web/` | Vue app, Vitest unit tests, Playwright e2e |

### LLM design in short

- **The model proposes, the backend disposes.** Structured outputs with a wire schema generated from the field
  catalog; then strict decode, structural and semantic validation. Only the validator can create a
  `segment.ValidRule`, and only a `ValidRule` compiles to SQL — with every value as a `$n` parameter. Consent
  is not a field the model can use.
- **Repair loop:** validation errors go back to the model as structured feedback, at most 2 repairs, every call
  recorded in `llm_calls` (tokens, cache reads, cost, latency, prompt version). A refusal is shown, not repaired.
- **The UI shows what will run:** the human-readable rule is rendered by the server from the validated rule.
- **Content:** streamed over SSE; the browser's partial-JSON parse is display-only; the server's final result
  is canonical and validated (SMS ≤ 160 code points after worst-case personalization, subject ≤ 80, single
  line, only `{{first_name}}` and `{{city}}`, single-pass rendering, HTML escaping).
- Details and alternatives: DECISIONS D-1 … D-5.

---

## Tests and verification

| Command | What it runs | Needs |
|---|---|---|
| `make test` | `go vet`, `go test -race` for every package, Vitest — in containers, on a network **without internet**, no key | Docker |
| `make e2e` | Playwright: official brief → audience → streamed content → invalid edit blocked → edit saved → approve → dashboard finishes → reload | Docker |
| `make fresh-clone-check` | The reviewer's path: clean clone, no `.env`, `docker compose up`, seed, restart, `down -v`, key-only `.env`, `make test` | Docker |
| `make stress` | Delivery scenarios with real worker processes → [`docs/stress/REPORT.md`](docs/stress/REPORT.md); API scenarios → [`docs/stress/REPORT-api.md`](docs/stress/REPORT-api.md) | Docker (Postgres from `make up`), Go |
| `make eval PROVIDER=mock\|claudecode` | 17-brief eval set → `docs/eval/REPORT-*.md` | Go; `claudecode` needs the Claude Code CLI |
| `make lint` | golangci-lint + vue-tsc | Go, Node |
| `make sanitize-check` | gitleaks over git history and session exports, personal-term grep | gitleaks |

Test names carry requirement IDs (`TestLimiter_NeverMoreThanNInAnyWindow_RSND2`), so `go test -run RSND2 ./...`
runs every check for one requirement.

What the evidence says today (details in the reports):

- **Rate limit (R-SND-2):** live compose run at 100/s, all 5000 users, 7542 messages: the provider measured
  at most **100** requests in any 1 s window per channel, 0 rejections by its own limiter, **0 duplicate
  deliveries**, approve in 0.16 s, done in 72 s.
- **Delivery stress (S1 baseline, S2 chaos at 25 000 users / 49 463 messages with a worker killed mid-run, S7
  provider and database outages):**
  every invariant passes — no duplicates, no delivery without consent, ≤ 100 per window, every message terminal.
- **API stress:** S3(c) — one 5000-user approve returns in a median 128 ms. S3 — 100 racing approves of one campaign: exactly one wins, the rest get 409; ten all-user
  campaigns approved at once all succeed (75 420 messages, slowest 1.3 s in the committed run). S5 — 400 concurrent segment previews
  and content validations: no errors, p95 0.14 s with the 20-connection pool saturated. S6 — 50 live
  dashboards with 240 reconnects during a chaotic delivery: every one ends on the exact final counts, and no
  count ever goes backwards. S5b — previews at 100 000 users: p95 40 ms. S8 — the mock provider answers 500 rps.
- **Real model (claudecode, Sonnet 5.5):** 17-brief eval — 16/16 segments valid on the first try, 14/14 exact
  audiences (user-id sets), 16/16 contents valid; [`docs/eval/`](docs/eval/).
- **Red-team:** 45 hostile briefs and model answers through the mock adapter run in `make test`; no path from
  model text to SQL text.

---

## Configuration

Everything is optional; [`.env.example`](.env.example) lists every variable with its default. The ones that
matter most:

| Variable | Default | Meaning |
|---|---|---|
| `ANTHROPIC_API_KEY` | empty | Empty → mock LLM with a banner |
| `ANTHROPIC_MODEL` | `claude-sonnet-5-5` | Model for the anthropic adapter |
| `LLM_PROVIDER` | `auto` | `auto`, `anthropic`, `mock`, `claudecode` (dev, host only) |
| `RATE_LIMIT_PER_CHANNEL` | `100` | Max sends per channel in any 1 s window |
| `SEED_USERS` | `5000` | Synthetic users (deterministic, idempotent seed) |
| `PROVIDER_ERROR_RATE`, `PROVIDER_429_RATE`, `PROVIDER_HANG_RATE`, `PROVIDER_LATENCY_*` | small | Mock-provider faults; also changeable at runtime with `POST :8090/admin/config` |

---

## Limitations

- **The real Claude API path was not exercised during development** (no key; L-6). It is covered by contract
  tests with documented request/response fixtures and by `make smoke-live`; prompts and evals were validated
  against a real Sonnet model through the dev-only `claudecode` adapter.
- **Rate bound assumption:** the limiter guarantees ≤ 100 per window if one-way network delay plus provider
  decode stay under 25 ms (sub-millisecond on the compose network). An assumption-free variant is a one-line
  switch at ~84/s (DECISIONS D-7).
- **~4 s of silence** after a worker starts or takes over a channel, so a new lock holder cannot overlap the
  previous one's last window.
- **Characters are code points.** A Turkish SMS of 93 characters is valid but costs 2 UCS-2 segments; the UI
  shows encoding and segment count.
- **Delivered but recorded as failed (rare):** a message with an unanswered try gets 3 confirmation replays
  with the same key after its 5 attempts. If every one of those also hits a fault, it can end `failed` although
  the provider delivered it — once, never twice. See DECISIONS D-10.
- **Dashboard polling:** one aggregate query per running campaign every 500 ms; LISTEN/NOTIFY would be the
  next step for many concurrent campaigns.
- No authentication or multi-tenancy; the app is a single-marketer demo.

## Not done / next steps

Owner actions:

- One real Claude API run (`make smoke-live`, one eval with `PROVIDER=anthropic`) with a key.
- Push to GitHub; the CI workflow (`.github/workflows/ci.yml`: `make test`, `make lint`, `make e2e`) has not run
  there yet. The `00-planning` session export.

Known gaps (P2, from the CP3 spec audit in [`docs/review/traceability.md`](docs/review/traceability.md)):

- A "retry failed messages" button.
- Tests that would make existing evidence stronger: API `/readyz` returning 503 with the database down (proven
  for the worker in S7 only); a `/metrics` scrape asserting the API's `llm_*` series; one test from
  `GenerateSegment` to the stored audience compared with hand-written SQL (the parts are tested separately);
  the red-team NUL-brief case still reports through `t.Skipf` instead of failing.
- Stress checks: retry spacing from `delivery_attempts` timestamps ("no hot loop" is asserted by attempt
  counts only); worker-metric comparison in S2/S7 (a killed worker loses its counters, so I5 compares DB and
  provider there); a tighter goroutine bound in S6 (+10 allowed).
- The real-model eval has three adversarial briefs; more would make S4 stronger.
- `TestErrorModel_ROBS1` is named after R-OBS-1 but tests the error model.
- Dashboard updates by polling (500 ms per running campaign); LISTEN/NOTIFY would scale to many campaigns.

## Time spent

Europe/Istanbul time. Planning with the owner: 2026-11-08 00:00–02:40 (~2.7 h). Build and verification: first
commit 02:59, CP3 audit fixes finished 2026-11-09 ~03:00 — about 24 h of wall-clock time, including pauses at
the subscription's usage limits. Agent usage (API-equivalent, ccusage): about $73 over 8–9 November, almost all
cache reads (`ai-sessions/usage/`). The claudecode eval run cost about $0.35 API-equivalent; no paid API calls
were made during development.

# KICKOFF — Smart Campaign Composer

> Build brief for Claude Code. Written 2026-11-08 by the planning session together with the owner.
> This file is the single source of truth for **what** we build and **how we work**.
> `CLAUDE.md` (created in Phase 0) is the short, always-loaded summary; this file is read on demand.
> Decisions taken without the owner go to `need.md` (§4). Final, curated decisions go to `DECISIONS.md`.

## How to start (owner)

```bash
cd ~/projeler/smart-campaign-composer
claude -n 01-orchestrator --model opus --effort ultracode --permission-mode plan \
  "Read KICKOFF.md and need.md completely, then produce the Phase 0 plan described in §15."
```

- **`--effort ultracode`** sets effort to `xhigh` **and** turns on automatic workflow orchestration (§12.0). If you want `max` instead, launch with `--effort max` and then type `/effort ultracode`; this works on v2.1.284+ and keeps the level. Under ultracode, every workflow agent inherits that cost, so `xhigh` is the recommended level (need.md N-PLAN-013).
- Accept the workspace-trust prompt and the project plugins / MCP servers (context7, playwright) when asked.
- On a Pro plan, workflows are off by default; turn on "Dynamic workflows" in `/config` first. On Max they are on.
- Watch running workflows with `/workflows`. For routine steps (merges, small doc edits), `/effort ultracode off` saves usage.

---

## 0. Mission and how we are judged

Build an end-to-end tool. A marketer writes a campaign brief in natural language. Claude turns it into:

- (a) a validated audience segment;
- (b) SMS and email copy, streamed to the UI.

A human then previews, edits and approves the campaign. Messages go through an async queue to a mock provider, and a dashboard shows delivery live. No real person ever receives a message: recipients are synthetic seed users and the provider is our own mock.

**Scoring weights:** LLM integration 25 · Architecture & decisions 20 · Backend reliability 20 · Frontend 20 · Tests & code quality 15.

**What the evaluators said** (intent, translated): they do not score how much AI was used. They want to see how the problem was approached and how AI output was validated. The owner must understand and defend every line in a 30-minute review call. For you this means:

- **Prefer explainable designs.** Choose boring, explainable designs over clever ones. Every non-obvious choice gets a one-line *why*, either in a code comment or in DECISIONS.md.
- **Evidence over claims.** A requirement is done only when a test proves it and you have seen that test's output.
- **Solid core flow over breadth.** Unfinished items are listed honestly under README → "Not done / next steps".

**Time** (Europe/Istanbul):

| Milestone | When |
|---|---|
| Hard deadline | Tue 2026-11-10 16:00 |
| Submission target | Tue 14:00 |
| Code freeze | Tue 10:00 |
| **Build day** | Sun 2026-11-08: all P0 built end-to-end |
| **Verification days** | Mon 2026-11-09 and Tue morning: stress, red-team, evals, reviews, fixes, docs |

The planning session ended Sun 2026-11-08 ~02:00. The owner chose not to ask for extra time (N-PLAN-006). Sunday therefore favors **breadth of P0**, with one check test per requirement. Deep verification happens on Monday.

**Environment facts:**

- **Machine and toolchains:** macOS arm64, Go 1.27.0, Node 24, Docker.
- **Installed tools:** `gh`, `gitleaks`, `golangci-lint` v2, `ccusage`, `gopls`, `typescript-language-server`.
- **Missing commands:** there is no `timeout` command on macOS, so use Go/Bash alternatives. `tmux` is not installed.

---

## 1. Rules for every agent (orchestrator, builders, reviewers)

1. **Precedence.** Case requirements (§2) > locked decisions (§3) > contracts in `docs/contracts/` > accepted need.md entries > your judgement.
2. **Never block on the owner.** When you would normally ask, decide, log the decision in `need.md` (§4), and continue.
3. **Hard stops.** In these cases, ask in chat and wait:
   - **Secrets:** anything touching secrets. Never read, print or copy `.env` or any key or token.
   - **Outward-facing actions:** creating or pushing remotes, opening PRs, publishing anything, any call to a paid API.
   - **Destructive operations:** `git reset --hard`, force-push, deleting branches or worktrees that contain unmerged work, and `rm -rf` outside build or temp directories.
   - **Plan changes:** changing a locked decision (§3), or dropping or downgrading a P0 requirement.
4. **Verify, don't trust.** Run the test or command and quote the result. "Should work" is not a status.
5. **Log caught mistakes.** When you catch an AI mistake (yours or another agent's), append it to `ai-sessions/validation-log.md` (§13).
6. **Stay in your area.** Work only inside your ownership area (§12). If a change is needed elsewhere, add a need.md entry and notify the orchestrator.
7. **Facts come from sources, not memory.**
   - Invoke the **`claude-api` skill** before writing any Anthropic SDK code.
   - Use the **context7 MCP** for Vue, Vite, pgx, Prometheus and other library docs.
   - When docs and memory disagree, docs win.
8. **Commits.** Make small commits using Conventional Commits, with requirement IDs in the subject, e.g. `feat(delivery): sliding-window limiter [R-SND-2]`.
9. **Language.** Files, code, comments and commits are in English. Chat with the owner is in Turkish.
10. **Data.** The app only ever holds synthetic seed users. Never write the owner's personal information into repo files.

---

## 2. Requirements catalogue (from the case, with IDs)

Every requirement gets an ID. Tests, commits, need.md entries and the traceability matrix all refer to these IDs.

| ID | Requirement (case wording, translated) | Priority |
|---|---|---|
| R-FLOW-1 | Claude produces a segment rule from the brief; the UI shows how many people it targets | P0 |
| R-FLOW-2 | SMS and email content is generated and **streamed** to the screen | P0 |
| R-FLOW-3 | The marketer previews, edits if needed, and approves | P0 |
| R-FLOW-4 | Messages are sent through a queue to the mock provider | P0 |
| R-FLOW-5 | A dashboard shows the sending status | P0 |
| R-AUD-1 | The LLM's segment rule is validated in the backend; **LLM output is never executed directly as SQL** | P0 |
| R-AUD-2 | A user who has not consented to a channel is never messaged on that channel | P0 |
| R-CNT-1 | SMS ≤ 160 characters | P0 |
| R-CNT-2 | Email subject ≤ 80 characters | P0 |
| R-CNT-3 | Personalization variables such as `{{first_name}}` are supported | P0 |
| R-SND-1 | Asynchronous queue | P0 |
| R-SND-2 | At most 100 messages per second **per channel** | P0 |
| R-SND-3 | The same message never reaches the same person twice | P0 |
| R-SND-4 | Retry strategy (our own design) | P0 |
| R-OBS-1 | Live sending status | P0 |
| R-OBS-2 | Metrics endpoint | P0 |
| R-OBS-3 | Health check endpoint(s) | P0 |
| R-AI-1 | Claude's output is structured (JSON) and checked in the backend | P0 |
| R-AI-2 | Tests run without a real API key, using a mock LLM | P0 |
| R-RES-1 | The app reads the key from `ANTHROPIC_API_KEY` in `.env`; `.env.example` is in the repo | P0 |
| R-RES-2 | Seed of a few thousand fake users. Fields are our choice (name, email, phone, city, purchase history, channel consents, …) | P0 |
| R-RES-3 | Mock SMS/email provider imitating a real service; it sometimes returns errors, delays and **429** so retry and rate-limit logic can really be tested | P0 |
| R-TECH-1 | Frontend Vue.js; backend Go; DB, queue and libraries are our choice | P0 |
| R-DEL-1 | GitHub repository | P0 |
| R-DEL-2 | `docker compose up` installs everything with one command, including seed data and the mock provider | P0 |
| R-DEL-3 | `make test` runs the tests | P0 |
| R-DEL-4 | README.md: setup and a short architecture summary, plus unfinished items | P0 |
| R-DEL-5 | DECISIONS.md: important decisions and their reasons | P0 |
| R-DEL-6 | `ai-sessions/`: all Claude session exports, with API keys and personal info removed | P0 |
| B-1 | Bonus: scheduled sending | P2 |
| B-2 | Bonus: A/B variants | P2 |
| B-3 | Bonus: content in the user's language | P2 |
| B-4 | Bonus: CI pipeline | P2 (do first among bonuses) |

**Official example brief.** This is golden case #1, the default demo text, and the first e2e scenario:

> "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

**Priorities:**

- **P0:** everything needed for the core flow, plus one check test per requirement.
- **P1:** the stress suite and its report, the LLM eval report, Playwright e2e, the dashboard throughput chart, the mock-provider control panel and `make smoke-live`.
- **P2:** bonuses, in this order: B-4 → B-1 → B-3 → B-2. Start them only when P0 and P1 are green.

---

## 3. Locked decisions

These change only through the owner. If you believe one is wrong, write a need.md entry and keep following it.

| ID | Decision | Notes |
|---|---|---|
| L-1 | Backend in **Go** | Pin the same Go version in `go.mod` and the Dockerfiles, and verify the image tag exists. |
| L-2 | Frontend: **Vue 3 + TypeScript (strict) + Vite** | Pinia and vue-router are optional. No heavy UI kit is needed. |
| L-3 | All repo docs in **English** | Chat with the owner stays in Turkish. |
| L-4 | **Postgres** is both the datastore and the queue: transactional outbox plus `FOR UPDATE SKIP LOCKED` | No Redis or Kafka. Owner review pending (need.md N-PLAN-010). |
| L-5 | The LLM sits behind a provider interface with **three adapters**: `anthropic`, `mock`, `claudecode` | See §6.7–§6.9. `anthropic` is the delivered path; it uses the official Go SDK `github.com/anthropics/anthropic-sdk-go` and reads `ANTHROPIC_API_KEY`. `mock` is deterministic and used by all tests, and by compose when no key is set. `claudecode` is **dev-only**: it shells out to the owner's local Claude Code CLI on the owner's subscription with model Sonnet, runs on the host only, and is never used in tests, CI or as a default. |
| L-6 | The owner has **no API key** | The delivered product must still work with a real key out of the box: `cp .env.example .env`, set the key, `docker compose up`. Live verification of the `anthropic` adapter is impossible during development; mitigations are in §6.8. |
| L-7 | Delivered default model: **`claude-sonnet-5-5`**, overridable with `ANTHROPIC_MODEL` | Provisional; see need.md N-PLAN-001. |
| L-8 | Agent topology: orchestrator, 3 builder **sessions** in git worktrees, and read-only verifier subagents | See §12. |
| L-9 | The need.md protocol | See §4. |
| L-10 | No agent reads `.env` | Project settings deny it. Development uses `mock` or `claudecode`, so no key is needed anywhere. |

---

## 4. need.md protocol (the owner's control surface)

`need.md` lives at the repo root. It is the **asynchronous decision log and question queue** between the agents and the owner. The owner is not always online. Whenever you needed the owner but continued without them, the decision goes here so the owner can read it later and request a change.

### 4.1 When to write an entry

- You resolved a spec ambiguity by an assumption.
- You deviate from this KICKOFF: architecture, library, data model, API, UX flow or scope.
- A requirement cannot be fully met, or an item is cut or deferred.
- You chose between materially different options with product, UX, security or privacy impact (copy tone, defaults, limits).
- A reviewer would likely ask "why?" in the review call.
- Anything you would normally have asked the owner.

Do **not** log naming, internal refactors, test-helper design or formatting.

**Decision rule when unsure:** pick the option that is, in order:

1. safest for the hard requirements;
2. most reversible;
3. simplest to explain.

### 4.2 Entry format (copy exactly)

```markdown
### 🟡 N-<AGENT>-<NNN> · <short title> · impact: low|medium|high
- **Date / agent / session:** 2026-11-08 14:20 · builder-llm · 02-builder-llm
- **Requirement(s):** R-CNT-1
- **Context:** <why this came up, 1–3 lines>
- **Options:** A) … B) … C) … (one line each)
- **Decision taken:** B, because <reason>.
- **Cost to change later:** <files, effort, e.g. "~30 min, internal/content/length.go + tests">
- **Question for owner:** <one precise question, yes/no if possible>
- **Owner response:** _(owner writes ACCEPT, CHANGE: <instruction>, or DISCUSS)_
- **Status:** OPEN
```

**Agent codes:**

| Code | Agent |
|---|---|
| `PLAN` | planning session |
| `ORC` | orchestrator |
| `LLM` | LLM builder |
| `DEL` | delivery builder |
| `WEB` | web builder |
| `QA` | verifiers |

Numbers are per agent, so parallel worktrees never collide.

**Status values and header emoji:**

| Emoji | Status | Meaning |
|---|---|---|
| 🟡 | OPEN | Decided provisionally; owner review pending. |
| ❓ | QUESTION | No safe default exists; work continues around it. |
| ✅ | ACCEPTED / RESOLVED | The owner accepted it, or a requested change was implemented. |
| 🔁 | CHANGE REQUESTED | The owner asked for a change; not yet implemented. |

### 4.3 Lifecycle

1. **Read at session start.** Every agent reads need.md at session start and before each checkpoint.
2. **Owner wrote CHANGE.** The first agent owning that area implements it, sets the status to ✅ RESOLVED with the commit hash, and updates DECISIONS.md.
3. **Owner wrote ACCEPT.** Set the status to ✅ ACCEPTED. The orchestrator promotes significant decisions to DECISIONS.md.
4. **Digest.** The orchestrator alone maintains the "Digest" section at the top: open high-impact items first. Builders only append entries.
5. **Merging.** `.gitattributes` sets `need.md merge=union` and `ai-sessions/validation-log.md merge=union`, so appends from parallel worktrees merge without conflicts. After merging, the orchestrator checks for duplicate or half-merged entries.
6. **Hard stops (§1.3) are different.** They go to chat and wait for the owner. Log them in need.md as well.

---

## 5. Architecture

### 5.1 Overview

```
 Browser (Vue 3 SPA, served by api)
   │  REST + SSE (same origin, no CORS)
 ┌─▼──────────────────────── api (Go) ─────────────────────────┐
 │ handlers → campaign service → segment (DSL→SQL) / content    │
 │                         │            ▲                       │
 │                         │      llm.Client (anthropic | mock | claudecode)
 │                         ▼                                    │
 │   Postgres: users · campaigns · campaign_content ·           │
 │             messages (outbox/queue) · delivery_attempts ·    │
 │             llm_calls                                        │
 └──────────────────────────▲───────────────────────────────────┘
                            │ claim (SKIP LOCKED, lease) / update
 ┌──────────────────────────┴──── worker (Go) ────┐      ┌─ mockprovider (Go) ─────┐
 │ one active dispatcher per channel (advisory    │─HTTP▶│ POST /v1/sms /v1/email   │
 │ lock) · sliding-window limiter 100/s · retry   │      │ latency, 5xx, 429, hangs │
 │ policy · lease recovery · graceful shutdown    │      │ Idempotency-Key, /stats  │
 └────────────────────────────────────────────────┘      └──────────────────────────┘
```

### 5.2 Repository layout (proposal; the orchestrator may refine it and log the change)

```
cmd/api/            HTTP API + SSE + embedded SPA; subcommands: serve | migrate | seed
cmd/worker/         dispatchers per channel; small HTTP server for health + metrics
cmd/mockprovider/   fake SMS/email provider with fault injection and /stats
internal/config/    env config (LLM_PROVIDER, ANTHROPIC_MODEL, rate limits, …)
internal/store/     pgx pool, migrations (embedded SQL), queries
internal/catalog/   segment field catalog: SINGLE source for prompt text, wire schema enums,
                    validator, SQL compiler and UI labels (/api/catalog)
internal/segment/   DSL types, validator, SQL compiler, audience counts
internal/content/   templates, variables, rendering, length/encoding rules, validation
internal/llm/       Client interface, prompts (embedded, versioned), repair loop, pricing,
                    adapters: anthropic/, mock/, claudecode/
internal/campaign/  campaign service + state machine (the glue)
internal/delivery/  claim/lease, limiter, retry policy, provider client, dispatcher
internal/api/       handlers, SSE hubs, request validation, error model
internal/metrics/   Prometheus registry and metric definitions
internal/seed/      deterministic fake-user generator (+ edge-case users)
web/                Vue 3 + TS app (Vite); unit tests (Vitest); e2e (Playwright)
docs/contracts/     API, SSE events, segment DSL + wire schemas, provider API, test vectors
docs/tasks/         builder task files (Phase 0 output)
docs/eval/          LLM eval set + reports
docs/stress/        stress scenarios + REPORT.md
docs/review/        verifier reports, traceability matrix
ai-sessions/        session exports (sanitized), validation-log.md, usage/
scripts/            sanitize-sessions, fresh-clone-check, transcript2md, …
```

### 5.3 docker compose (R-DEL-2)

| Service | Notes |
|---|---|
| `postgres` | 16+, with a healthcheck. Host port **55432**, to avoid clashing with a local Postgres. |
| `migrate` | One-shot, using the api image: `api migrate && api seed`. The seed is idempotent and deterministic. |
| `api` | Serves the SPA with `embed`. Port 8080. Depends on `migrate` completing successfully. |
| `worker` | Health and metrics on 9091. Must work with `--scale worker=2`. |
| `mockprovider` | Port 8090. |

- **`.env` handling:** `.env` is passed with `env_file: [{path: .env, required: false}]`. A reviewer who runs `docker compose up` without creating `.env` still gets a working app in mock-LLM mode.
- **Builds:** multi-stage Dockerfiles with build caches. The first `up` may take a few minutes; say so in the README.

### 5.4 Data model (sketch; the orchestrator finalizes it in migrations)

```sql
users(id bigserial pk, first_name text, last_name text, email text, phone text,      -- phone E.164
      city text, locale text check (locale in ('tr','en')), birth_date date,
      signup_at timestamptz, last_login_at timestamptz,
      last_purchase_at timestamptz null, purchase_count int, total_spent numeric(12,2),
      last_cart_add_at timestamptz null,
      cart_abandoned bool generated always as (last_cart_add_at is not null and
             (last_purchase_at is null or last_purchase_at < last_cart_add_at)) stored,
      favorite_category text, loyalty_tier text,
      sms_consent bool not null, email_consent bool not null, consent_updated_at timestamptz)
campaigns(id uuid pk, brief text, status text, segment_rule jsonb, segment_version int,
          audience jsonb, language text, channels text[], approve_idempotency_key text unique,
          created_at, approved_at, completed_at, scheduled_at null /* B-1 */)
campaign_content(campaign_id, version int, sms_text, email_subject, email_body,
          source text /* llm|edited */, validation jsonb, created_at, pk(campaign_id, version))
messages(id uuid pk, campaign_id, user_id, channel text /* sms|email */, status text,
          attempts int, throttled_count int, next_attempt_at timestamptz, lease_until timestamptz,
          last_error text, provider_message_id text, created_at, sent_at,
          unique (campaign_id, user_id, channel))                 -- R-SND-3 layer 1
          -- partial index on (channel, next_attempt_at) where status in ('queued','retry')
delivery_attempts(message_id, attempt, http_status, error_class, latency_ms, at)
llm_calls(id, campaign_id, purpose /* segment|content|repair */, provider, model,
          prompt_version, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
          cost_usd, latency_ms, stop_reason, outcome /* valid|repaired|invalid|refused|error */,
          error, created_at)
```

- **Purchase history** is kept as denormalized aggregates on `users`, which keeps the DSL simple and queries fast. An `orders` table is optional realism that the DSL does not query; if you add it, log it.
- **The example brief** maps cleanly onto this model: `city = İstanbul` AND `last_cart_add_at within 30 days` AND `cart_abandoned = true`. The alternative reading, "no purchase in the last 30 days", must appear in the model's `assumptions` output.

**State machines.** Each transition is a guarded `UPDATE … WHERE status = $expected`, and 0 rows updated means 409.

- **Campaign:** `draft → segmented → content_ready → sending → completed | completed_with_failures` (`cancelled` is P2).
- **Message:** `queued → sending → sent | retry → … | failed | skipped_no_consent`.

### 5.5 API sketch (the orchestrator finalizes `docs/contracts/api.md`)

| Endpoint | Purpose |
|---|---|
| `GET /api/catalog` | Segment fields, types, operators and enum values (UI and docs) |
| `POST /api/campaigns {brief}` | Create a draft |
| `POST /api/campaigns/{id}/segment/generate` | LLM → validated rule, human-readable rendering, audience counts |
| `PUT /api/campaigns/{id}/segment {rule}` | Manual edit → validate → counts |
| `POST /api/campaigns/{id}/content/generate` | Streamed generation (SSE) |
| `PUT /api/campaigns/{id}/content {sms_text, email_subject, email_body}` | Saves a new version and returns the validation report |
| `POST /api/content/validate` | Stateless; used for live validation while editing |
| `POST /api/campaigns/{id}/approve {channels, content_version}` | `Idempotency-Key` header required; returns 202 |
| `GET /api/campaigns`, `GET /api/campaigns/{id}` | List and detail |
| `GET /api/campaigns/{id}/messages?status=` | Paged list |
| `GET /api/campaigns/{id}/events` | SSE live progress; an initial snapshot, then deltas |
| `GET /api/system/status` | Health of api, worker, db and provider, plus LLM provider and model |
| `/healthz`, `/readyz`, `/metrics` | On api, worker and mockprovider |

SSE for generation: since `EventSource` is GET-only, choose either POST plus a fetch-stream parser, or POST-to-start then a GET EventSource. Decide and log the choice in need.md.

---

## 6. LLM integration (25% — the most important part)

> "Calling Claude is not enough; we expect you to make its output reliable." (case)
>
> Principle: **the model proposes, the backend disposes.** Nothing the model emits is trusted until it has passed deterministic validation, and nothing it emits is ever executed.

### 6.1 Two LLM tasks

1. **Segment extraction:** brief → `SegmentProposal`. It may run without streaming; target under 10 s.
2. **Content generation:** brief + validated segment *summary* + channel constraints → `ContentProposal`. This one is **streamed**.

The LLM never sees user rows or PII. It sees only the field catalog, aggregate counts and the brief.

### 6.2 Segment DSL (fixed depth, so it is compatible with structured outputs)

- **Shape:** `groups` are ANDed together; the `conditions` inside a group are ORed (CNF). The schema must not be recursive, because recursive schemas are not supported.
- **Proposal contents:**
  - `segment.groups[].conditions[] = {field, op, value}`;
  - `unsupported_criteria[]`: parts of the brief that we have no data for (e.g., "likes football"). They are shown to the user, never invented as fields.
  - `assumptions[]`;
  - `channels_requested`: sms, email or both;
  - `language`: tr or en;
  - `explanation`.
- **Fields:** they come only from `internal/catalog`, e.g. city (enum of seeded cities), locale, age (derived from `birth_date`), loyalty_tier, favorite_category, signup_at, last_login_at, last_purchase_at, purchase_count, total_spent, last_cart_add_at, cart_abandoned.
- **Consent is not a DSL field.** The model cannot target or untarget consent; the system always enforces it (R-AUD-2).
- **Operators by type:**

  | Type | Operators |
  |---|---|
  | number | eq, neq, gt, gte, lt, lte, between |
  | enum | eq, neq, in, not_in |
  | bool | is_true, is_false |
  | date | within_last_days, not_within_last_days, before, after, is_null, is_not_null |

- **Relative time.** Express it with `*_days` operators rather than letting the model compute absolute dates.
- **Null semantics.** These must be documented in the contract and tested:
  - `not_within_last_days N` = `col IS NULL OR col < now() - N days`, so "has not purchased in 30 days" includes "never purchased";
  - all other comparisons on NULL are false.
- **Limits** (enforced by the validator; the wire schema cannot express them):

  | Limit | Value |
  |---|---|
  | Groups | ≤ 5 |
  | Conditions per group | ≤ 10 |
  | Conditions in total | ≤ 20 |
  | `in` list length | ≤ 50 |
  | Days | 1..3650 |
  | Numbers | within the field's range |
  | Strings | ≤ 100 runes |

- **Value typing in the wire schema.** Check which JSON Schema keywords structured outputs support (see §6.7) and design the typing around them, e.g. typed slots or `anyOf` if supported. Log the choice.

### 6.3 Validation pipeline (shared by all adapters)

1. **Transport / stop reason.** `refusal` → show it to the user and do not repair. `max_tokens` → treat as invalid and run one repair with a larger limit.
2. **Strict JSON decode.** Use `DisallowUnknownFields` and reject trailing data.
3. **Structural check.** Enums, required fields, types.
4. **Semantic check.** Operator/type compatibility, value ranges, list sizes, the limits above, duplicates, contradictions. Contradictions (e.g. `age > 60 AND age < 20`) are a warning or an error; decide and document which.
5. **Compile to SQL.** Field names map through a whitelist to SQL expressions. Values are **always** `$n` parameters (`= ANY($n)` for lists). There is no string concatenation of values, ever.
6. **Dry-run count.** Run the count with `statement_timeout` and return `total`, `sms_reachable`, `email_reachable` and `both`.

**Errors** are structured: `[{path, code, message}]`.

**Types enforce the pipeline.** Only the validator can construct a `segment.ValidRule`. The compiler accepts only `ValidRule`, so no code path can compile unvalidated model output.

**The UI shows what will actually run.** The primary display is a human-readable rendering generated by the **server** from the validated rule. The model's `explanation` is secondary.

### 6.4 Repair loop

- If validation fails, send the structured errors back to the model as a follow-up turn and ask for a corrected object. Allow **at most 2 repairs**. Record each attempt in `llm_calls` with purpose `repair`.
- Never relax rules and never "auto-fix" semantics silently. If the result is still invalid, return 422 with the errors. The UI then offers to regenerate or to edit manually.

### 6.5 Content proposal and validation (R-CNT-*)

**Proposal fields:** `{language, sms:{text}, email:{subject, body}, notes}`. The body is plain text with paragraphs. The server renders it to HTML with escaping, and raw HTML from the model is never used.

**Server rules.** These are authoritative; the client mirrors them only for UX.

- **Characters.** A character is a **Unicode code point**: Go `utf8.RuneCountInString`, JS `[...s].length`. This is provisional; see N-PLAN-005.
- **Lengths.**
  - SMS ≤ 160 after **worst-case personalization**.
  - Email subject ≤ 80, a single line with no control characters.
  - Email body 1..5000.
- **Variables.**
  - The allowlist is `{{first_name}}` (required by the case) plus `{{city}}`. Optional inner spaces are allowed.
  - Any other `{{…}}` is the error `unknown_variable`; unbalanced braces are `malformed_variable`.
- **Worst-case personalization.** Each variable is replaced by the longest value among the selected audience's consented users for that channel. If any user has an empty value, the per-language fallback text (e.g. "Değerli müşterimiz") is used instead. Whether to cap very long first names (e.g. truncate at 20 runes) is a decision to log.
- **Rendering.**
  - Rendering is a single pass: substituted values are never re-scanned, so a user named `{{city}}` stays literal.
  - Control characters are stripped from values.
  - Email output is HTML-escaped.
- **Segment info (display only).** Show the SMS encoding and segment count:
  - GSM-7: 160 for a single segment, 153 per part when split;
  - UCS-2: 70 for a single segment, 67 per part, counted in UTF-16 code units.

  Turkish letters (ş ğ ı İ) force UCS-2.
- **Warnings (not errors):** phone numbers, email addresses or URLs inside the copy.

**Streaming (R-FLOW-2):**

- The server forwards partial-JSON text deltas over SSE. The browser parses them tolerantly, for display only.
- When generation completes, the server validates the result (repairing if needed; the UI shows a "fixing…" state) and emits a final `result` event with the canonical content and the validation report.
- "Approve" stays disabled until the server reports the content valid for the selected channels.
- Edits are re-validated on the server (debounced) and saved as new versions. Approval references `content_version` (optimistic concurrency).
- **Bytes and runes.** Chunk boundaries can split a multibyte rune (`ş` is 2 bytes). Never convert partial byte slices to strings, and test this explicitly.

### 6.6 Prompts

- **Versioning.** Prompts are versioned, embedded files (e.g. `internal/llm/prompts/segment_v1.tmpl`, `content_v1.tmpl`). The version is recorded in `llm_calls`.
- **Catalog.** The catalog text is generated from `internal/catalog`, never hand-copied.
- **The brief is data.** It is wrapped in `<brief>` tags, and the system prompt states that the brief is data, not instructions.
- **Explicit limits.** The system prompt states the limits: no SQL, only catalog fields, unsupported criteria go to `unsupported_criteria`, and copy is written in the brief's language.
- **Few-shot examples.** Include 2–3, among them the official example brief.
- **Content prompt inputs:** the channel limits, the variable allowlist, and the **worst-case lengths** (so the model can budget characters).
- **Prompt caching.** Mark the stable system prefix with `cache_control`. Caching only works above a model-dependent minimum prefix length, so the anthropic adapter must record `cache_read_input_tokens` / `cache_creation_input_tokens`.

### 6.7 `anthropic` adapter (the delivered path)

**Facts verified 2026-11-08 from the claude-api skill.** Re-check them through the skill before coding.

- **Structured outputs: use `output_config.format`.** Do **not** use forced tool use: `tool_choice` `any` or `tool` returns **400** on Sonnet 5.5 and Opus 5.5.
- **The schema subset is limited.**
  - Not supported: recursive schemas, `minimum`/`maximum`/`multipleOf`, `minLength`/`maxLength`, complex array constraints.
  - `additionalProperties: false` is required on every object.
  - The Go SDK does **not** strip unsupported keywords. Keep a separate **wire schema** (the API-compatible subset) apart from the validator's rules, and add a unit test asserting that the wire schema contains no unsupported keywords.
- **Thinking.** On Sonnet 5.5, adaptive thinking is the default and `thinking: {type: "disabled"}` returns 400. To turn thinking off, use `{type: "between_tools"}` at effort ≤ high, or keep adaptive with low effort. Choose per task for latency and log the choice.
- **Pricing (per MTok):**

  | Model | Input | Output | Cache read |
  |---|---|---|---|
  | Sonnet 5.5 | $2 | $10 | $0.20 |
  | Opus 5.5 | $4 | $20 | — |

  Keep a price table in code with its source and date. Compute `cost_usd` for every call.
- **Errors and timeouts.** Configure SDK retries for 429/5xx/529 (max 2). Use a per-call context timeout (segment 30 s, content 60 s). Handle the `refusal` stop reason.
- **Configuration.** The model comes from `ANTHROPIC_MODEL` (default `claude-sonnet-5-5`). Provider selection is `LLM_PROVIDER=auto|anthropic|mock|claudecode`, where `auto` means `anthropic` if the key is non-empty, else `mock`.

### 6.8 Verifying `anthropic` without a key (L-6 mitigation)

- **Contract tests against an `httptest` server.**
  - Assert the outgoing request: model, `output_config.format` schema, system blocks with `cache_control`, stream flag, headers.
  - Replay **recorded SSE streams in the documented event format**: `message_start`, `content_block_start`, `content_block_delta` with `text_delta`, `message_delta` with usage, `message_stop`.
  - Include error cases: 429 with `retry-after`, 529 overloaded, the refusal stop reason, `max_tokens` truncation, malformed JSON.
  - Fixtures must come from official docs (skill or context7), never from imagination. Note the source in each fixture's header comment.
- **`make smoke-live`:** one real segment call and one real content call. It is skipped with a clear message when there is no key. The README tells reviewers to run it first.
- **Owner action (N-PLAN-003, accepted):** the owner creates a Console key with a spend limit and writes it into `.env` personally.
- **Money-spending targets and how they are guarded** (N-PLAN-014):
  - Only two targets can reach the paid API: `make smoke-live` and `make eval PROVIDER=anthropic`. Both refuse to run unless `CONFIRM_SPEND=yes` is passed.
  - Project settings **deny** agents any command that contains `CONFIRM_SPEND=yes`, and also `make smoke-live`. So **only the owner** runs them, from their own terminal, on Monday.
  - Agents use `make up` (which forces `LLM_PROVIDER=mock`) and never a bare `docker compose up`. Once a key is in `.env`, `auto` would pick the real API.
  - `make test`, `make e2e` and `make stress` force `LLM_PROVIDER=mock` regardless of `.env`.
  - Day-to-day real-model iterations use `claudecode`.

### 6.9 `claudecode` adapter (dev-only, host-only)

**Facts verified by a probe on 2026-11-08** with Claude Code 2.1.291 on the owner's subscription:

- **Invocation.**
  ```
  claude -p --model sonnet --output-format stream-json --verbose --include-partial-messages
         --json-schema '<wire schema>' --system-prompt '<system prompt>'
         --tools "" --no-session-persistence --setting-sources "" --strict-mcp-config
  ```
  The prompt goes on stdin.
- **Isolation.** Run with `cwd` set to an empty temp dir. **Unset `ANTHROPIC_API_KEY`** in the child environment so the CLI uses the subscription login.
- **Output shape** (one JSON object per line):
  - A `system/init` line. It **contains local paths** (cwd, memory paths), so never commit raw output unsanitized.
  - `stream_event` lines wrapping Anthropic stream events. With `--json-schema` the CLI uses an internal `StructuredOutput` tool, so deltas arrive as **`input_json_delta.partial_json`**, not `text_delta`.
  - A final `{"type":"result","subtype":"success","structured_output":{…},"usage":{…},"total_cost_usd":…,"modelUsage":{"claude-sonnet-5-5":…}}`.
  - The probe took 1.7 s and cost about $0.005 (API-equivalent).
- **Normalizing deltas.** The anthropic adapter's `text_delta` and this adapter's `input_json_delta` both normalize to the same `llm.Delta{JSONFragment}`.
- **Fixtures.** Before relying on the shape, run your own probe and save a **sanitized** fixture under `internal/llm/claudecode/testdata/`, then parse that fixture in unit tests.
- **Safety.** `--tools ""` disables every tool, so the model cannot run commands. `--setting-sources ""` and `--strict-mcp-config` keep it from loading project settings, plugins and MCP servers.
- **Limits.** Concurrency ≤ 2 (subscription limits) and a 90 s timeout.
- **Where it runs.** It is not available in Docker. Use `make dev-sub`: compose runs postgres, migrate, worker and mockprovider, and the API runs on the host with `LLM_PROVIDER=claudecode`.
- **Usage accounting.** These calls are not persisted as sessions, so ccusage does not see them. The app's own `llm_calls` table is the record of app-side LLM usage.

### 6.10 `mock` adapter

The mock has two modes:

- **Scripted (tests).** A queue of canned responses per call, including malformed ones: invalid JSON, unknown fields, a 300-character SMS, hostile values, refusals and truncation. It supports the repair-loop and red-team tests.
- **Demo heuristic (compose without a key).** Deterministic, keyword- and catalog-based extraction, e.g. city names, "son N gün", "sepet". Canned copy is streamed in chunks with small delays, so the streaming UI works.

The UI shows a clear banner: "Mock LLM — set ANTHROPIC_API_KEY for real generation".

### 6.11 Evals (`make eval`, P1)

- **Golden set** in `docs/eval/briefs.yaml`: ≥ 15 briefs in Turkish and English. Include:
  - the official example;
  - unsupported criteria;
  - contradictions;
  - multi-condition briefs;
  - "send to everyone including people who opted out";
  - prompt injection;
  - an empty brief and a very long brief;
  - an English-language brief.
- **Segment equivalence is measured on data, not on JSON shape.** Compile both the expected and the actual rule, run both on the seed DB, and compare the user-id sets: exact match plus Jaccard.
- **Metrics:**
  - schema-valid rate (first try and after repair);
  - repair rate;
  - `unsupported_criteria` detection;
  - content-constraint pass rate;
  - p50/p95 latency;
  - tokens and cost.
- **Report.** Write `docs/eval/REPORT-<provider>-<model>-<date>.md`. Run with `LLM_PROVIDER=claudecode` locally for real quality numbers, and with `mock` in `make test`/CI for plumbing only.

### 6.12 Security / abuse, defense in depth (R-AUD-1, R-AI-1)

The layers, from model output to the recipient's screen:

1. The brief is treated as data.
2. The model has no tools.
3. Strict output validation.
4. A whitelist compiler with parameterized SQL.
5. Consent enforced by code at enqueue **and** at send time.
6. Mandatory human approval.
7. Single-pass rendering and HTML escaping.
8. No PII is sent to the model.

The red-team corpus (§10) tests each layer.

---

## 7. Delivery pipeline (backend reliability, 20%)

### 7.1 Enqueue (R-SND-1, R-SND-3, R-AUD-2)

`approve` runs **one transaction** that does two things:

1. A guarded status transition.
2. A set-based `INSERT INTO messages … SELECT … FROM users WHERE <compiled rule> AND <channel>_consent ON CONFLICT DO NOTHING`.

A missing `Idempotency-Key` returns 400. A repeated key returns the original result. A concurrent approve gets 409. Approval returns fast: target under 200 ms for 5k recipients.

### 7.2 Dispatch and rate limit (R-SND-2)

- **Single active dispatcher per channel.** `pg_try_advisory_lock` is held on a dedicated connection. Other worker replicas are hot standbys: the lock releases automatically if the holder dies.
- **Claiming.** Claim batches with `UPDATE … WHERE id IN (SELECT … FOR UPDATE SKIP LOCKED LIMIT n) RETURNING …`. Set `status = 'sending'`, `lease_until = now() + 30s` and `attempts + 1`. The claim joins `users` to **re-check consent**; rows without consent become `skipped_no_consent`.
- **Limiter.** Use an exact **sliding-window log limiter**: a ring buffer of the last N send timestamps, where the next send waits until the oldest is more than 1 s old. It guarantees ≤ N sends in **any** 1-second window, which a token bucket with burst does not. N = 100 (`RATE_LIMIT_PER_CHANNEL`).
- **Two-sided measurement.** Count at dispatch time; the mock provider independently measures arrivals. If network jitter pushes the provider's count above the limit, add a configurable safety margin and log it.
- **Concurrency.** In-flight requests per channel are bounded by a semaphore (e.g. 32), because 100 msg/s at ~300 ms latency needs about 30 in flight.
- **Throughput and scale.** 5k users × 2 channels ≈ 50 s per channel at full rate. Show an ETA in the UI. Replicas add failover, not throughput: the limit caps throughput anyway. Explain this in DECISIONS.

### 7.3 Retry policy (R-SND-4)

| Provider outcome | Class | Action | Counts toward max attempts |
|---|---|---|---|
| 2xx | success | `sent` | — |
| 400 / 404 / 422 | permanent | `failed` with no retry (e.g. invalid number) | — |
| 401 / 403 | configuration | pause the channel and alert; messages stay queued | no |
| 429 | throttled | **pause the whole channel** until `Retry-After` (default 1 s, cap 30 s); reschedule | no; separate cap of 20 throttles |
| 5xx | transient | exponential backoff with **full jitter**: base 1 s, ×2, cap 60 s | yes, max 5 |
| timeout / connection error | ambiguous | retry with the **same Idempotency-Key** | yes |

When attempts are exhausted, the message becomes `failed` with `last_error`. It is visible in the UI; a "retry failed" button is P2.

### 7.4 Duplicate prevention in layers (R-SND-3)

1. `UNIQUE(campaign_id, user_id, channel)`.
2. A guarded, idempotent approve.
3. `SKIP LOCKED`: two workers never claim the same row.
4. Lease plus `Idempotency-Key = message.id` towards the provider. A crash after send but before the status update causes a retry that the provider deduplicates.
5. The provider's `/stats` **proves** it: `duplicate_recipient_deliveries == 0`.

This is the classic combination of at-least-once delivery and an idempotent receiver, which gives exactly-once effect. Explain it in DECISIONS.

### 7.5 Recovery and shutdown

- **Lease recovery.** Rows still in `sending` after `lease_until` passes are reclaimable.
- **Graceful shutdown on SIGTERM.** Stop claiming, drain in-flight sends (≤ 10 s), and release the leases of anything unsent.
- **Database outage.** The worker survives a Postgres restart using backoff, and `/readyz` reflects the outage.

### 7.6 Send-time rendering

- Render per recipient from the approved `content_version`.
- Defense in depth: if a rendered SMS is over 160, it becomes a permanent failure `content_too_long`. This should never happen; it increments an alert metric.

### 7.7 Mock provider (R-RES-3)

**Endpoints:** `POST /v1/sms {to, text, campaign_id, user_id, message_id}`, `POST /v1/email {to, subject, html, text, …}`, plus the `Idempotency-Key` header.

**Behaviors.** They are configured through env vars and changeable at runtime with `POST /admin/config`. A seed option makes them deterministic for tests.

| Behavior | Details |
|---|---|
| Latency | A distribution, e.g. 20–300 ms with rare 2 s spikes |
| 5xx | A configurable rate |
| 429 | Random 429s with `Retry-After`. Also a **real limiter** at its own configured limit, so non-compliance is visible. |
| Hangs | Requests that exceed the client timeout |
| Permanent 400 | For invalid addresses (some are seeded on purpose) |
| Idempotency | The same key returns the same response and is counted once |

**`GET /stats`** reports, per channel and in total:

- accepted;
- unique keys;
- replayed keys;
- `duplicate_recipient_deliveries`, keyed by (campaign_id, user_id, channel);
- **max deliveries in any 1-second sliding window**;
- counts by status code;
- the list of delivered recipient ids (or a hash), used for consent cross-checks.

`POST /admin/reset` resets the state for tests.

---

## 8. Frontend (20%)

**Screens:**

1. **Composer** (a stepper):
   1. **Brief.** Prefilled with the official example; example chips are available.
   2. **Audience.**
      - Server-rendered rule chips, with the raw JSON behind a toggle.
      - Audience counts: total, SMS-reachable and email-reachable.
      - `unsupported_criteria` and `assumptions` shown as warnings.
      - LLM metadata: provider, model, latency, tokens and cost.
      - Manual rule editing (P1: a simple form, or JSON with server validation).
   3. **Content.**
      - Streaming SMS and email fields.
      - Live counters per field: runes, worst-case rendered length, and SMS encoding/segments.
      - Validation badges and editable fields with debounced server validation.
      - A preview rendered with real sample users, including the longest name.
   4. **Approve.** Choose channels; a confirmation dialog shows the exact recipient counts; Approve is disabled until the server says valid.
2. **Campaign dashboard** (live over SSE):
   - Per-channel counts for queued, sending, sent, retry, failed and skipped.
   - A progress bar and ETA.
   - A throughput chart in msg/s per channel, with the 100/s line (P1; a lightweight SVG is fine).
   - An error breakdown: 429, 5xx, timeouts, permanent.
   - Recent events.
   - On reconnect, fetch a snapshot again, then continue the stream.
3. **Campaign list and system panel:**
   - Health of api, worker, db and provider.
   - LLM provider and model, plus the mock banner.
   - **Mock-provider fault controls** (P1, great for the live demo).

**Quality bar:**

- Loading, empty and error states everywhere. LLM failures, 422 validation errors and refusals show human-readable messages.
- Keyboard-accessible, with labels on inputs.
- Responsive enough for a laptop screen.
- Use the `frontend-design` skill for a polished, non-generic look, but clarity wins over decoration.

**Shared test vectors.** `docs/contracts/testvectors/text_length.json` is run by **both** Go and Vitest tests. Counting must be identical across server and client.

**Tests:**

- **Vitest:** counters, partial-JSON stream rendering (including split multibyte runes), approve gating, and how validation errors are displayed.
- **Playwright e2e** (`make e2e`, P1): the official example brief → audience → streamed content → edit → approve → dashboard reaches completed, all with the mock LLM.

---

## 9. Observability (R-OBS-*)

- **Health:** `/healthz` is liveness (the process is up). `/readyz` is readiness: DB reachable and migrations applied; the worker also requires a fresh dispatcher heartbeat. `GET /api/system/status` aggregates these for the UI.
- **Metrics:** Prometheus `/metrics` on api, worker and mockprovider.

  | Area | Metrics |
  |---|---|
  | Messages | `messages_total{channel,status}`, `queue_depth{channel,status}`, `retries_total{channel,class}`, `provider_responses_total{channel,code}`, `send_latency_seconds{channel}`, `rate_limiter_wait_seconds{channel}`, `channel_paused{channel}`, `content_too_long_total` |
  | LLM | `llm_requests_total{purpose,provider,model,outcome}`, `llm_tokens_total{type}`, `llm_cost_usd_total`, `llm_latency_seconds{purpose}`, `llm_validation_failures_total{reason}`, `llm_repairs_total` |

- **Logs:** `log/slog` JSON with `campaign_id`, `message_id` and `attempt`. Never log message bodies with PII at info level.

---

## 10. Verification matrix: one check and one stress test per requirement

The owner explicitly asked for this: **every Insider requirement gets its own functional check and its own stress or adversarial test.**

- **Checks** run in `make test`.
- **Stress scenarios** run in `make stress` and write numbers to `docs/stress/REPORT.md`.
- **Adversarial suites** (red-team) run in `make test` with the mock LLM, and in `make eval` with a real model.
- The `spec-auditor` maintains `docs/review/traceability.md` with one row per ID: code location, test names, last result and evidence link.

### 10.1 Stress scenarios

| ID | Scenario |
|---|---|
| S1 baseline | 5k users × 2 channels, faults off. Measure throughput per channel (expect ≈100/s) and time to completion. |
| S2 chaos | ~50k messages (`SEED_USERS=25000`); provider at 10% 5xx, 5% 429 with Retry-After, 2% hangs, 20–800 ms latency; `--scale worker=2`; `docker kill` one worker mid-run and restart it. |
| S3 approve storm | 100 concurrent approves of the same campaign, plus 10 different campaigns approved concurrently. |
| S4 LLM adversarial | Red-team corpus through the mock adapter (in `make test`), plus a real-model run via `make eval` with claudecode. |
| S5 API load | 200 concurrent segment previews and content validations. Report p95 latency, errors and DB-pool saturation. |
| S6 dashboard fan-out | 50 SSE subscribers during S2, plus reconnects mid-run. |
| S7 dependency restart | Restart Postgres during sending; the provider becomes unavailable for 10 s. |

### 10.2 Invariants (asserted after every stress scenario)

| ID | Invariant |
|---|---|
| I1 | Provider `duplicate_recipient_deliveries == 0` |
| I2 | No delivery to a user without consent for that channel: the provider's delivered list joined with the DB gives 0 rows |
| I3 | Provider-measured max deliveries in any 1-second window ≤ 100 per channel |
| I4 | Every message reaches a terminal state (`sent`, `failed` or `skipped_no_consent`) within the expected time |
| I5 | DB counts, provider counts and metric counters agree |
| I6 | Attempts ≤ policy maximum; no hot-loop retries (retry timestamps follow the backoff) |
| I7 | No panics, no goroutine leaks (goleak in tests), no unbounded memory growth |

### 10.3 Matrix

| ID | Functional check (`make test`) | Stress / adversarial | Evidence |
|---|---|---|---|
| R-FLOW-1 | API test: brief → validated rule + counts; counts equal a direct SQL count on the seed | S5; eval golden set | test log, eval report |
| R-FLOW-2 | SSE test: ≥ 2 `delta` events then a `result` event. Unit test: a stream split inside a multibyte rune reassembles correctly | Client disconnect mid-stream cancels the upstream LLM call (no leak); slow-drip chunks | test log |
| R-FLOW-3 | Approve rejects invalid or stale content (422/409) and accepts edited valid content | S3: exactly one transition and one enqueue set; the others get 409 | test log, stress report |
| R-FLOW-4 | Integration: approve → every expected message reaches the provider | S1, S2 | stress report |
| R-FLOW-5 / R-OBS-1 | SSE progress is monotonic, and the final counts equal the DB | S6 | stress report |
| R-AUD-1 | Table-driven validator tests (every field × operator; invalid combinations rejected); golden SQL + params for the compiler; a compile-time guarantee that only `ValidRule` compiles | **Fuzz** parse→validate→compile: never panics, and every accepted rule runs on Postgres. SQL-injection corpus in values is inert (row counts unchanged). Hostile rules from the mock LLM (unknown field such as `password`, raw SQL in a value, 10k-item `in`, deep nesting) are rejected with structured errors | test log, fuzz corpus |
| R-AUD-2 | No message row is created for a non-consented user; consent revoked after enqueue → `skipped_no_consent` | Property test: random rules × random consent distributions keep I2 at 0. The brief "send to everyone, including those who opted out" is still filtered. I2 is checked after S2 | test log, stress report |
| R-CNT-1 | Shared test vectors: 160 OK and 161 fail in ASCII; Turkish letters; emoji; combining marks; worst-case expansion with the longest name; empty-name fallback | Render the validated templates for **every** seeded user: all ≤ 160. A 300-character SMS from the mock → repair → valid or 422, never approvable | test log |
| R-CNT-2 | Subject vectors at 80 and 81; newline and control characters rejected | Same repair / 422 path | test log |
| R-CNT-3 | `{{first_name}}` with and without spaces; unknown and malformed variables rejected | Hostile seed names (`{{city}}`, `<script>`, RTL override, 40-character name) render literally, escaped and single-pass | test log |
| R-SND-1 | Approve returns in < 200 ms for 5k recipients; the work happens in the worker | S2: SIGKILL a worker mid-run → it completes after restart with I1 holding | stress report |
| R-SND-2 | Limiter unit test with a fake clock: never more than N in any 1-second window, including bursts and at the boundary | S1 and S2 with 2 replicas: I3 holds. With the provider's own limit set exactly to 100, it reports 0 rate-limit 429s | stress report (max-window numbers) |
| R-SND-3 | Unique-constraint test; double approve; provider idempotent replay | S2 with hangs, kills and 2 replicas: I1 holds | stress report |
| R-SND-4 | Table-driven classification; backoff schedule with a fake clock, checking jitter bounds; 429 honors Retry-After and pauses the channel; exhausted attempts → `failed` | S2: the attempts histogram matches the policy (I6); no hot loop | stress report |
| R-OBS-2 | Scrape `/metrics` and assert the required series and labels exist | I5 after S2 | test log, stress report |
| R-OBS-3 | `/healthz` 200; `/readyz` 503 when the DB is unreachable, recovers afterwards | S7 | stress report |
| R-AI-1 | Every adapter path goes through the same validator (enforced by types); scripted bad outputs → repair / 422; refusal and `max_tokens` paths | Red-team corpus (≥ 25 cases): injection briefs, SQL in fields, consent bypass, PII exfiltration requests, oversize lists; plus a real-model eval (S4) | test log, eval report |
| R-AI-2 | `make test` runs with `ANTHROPIC_API_KEY` unset and **no internet** for the test container | CI run | CI log |
| R-RES-1 | Config loads the key from env/.env; the missing-key behavior follows N-PLAN-004; `.env.example` exists; gitleaks is clean | `make sanitize-check` | check log |
| R-RES-2 | The seed is deterministic and idempotent, has ≥ 3k users (default 5k), and contains the edge-case users | `SEED_USERS=100000`: segment counts stay < 300 ms with indexes (S5) | stress report |
| R-RES-3 | Provider unit tests for every fault mode, idempotency and `/stats` | The provider sustains 500 rps without becoming the bottleneck | stress report |
| R-DEL-2 | `scripts/fresh-clone-check.sh`: clean clone → `docker compose up -d` → healthy in < 180 s → UI reachable → seed present | Run it twice (restart idempotency), then `down -v && up` | script log |
| R-DEL-3 | `make test` from a clean clone, needing only Docker, in < 5 minutes | Run it in CI (B-4) | CI log |
| R-DEL-4 / 5 | spec-auditor: README covers setup, architecture, how to run with a key, mock mode, limitations and not-done items. Every locked decision and every accepted high or medium need.md entry appears in DECISIONS.md | — | review report |
| R-DEL-6 | `make sanitize-check`: gitleaks over the repo and `ai-sessions/`; grep for the owner's terms (§13); every session listed in `ai-sessions/README.md` has an export | — | check log |
| B-1..B-4 | Each bonus gets its own checks if implemented: B-1 fake-clock scheduling; B-3 locale-based selection; B-2 deterministic hash split with per-variant stats | — | — |

---

## 11. Test strategy and make targets

| Target | What it does | Needs |
|---|---|---|
| `make up` / `down` / `logs` | Compose lifecycle. `make up` forces `LLM_PROVIDER=mock`, so agents never spend money even when a key is in `.env` | Docker |
| `make test` | `go vet` + `go test -race -count=1 ./...` (unit + integration against an ephemeral Postgres and mockprovider from a compose test profile) + web unit tests. No key, no internet | Docker only |
| `make lint` | golangci-lint + `vue-tsc` / eslint | Docker or local |
| `make stress` | Build tag `stress`; scenarios S1–S7; writes `docs/stress/REPORT.md` | Docker |
| `make e2e` | Playwright against the compose stack with the mock LLM | Docker |
| `make eval PROVIDER=mock\|claudecode\|anthropic` | LLM eval with an **explicit** provider (no `auto`); writes a report. `anthropic` also requires `CONFIRM_SPEND=yes` | claudecode, or a key (owner only) |
| `make smoke-live CONFIRM_SPEND=yes` | One real Anthropic call per task. Refuses without the flag; agents are denied by settings | API key (owner only) |
| `make dev-sub` | Compose dependencies + API on the host with `LLM_PROVIDER=claudecode` | Owner's subscription |
| `make sanitize-check` | gitleaks + PII grep over the repo and `ai-sessions/` | local |
| `make fresh-clone-check` | Clones to a temp dir, runs `up`, then `test` | Docker |

**Conventions:**

- Table-driven tests.
- Injected clocks; no `time.Sleep` in unit tests.
- Integration tests get an isolated database per package via `TEST_DATABASE_URL` plus a random DB name, so packages can run in parallel and each worktree can run its own tests.
- goleak in packages that start goroutines.
- Fuzz targets for the DSL and the renderer.
- Test names reference requirement IDs, e.g. `TestApprove_ConcurrentStorm_SingleEnqueue_RFLOW3`.

---

## 12. Agent orchestration (how the agents are connected)

### 12.0 Execution mode: ultracode (the owner's choice)

The owner runs the orchestrator with **ultracode**. With it on, Claude plans a **dynamic workflow** for each substantive task: a JavaScript script that the runtime executes, fanning out subagents. The default execution is therefore:

- **Builders are workflow agents launched by the orchestrator**, not manually opened sessions.
  - The §12.1 table still defines what each role owns and may touch.
  - The manual sessions in §12.3 are the **fallback**. Use them if workflows misbehave, merges get messy, or usage limits call for tighter control.
- **Invoke the bundled `/workflow-authoring` skill before writing any workflow script.** Never guess the script API: `agent()`, `pipeline()`, `parallel()`, `phase()`, per-agent model, output `schema`, isolated copies.
- **Verified facts** (code.claude.com/docs/en/workflows, 2026-11-08):
  - Runs execute in the background and accept **no mid-run user input**.
  - At most 16 concurrent agents.
  - Agent prompt cache lasts 5 minutes.
  - Runs are resumable within the same session.
  - The run's script and results are saved under the session's directory in `~/.claude/projects/`.
  - `Date.now()` and `Math.random()` throw inside scripts.
- **One workflow per stage.** Runs cannot ask questions, so the owner signs off between stages:

| Workflow | Purpose | Shape |
|---|---|---|
| W0 plan (optional, plan mode) | Draft the Phase 0 plan from 2–3 independent angles, then reconcile | ≤ 4 read-only agents |
| W1 build-slices | Build the llm, delivery and web areas in parallel from `docs/tasks/*.md` | One builder agent per area in an **isolated copy (worktree)**, each followed by a verifier agent that runs the area's check tests and reports pass/fail with output |
| W2 integrate | End-to-end flow, compose up, e2e happy path. Fix loop until `make test` is green or two rounds make no progress | sequential + fix loop |
| W3 verify | Stress S1–S7 with invariants, red-team, reliability review, spec audit. Findings are **adversarially cross-checked** before being reported | fan-out + cross-check |
| W4 fix | One agent per confirmed finding (isolated copies), then re-verify | pipeline |
| W5 release | Docs, traceability, sanitization checks | small |

- **Agents return structured output, not prose.** Every build or verify agent returns a JSON `schema` result:
  ```
  {area, requirement_ids_done[], tests: [{cmd, exit_code, summary}], files_changed[],
   need_entries[], validation_log_entries[], open_issues[]}
  ```
  The **orchestrator** writes `need_entries` into need.md (using the §4.2 format and agent codes) and `validation_log_entries` into the validation log. Workflow agents never edit those two files directly, so concurrent edits never conflict.
- **Merging.** For each isolated copy, the orchestrator reviews the diff, runs `make test` and merges, as in §12.2(2). If the runtime handles isolated copies differently than expected, log it in need.md and adapt.
- **Model routing per agent.** Otherwise every agent runs on the session model (Opus). Name the model in the script for each stage:

  | Stage | Model |
  |---|---|
  | `llm` builder and all verifiers | opus |
  | delivery builder, web builder, fix loops | sonnet |
  | listing, searching and summarizing steps | haiku |

- **Size.** Stay within the `medium` guideline (< 10 agents per workflow) unless the owner approves more. W3 may justify more.
- **Save every workflow that ran** into `.claude/workflows/` (`/workflows` → select → `s`), with names like `scc-w1-build-slices`. They are committed, so reviewers can read exactly how the agents were connected.
- **Permissions.** Agents use the project allow/deny rules in `.claude/settings.json`. Hard stops are enforced by deny rules: push, `reset --hard`, `CONFIRM_SPEND=yes`, `.env`. If an agent needs a command that is not allowed, it fails fast and reports it. Do not widen the rules without logging it.
- **Usage.** Ultracode multiplies tokens, and the owner is on a subscription.
  - Watch the token counts in `/workflows`.
  - Use `/effort ultracode off` for routine steps: merging, small edits, doc tweaks.
  - Prefer smaller models for mechanical stages.

### 12.1 Roles

| Agent | Kind | Model / effort | Owns (may write) |
|---|---|---|---|
| **orchestrator** (`01-orchestrator`) | Main session, main checkout | Opus 5.5 · ultracode (xhigh) | `docs/contracts/`, `internal/{config,store,catalog,campaign,api,metrics,seed}`, `cmd/api`, compose, Dockerfiles, Makefile, root docs, `.claude/`, `need.md` digest, merges |
| **builder-llm** (`02-builder-llm`) | Session in worktree `llm` | Opus 5.5 · high | `internal/llm/**`, `internal/segment/**`, `internal/content/**`, `docs/eval/**` |
| **builder-delivery** (`03-builder-delivery`) | Session in worktree `delivery` | Sonnet 5.5 · high | `internal/delivery/**`, `cmd/worker/**`, `cmd/mockprovider/**`, `docs/stress/**` |
| **builder-web** (`04-builder-web`) | Session in worktree `web` | Sonnet 5.5 · high | `web/**` |
| `spec-auditor` | Subagent, read-only + test commands | Opus · high | nothing (returns a report) |
| `llm-red-team` | Subagent, `isolation: worktree` | Opus · high | red-team testdata and tests under `internal/{llm,segment,content}/**/testdata/redteam` |
| `reliability-reviewer` | Subagent, read-only + test commands | Opus · high | nothing (returns a report) |
| Plugin and built-in reviewers | Invoked by the orchestrator | — | nothing |

The plugin and built-in reviewers are: pr-review-toolkit's `silent-failure-hunter`, `pr-test-analyzer` and `type-design-analyzer`, plus `/code-review`, `/security-review` and `/simplify`.

**Builders as workflow agents (default) or as sessions (fallback):**

| | Workflow agents (default) | Manual sessions (fallback, §12.3) |
|---|---|---|
| Context | Fresh context per task | Full context per builder |
| Parallelism | Parallel, with no terminal juggling | Parallel, one terminal each |
| Steering | Through the orchestrator, between stages | The owner steers each builder directly |
| Transcripts | **Not** in the parent's `/export`; §13 harvests them from the session directory | One `/export` per builder |

The builder rows in the table above describe the **roles**. Under ultracode, each one runs as a workflow agent.

**Why verifiers are subagents:** a clean context with no authorship bias. A reviewer that did not write the code finds more problems.

### 12.2 How they connect

1. **Contracts + Go interfaces (the backbone).** These are produced in Phase 0. Builders code against them, so nobody waits for anybody.
2. **Git.**
   - Each builder works on branch `worktree-<name>`.
   - At each checkpoint the orchestrator reviews `git diff main...worktree-<name>`, runs `make test` on the branch and merges `--no-ff`.
   - After contract changes, builders run `git merge main`.
   - `go.mod`/`go.sum`: Phase 0 pre-adds the expected dependencies. If a builder adds another, it logs that in need.md and the orchestrator resolves it with `go mod tidy` at merge time.
   - Migrations use timestamp names, so parallel additions never collide.
3. **Task files.** `docs/tasks/<builder>.md` holds the goal, owned paths, contracts, the requirement IDs with the tests that prove them, ports and run commands, and a done checklist. Builders tick the checkboxes and append a short status log at the bottom.
4. **need.md and `ai-sessions/validation-log.md`.** Union-merged; see §4.
5. **Cross-session messages.** If `SendMessage` / `ListAgents` are available, a builder notifies `01-orchestrator` at milestones (e.g. "R-SND-2 limiter done, tests green, commit abc123"). Otherwise it commits and updates its task file.
6. **Verifier subagents.** The orchestrator invokes them at checkpoints and saves their reports under `docs/review/<date>-<agent>.md`. Each finding becomes a fix task for the owning builder.
7. **Optional, Phase 2 only: agent teams.** Run with `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`: 3 reviewers in parallel that challenge each other's findings. Do this only if usage limits allow, and **never for building** (no resume, high token use).

### 12.3 Fallback: manual builder sessions (used only if workflows are not suitable)

```bash
cd ~/projeler/smart-campaign-composer   # Phase 0 must be committed first (worktree.baseRef = head)
claude -w llm      -n 02-builder-llm      --model opus   --effort high "Read docs/tasks/llm.md and follow it."
claude -w delivery -n 03-builder-delivery --model sonnet --effort high "Read docs/tasks/delivery.md and follow it."
claude -w web      -n 04-builder-web      --model sonnet --effort high "Read docs/tasks/web.md and follow it."
```

**Shared infrastructure for worktrees:**

- Compose runs **only** from the main checkout.
- Builders run their own binaries on separate ports:

  | Worktree | Port range |
  |---|---|
  | llm | 81xx |
  | delivery | 82xx |
  | web | 83xx, plus the Vite port |

- Builders use the shared Postgres on 55432 with their own database names.
- `.env` is never copied into worktrees.

### 12.4 Checkpoints

| CP | When (target) | Content |
|---|---|---|
| CP0 | Sun ~12:00 | Phase 0 committed: compose up healthy, `make test` green (skeleton), task files and agents ready, builders launched |
| CP1 | Sun ~17:00 | First vertical slices merged: brief → validated rule → count; approve → queue → provider; composer UI against contracts. spec-auditor run #1 |
| CP2 | Sun ~23:00 | **Build day done: P0 end-to-end with the mock LLM**, one check test per requirement, a claudecode smoke run, README and DECISIONS drafts |
| CP3 | Mon ~14:00 | **Verification, part 1:** stress S1–S7 and REPORT.md, `llm-red-team`, `reliability-reviewer`, claudecode eval → fixes |
| CP4 | Mon ~22:00 | **Verification, part 2:** `/code-review`, `/security-review`, Playwright e2e, owner-authorized `make smoke-live` and one real-API eval, B-4 CI → fixes |
| CP5 | Tue 10:00 | **Freeze.** Final docs, sanitization, fresh-clone check; submit by 14:00 |

At every checkpoint the orchestrator:

1. merges;
2. runs `make test`;
3. runs spec-auditor;
4. updates the need.md digest;
5. adds a row to `ai-sessions/README.md`;
6. posts a short status in chat, in Turkish, for the owner.

**Usage limits** (the owner is on a subscription): if limits get tight, pause builder-web before builder-llm, and drop verifier effort before dropping verifiers.

---

## 13. AI sessions, evidence and presentation data (R-DEL-6)

The owner will build a presentation covering:

- how Claude was used and the md design principles;
- token usage and cost, and the models used;
- how agents were connected;
- session history;
- MCP and API connections;
- efficiency.

Collect the evidence as you go, because it cannot be reconstructed later.

### Session naming

Use `NN-role-topic`, and start or rename sessions accordingly:

| Session | Role |
|---|---|
| `00-planning` | The session that wrote this file, started in `~` |
| `01-orchestrator` | Orchestrator |
| `02/03/04-builder-*` | Builders |
| `05-review-*` | Verifier runs |
| `06-release` | Release work |

### Export pipeline

1. `/export ai-sessions/_raw/NN-name.txt`. `_raw/` is gitignored.
2. `scripts/sanitize-sessions.sh` replaces owner-specific terms with placeholders. The terms come from the gitignored `.sanitize-terms` file: name, email, home path, any third-party names. Its output goes to `ai-sessions/NN-name.txt`.
3. gitleaks scans the output.
4. A human skims it.

**Subagent and workflow transcripts.** Under ultracode, most of the work happens in workflow agents, and their reasoning is **not** in the main `/export`. The harvest script `scripts/harvest-sessions.sh` collects, for every session of this project:

- the full session `.jsonl`, which is also the only complete record if a session was compacted;
- `subagents/*.jsonl`;
- the workflow run scripts and results.

All of these live under `~/.claude/projects/<encoded-project-path>/<session-id>/`. Worktree sessions have their own encoded path, so glob for `*smart-campaign-composer*`.

The harvest script copies everything to `_raw/`. Then `scripts/transcript2md.py` converts it to readable Markdown, dropping images and base64. Finally, sanitize the output and put it in `ai-sessions/sessions/` and `ai-sessions/workflows/`. The saved workflow scripts in `.claude/workflows/` are the readable summary of "how the agents were connected".

### Files to maintain

**`ai-sessions/README.md`** contains:

- the index table: session, role, model/effort, date and duration, purpose, key outcomes, **what was validated and how**;
- "How the agents were connected", with a diagram;
- "Tooling and connections":

  | Kind | Items |
  |---|---|
  | Plugins | gopls-lsp, typescript-lsp, context7, frontend-design, playwright, pr-review-toolkit, session-report |
  | MCP | context7 (docs), playwright (browser), Claude in Chrome |
  | CLIs | gh, gitleaks, golangci-lint, ccusage |
  | App APIs | Anthropic Messages API via the Go SDK; Claude Code headless as the dev LLM |

**`ai-sessions/validation-log.md`** is append-only and union-merged:

```markdown
### V-<NNN> · <title>
- **When / agent:** …
- **AI proposed:** …
- **How it was caught:** test | review subagent | owner | tool (which)
- **Fix:** … (commit)
- **Lesson / guard added:** … (e.g. a new test or rule)
```

### Usage data

- **Daily snapshots:** `ccusage daily --json > ai-sessions/usage/ccusage-daily-<date>.json` and `ccusage session --json > …`.
- **At the end:** the `session-report` skill's HTML report (tokens, cache efficiency, subagents, expensive prompts) and `/insights`.
- **App-side LLM usage:** export a summary of `llm_calls`.
- **Note:** on a subscription, ccusage figures are **API-equivalent estimates**, not invoices. Say so in the presentation.

---

## 14. Definition of done and submission checklist

**A requirement is done when** it is implemented, its check test is green, its stress or adversarial test is green (or the gap is documented), and the traceability row links the evidence.

**Before submission (Tue):**

- [ ] All P0 rows in `docs/review/traceability.md` are green; gaps are listed in the README's "Not done / next steps".
- [ ] The fresh-clone check passes: `docker compose up` works with **no** `.env` (mock mode) and with a key-only `.env`; `make test` < 5 min.
- [ ] `make stress` report committed with real numbers; eval report committed (claudecode run).
- [ ] README: what it is, quick start (with key / without key), architecture summary + diagram, how to run tests, stress and eval, LLM design summary, limitations and not-done items, and the time spent.
- [ ] DECISIONS.md: ADR-lite (context → decision → alternatives → consequences). Covers at least:
  - the LLM provider abstraction;
  - structured outputs and the repair loop;
  - the segment DSL and whitelist compiler;
  - character counting;
  - Postgres as the queue;
  - the limiter;
  - the retry policy;
  - dedup layers;
  - consent enforcement;
  - the model choice;
  - the agent workflow.
- [ ] need.md: no 🔁 left unimplemented; no high-impact 🟡 without an owner response; digest current.
- [ ] `ai-sessions/` complete and sanitized; `make sanitize-check` clean; `.env` not tracked (`git ls-files | grep -E '^\.env$'` empty).
- [ ] Owner actions (hard stops): repo visibility / reviewer access, final push, reply-all email with the repo link.

---

## 15. Phase 0 — your first task (orchestrator)

**In plan mode, produce a plan** containing the items below. Keep it tight and do not restate this file.

1. Your understanding, in ≤ 12 bullets, plus the **top 5 risks** with mitigations.
2. The final repo layout and any deviations from §5 (each deviation becomes a need.md entry).
3. The list of contract files and what each one fixes.
4. The skeleton you will create, and how you will prove it works.
5. The outline of each builder task file.
6. The subagent definitions (frontmatter + purpose).
7. The timeline against §12.4.
8. The need.md entries you expect to add.

**After the owner approves, execute in this order and commit at the end:**

1. **`CLAUDE.md`**, ≤ 120 lines: project one-liner; pointers to KICKOFF.md, need.md, DECISIONS.md and `docs/contracts/`; commands; the ownership map; rules (need.md, hard stops, secrets, validation-log, evidence before "done", commit format, English in files); "invoke `claude-api` before Anthropic code; use context7 for library docs". Also create small `web/CLAUDE.md` and `internal/llm/CLAUDE.md` with area-specific notes.
2. **Repo plumbing.** `.gitattributes` (union merge for `need.md` and `ai-sessions/validation-log.md`). Extend `.gitignore` with `ai-sessions/_raw/`, `.sanitize-terms`, build outputs and `node_modules`. Add `.env.example` with **every** variable and a comment for each (no real values).
3. **Contracts in `docs/contracts/`:**
   - `segment-dsl.md`, `segment.wire.schema.json`, `content.wire.schema.json`;
   - `api.md` (endpoints, error model, SSE event names and payloads);
   - `provider-api.md` (mock provider incl. `/stats` and `/admin/*`);
   - `testvectors/text_length.json`.
4. **Skeleton that compiles and runs:**
   - `go.mod` with the expected dependencies pre-added (anthropic-sdk-go, pgx/v5, prometheus client, uuid, goleak, go-cmp, a migration approach of your choice);
   - interfaces and stub packages;
   - migrations and the seed (deterministic, edge-case users included);
   - a minimal mockprovider;
   - `cmd/api serve|migrate|seed`;
   - the worker loop stub;
   - the web scaffold (Vite + Vue 3 + TS strict + Vitest);
   - Dockerfiles, `docker-compose.yml` (plus a test profile), and a Makefile with all §11 targets (stubs print "not implemented yet" and exit 0, except `test`).
5. **`.claude/agents/`:** `spec-auditor.md`, `llm-red-team.md`, `reliability-reviewer.md`. Each has a precise description of when to use it, minimal tools, model, effort and an output format.
6. **`docs/tasks/`:** `llm.md`, `delivery.md`, `web.md`, following §12.2(3). Write each one so it works as the prompt of a W1 workflow agent **and** as the prompt of a fallback manual session.
7. **`ai-sessions/`:** `README.md` (skeleton + tooling section), `validation-log.md` (header), `usage/`. **Scripts:** `scripts/harvest-sessions.sh`, `scripts/transcript2md.py`, `scripts/sanitize-sessions.sh`, `scripts/fresh-clone-check.sh`. **Makefile guard:** `smoke-live` and `eval PROVIDER=anthropic` refuse to run without `CONFIRM_SPEND=yes`.
8. **Verify:** `docker compose up -d` → all services healthy, the seed is present, and `curl /healthz` works on each; `make test` is green. Quote the outputs.
9. **Commit:** `chore: phase 0 contracts and skeleton [R-DEL-2][R-DEL-3]`. Update the need.md digest. Then write the owner a 5-line summary in Turkish and propose **W1** (§12.0) for approval: phases, agents per phase, the model per agent, and a rough token estimate. Keep the §12.3 commands as the fallback.

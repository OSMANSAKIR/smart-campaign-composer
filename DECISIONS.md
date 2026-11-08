# DECISIONS

Final, curated decisions, ADR-lite: **context → decision → alternatives → consequences**. The full log of
decisions taken without the owner, with the owner's answers, is in [`need.md`](need.md); IDs in brackets point
there. The requirements and locked decisions are in [`KICKOFF.md`](KICKOFF.md) §2–§3.

Guiding rule when the case left a choice open: safest for the hard requirements, then most reversible, then
simplest to explain.

---

## D-1 · The model proposes, the backend disposes

- **Context.** The case: "calling Claude is not enough; make its output reliable" (R-AI-1, R-AUD-1).
- **Decision.** Nothing the model emits is trusted or executed. Every answer goes through: stop-reason check →
  strict JSON decode (unknown fields and trailing data rejected) → structural check → semantic check → only then
  a typed value. The LLM never sees user rows or PII: only the field catalog, aggregate counts and the brief.
  The brief is wrapped in `<brief>` tags and the system prompt says it is data, not instructions.
- **Alternatives.** Trust the structured-output guarantee alone; let the model write SQL.
- **Consequences.** More code than a single call, but every failure is a structured error
  `[{path, code, message}]` that the UI and the repair loop can use. The CP1 red-team (45 hostile cases) found
  no path from model or brief text to SQL text (`ai-sessions/validation-log.md` V-014).

## D-2 · One provider interface, three adapters [L-5, N-PLAN-002, N-PLAN-004]

- **Context.** The owner has no API key during development (L-6), but reviewers will run with theirs.
- **Decision.** `llm.Provider` is a thin transport. Prompts, validation, repair and `llm_calls` accounting
  live once in `internal/llm`, so every adapter gets the same guarantees.
  - `anthropic` — the delivered path: official Go SDK, streamed, structured outputs.
  - `mock` — deterministic; used by every test and by compose when no key is set.
  - `claudecode` — **dev-only, host-only**: shells out to the local Claude Code CLI (`claude -p`) on the
    owner's subscription with Sonnet. It is how prompts and evals were validated against a real model without
    a key. Never used in tests, CI or as a default.
  - `LLM_PROVIDER=auto` (compose default) picks `anthropic` when `ANTHROPIC_API_KEY` is set, otherwise `mock`
    with a banner in the UI. An explicit `LLM_PROVIDER=anthropic` without a key fails fast.
- **Alternatives.** Anthropic only (nothing runnable without a key); mock only (no real-model evidence);
  remove `claudecode` before submission (loses the evidence of how the prompts were validated).
- **Consequences.** `docker compose up` always demos end to end. The real-API request shape is covered by
  contract tests (outgoing body asserted against an httptest server, SSE fixtures copied from the docs) and by
  the owner-run `make smoke-live`.

## D-3 · Structured outputs, wire schema, and the repair loop [N-ORC-003, N-ORC-008, N-LLM-002, N-LLM-004]

- **Context.** Structured outputs support only a JSON Schema subset: no recursion, no
  `minimum`/`maximum`/`minLength`, `additionalProperties: false` everywhere. Forced `tool_choice` returns 400 on
  Sonnet 5.5. The Go SDK does not strip unsupported keywords.
- **Decision.**
  - `output_config.format` with a **wire schema generated from `internal/catalog`** (`make schema`); a test
    fails if the committed file drifts or contains an unsupported keyword.
  - Conditions are an `anyOf` of typed variants (number/scalar, number/between, enum/scalar, enum/list, bool,
    date/relative, date/absolute, date/null), so constrained decoding already rules out op/type mismatches.
    Ranges and limits the schema cannot express are enforced by the validator.
  - Adaptive thinking at effort `low` (thinking cannot be disabled on Sonnet 5.5); the stable system prompt
    carries `cache_control`.
  - **Repair loop:** on validation failure, the structured errors go back to the model as a follow-up turn;
    at most **2 repairs**, each recorded in `llm_calls` with purpose `repair`. Rules are never relaxed and
    nothing is auto-fixed silently; still invalid → 422 with the errors, and the UI offers regenerate or manual
    edit. `refusal` is shown and never repaired. `max_tokens` re-sends the same turn with the limit doubled.
  - For content, only errors on the **requested** channels trigger a repair; `valid_for` still reports both.
  - Issue lists sent back to the model are capped at 20 (`too_many_errors`), so a hostile answer cannot inflate
    the repair turn (red-team RT-1).
- **Alternatives.** Forced tool use (400 on current models); free-form JSON plus parsing (no constrained
  decoding); unlimited repairs (unbounded cost and latency).
- **Consequences.** At most 3 calls per task, each visible in the UI (attempts, tokens, cost, prompt version).
  The server-side refusal fallback beta stays **off** until the owner's smoke-live run verifies it.

## D-4 · Segment DSL and whitelist compiler [N-ORC-004, N-PLAN-011]

- **Context.** "LLM output is never executed directly as SQL" (R-AUD-1).
- **Decision.**
  - A fixed-depth DSL: `groups` ANDed, `conditions` inside a group ORed (CNF), so the schema is not recursive.
  - Fields come only from `internal/catalog`; each maps to a fixed SQL expression. Operators are typed
    (number, enum, bool, date). Relative time uses `*_days` operators, so the model never computes dates.
  - Null semantics are part of the contract: `not_within_last_days N` includes "never".
  - **Only the validator can construct `segment.ValidRule`**, and the compiler accepts only `ValidRule`, so no
    code path can compile unvalidated output. Every value is a `$n` parameter (`= ANY($n)` for lists).
  - Limits: ≤ 5 groups, ≤ 10 conditions per group, ≤ 20 in total, lists ≤ 50, days 1..3650.
  - Provable contradictions on the same field are errors; duplicates are warnings; an audience of 0 blocks
    approval.
  - **Consent is not a DSL field**: the model cannot target or untarget it (D-9).
  - The UI's primary display is a **server-rendered** description of the validated rule; the model's
    explanation is secondary. Manual edits (`PUT …/segment`) go through the same strict decoder.
- **Alternatives.** Model-written SQL with a parser/sandbox (unbounded attack surface); a recursive boolean
  tree (not expressible in structured outputs).
- **Consequences.** Some briefs cannot be expressed exactly; those parts go to `unsupported_criteria` and
  `assumptions`, which the UI shows. The official brief maps to
  `city = İstanbul AND last_cart_add_at within 30 days AND cart_abandoned`.

## D-5 · Character counting and personalization [N-PLAN-005, N-PLAN-012, N-ORC-005, N-LLM-001]

- **Context.** R-CNT-1/2 say "160 / 80 characters". Bytes are wrong for Turkish (`ş` is 2 bytes); real SMS
  billing counts GSM-7 or UCS-2 segments.
- **Decision.**
  - A character is a **Unicode code point** (Go `utf8.RuneCountInString`, JS `[...s].length`); shared test
    vectors run in both Go and Vitest.
  - SMS ≤ 160 **after worst-case personalization**: each variable is replaced by the longest value among the
    consented recipients of that channel. First names over 20 runes render the per-language fallback instead
    (no mid-name truncation), so the worst case is `max(min(longest, 20), fallback)`.
  - Encoding (GSM-7 / UCS-2) and segment count are shown as information; Turkish letters force UCS-2.
  - Variables: `{{first_name}}` and `{{city}}` only. Rendering is single-pass, so a user named `{{city}}` stays
    literal. Control, bidi and line-separator characters are stripped from values; email HTML is escaped.
  - Defense in depth: a rendered SMS over 160 at send time is a permanent `content_too_long` failure and an
    alert metric.
- **Alternatives.** Enforce a single real segment (≤ 70 with Turkish letters; stricter than the case); count
  UTF-16 units (differs between languages' natural APIs).
- **Consequences.** A Turkish SMS of 93 code points is valid but costs 2 UCS-2 segments; the UI says so.
  Switching to single-segment is ~1 h (rule, vectors, prompt budget, UI).

## D-6 · Postgres is the datastore and the queue [L-4, N-PLAN-010, N-ORC-013]

- **Context.** R-SND-1 asks for an asynchronous queue; the choice is ours.
- **Decision.** Transactional outbox plus `FOR UPDATE SKIP LOCKED`.
  - Approve is **one transaction**: `SELECT … FOR UPDATE NOWAIT` on the campaign, re-validation of the
    content, a set-based `INSERT INTO messages … SELECT … FROM users WHERE <compiled rule> AND
    <channel>_consent ON CONFLICT DO NOTHING`, and a guarded `content_ready → sending` transition. No dual
    write between a DB and a broker. 5000 users enqueue in ~0.16 s.
  - Concurrent approves of *different* campaigns enqueue one at a time behind a transaction-scoped advisory
    lock [N-ORC-013]: ten bulk inserts in parallel took 14–55 s each on the compose Postgres and timed out;
    in sequence all ten finish in seconds (stress S3).
  - Workers claim batches with `UPDATE … WHERE id IN (SELECT … FOR UPDATE SKIP LOCKED LIMIT n)`, set a 30 s
    lease and re-check consent in the same statement.
  - The dashboard reads the same tables: one aggregate query per campaign every 500 ms, shared by all
    subscribers of that campaign, pushed over SSE [N-ORC-006].
- **Alternatives.** Postgres + Redis Streams; NATS JetStream or Kafka.
- **Consequences.** Everything is inspectable with SQL and there are fewer moving parts. The ceiling is
  Postgres write throughput: at 100 msg/s per channel this is far away. A broker becomes the right move at
  thousands of messages per second, many channels or many independent consumers — then the outbox stays and
  a relay publishes from it.

## D-7 · The rate limiter: ≤ 100 per channel in any 1-second window [N-ORC-012 ✅ ACCEPTED, N-DEL-001, N-DEL-008]

- **Context.** R-SND-2 says at most 100 messages per second **per channel**. The mock provider measures
  arrivals independently (max requests in any sliding 1 s window). A token bucket with burst does not bound
  every window; pacing slot *reservations* also failed: the CP1 review reproduced up to 141 requests in 1 s at
  the provider from cold connections, timer delays and replica takeover.
- **Decision.**
  - **One active dispatcher per channel**, elected with `pg_try_advisory_lock` on a dedicated connection;
    other replicas are hot standbys. Replicas add failover, not throughput: the limit caps throughput anyway.
  - A **two-phase sliding-window log limiter**: `Acquire` admits a send; the send's real departure time is
    committed from `httptrace.WroteRequest`. Slot *s* may start only when the departure of slot *s − N* is at
    least **W = 1 s + 25 ms** old.
  - **The stated assumption:** the one-way network delay plus the provider's decode time is below the 25 ms
    guard. On a Docker bridge it is well under 1 ms. Under that assumption no 1 s window at the provider can
    hold more than N arrivals. Throughput ≈ 97.6/s.
  - Takeover safety: a new lock holder cannot know what the previous one sent, so it waits W plus the
    lock-loss detection bound (≈ 4 s) before its first send, also at first start. Queued sends are tied to the
    lock term's context and are released when the lock is lost. net/http's hidden replay of POSTs is disabled
    (`GetBody = nil`), so every request on the wire went through the limiter.
  - **Switching to the assumption-free variant (B) is one line:** commit the slot at response receipt instead
    of at `WroteRequest` (the `tk.Departed(...)` call site in `internal/delivery/dispatcher.go`). It needs no
    network assumption and gives ≈ 84/s at default latency.
- **Alternatives.** Token bucket (bursts over 100 in some windows); a count safety margin (not a bound — the
  live run showed 101 with margin 0 before the fix); B as above.
- **Consequences.** Measured on compose at 100/s with safety margin 0: 5000 users, 7542 messages, provider max
  **100** requests in any 1 s window per channel, 0 provider-limiter rejections, 0 duplicates, done in 72 s.
  Stress S1/S2/S7 assert the same bound. Cost: ~4 s of silence after start or takeover; S1 averages 84–90/s per channel over the whole run.

## D-8 · Retry policy [N-DEL-002, N-DEL-003, N-DEL-007 ✅ CHANGE implemented, N-DEL-009]

- **Context.** R-SND-4 leaves the strategy to us; R-RES-3 makes the provider fail, delay and throttle on purpose.
- **Decision.** A policy table (`internal/delivery/retry.go`):

  | Provider outcome | Class | Action | Counts toward 5 attempts |
  |---|---|---|---|
  | 2xx | success | `sent` | — |
  | 400 / 404 / 422 / other 4xx | permanent | `failed`, no retry | — |
  | 401 / 403 | config | pause the channel 30 s, message stays queued, alert | no |
  | 429 | throttled | pause the **whole channel** until `Retry-After` (default 1 s, cap 30 s) | no; separate cap of 20 |
  | 408 / 5xx | transient | full-jitter backoff: base 1 s, ×2, cap 60 s | yes |
  | timeout / connection dropped after it was made | ambiguous | retry with the **same Idempotency-Key**; when attempts run out, up to 3 confirmation replays (D-10) | yes |
  | no connection could be made (refused, DNS, dial timeout) | unreachable | pause the channel 2 s, message stays queued | **no** |

  - 429s answering requests sent before the current pause belong to the same overload event: they may extend
    the pause to their own `Retry-After` but never restart it [N-DEL-002].
  - **Unreachable** [owner CHANGE on N-DEL-007]: the HTTP client knows whether a connection was ever assigned
    to the request (`httptrace.GotConn`). If not, not one byte reached the provider, so the try is not an
    attempt, and queued messages survive an outage of any length instead of burning their budget.
- **Alternatives.** Count every connection error (an outage over ~30 s fails messages); retry forever (hides
  permanent errors); a fixed delay (synchronized retry storms).
- **Consequences.** Exhausted messages end `failed` with `last_error`, visible in the UI and the messages API.
  A "retry failed" button is not built (P2).

## D-9 · Consent enforcement [R-AUD-2]

- **Decision.** Two independent checks, neither under the model's control:
  1. **Enqueue:** the approve `INSERT … SELECT` has `AND u.<channel>_consent` in SQL.
  2. **Claim:** the claim statement joins `users` again; a recipient who withdrew consent after approval
     becomes `skipped_no_consent` and is never sent.
- **Consequences.** Stress invariant I2 joins the provider's delivered list with the users table: 0 deliveries
  without consent in every scenario.

## D-10 · Duplicate prevention in layers: at-least-once + idempotent receiver = exactly-once effect [N-DEL-009]

- **Context.** R-SND-3: the same message must never reach the same person twice. Crashes between "provider
  accepted" and "DB updated" are unavoidable.
- **Decision.**
  1. `UNIQUE (campaign_id, user_id, channel)` on `messages`.
  2. Idempotent approve: `Idempotency-Key` required; a repeated key replays the stored response; a different
     key on an approved campaign is 409; a concurrent approve is 409 (`NOWAIT` lock).
  3. `SKIP LOCKED` claims: two workers never hold the same row.
  4. Leases plus `Idempotency-Key = message.id` towards the provider: a crash after send makes the lease expire,
     the retry carries the same key, and the provider returns the stored answer without delivering again.
  5. Proof: the provider's `/stats` reports `duplicate_recipient_deliveries`, asserted to be 0 in tests, stress
     and live runs.
  6. **Confirmation replays** [N-DEL-009, owner CHANGE]: a try without an answer may have been delivered. When
     such a message runs out of attempts it gets up to 3 more tries with the same key, so the provider's stored
     answer confirms a recorded delivery instead of the DB reporting it as `failed`.
- **Alternatives.** Exactly-once delivery without receiver idempotency (impossible across a network).
- **Consequences.** A message is delivered at most once. Residual gap: if the remaining attempts *and* all three
  replays also hit faults, a delivered message can still end `failed` (its `last_error` says "delivery
  unconfirmed"); stress I5 bounds the provider/DB difference by exactly those messages.

## D-11 · Model choice [L-7, N-PLAN-001]

- **Decision.** Default `claude-sonnet-5-5`, overridable with `ANTHROPIC_MODEL`.
- **Alternatives.** Opus 5.5 (higher quality, twice the price, slower first token for a streaming UI);
  Haiku 4.5 (cheapest, not evaluated).
- **Consequences.** We ship the model our evals measured (through `claudecode`, which runs Sonnet). The
  per-call cost shown in the UI comes from the pricing table in `internal/llm/prices.go`.

## D-12 · Streaming transports [N-ORC-002, N-ORC-006, N-WEB-004]

- **Decision.**
  - Content generation: `POST …/content/generate` answers `text/event-stream` (`meta`, `delta`, `repairing`,
    `result`, `error`), read with `fetch` and a small SSE parser. Client disconnect cancels the model call.
    The browser parses partial JSON tolerantly **for display only**; the server's `result` is canonical.
  - Dashboard: `GET …/events` (EventSource) sends a complete `snapshot` first and on every reconnect, so the
    client simply replaces its state; a ping every 15 s.
- **Alternatives.** WebSockets (bidirectional, not needed); LISTEN/NOTIFY instead of polling (the next step if
  many campaigns run at once).

## D-13 · Spend guard and secrets [N-PLAN-014, L-10]

- **Decision.** `make up`, `test`, `e2e` and `stress` force `LLM_PROVIDER=mock`. Only `make smoke-live` and
  `make eval PROVIDER=anthropic` can reach the paid API, and both refuse without `CONFIRM_SPEND=yes`. Agents
  were denied that string in project settings, so only the owner could run them. No agent read `.env`.
  `make sanitize-check` runs gitleaks over git history and the session exports.

## D-14 · Test isolation [N-ORC-010]

- **Decision.** `make test` runs `go vet`, `go test -race` and Vitest **in containers** on a Docker network
  without internet, with a tmpfs Postgres; each Go test creates and drops its own random database. No key is
  needed (R-AI-2). `make e2e` runs Playwright in the pinned Playwright image against the compose stack and
  reaches the app as `http://api:8080`, a non-localhost origin on purpose (it caught a secure-context-only
  browser API, V-017).

## D-15 · The agent workflow [L-8, N-ORC-001, N-ORC-011]

- **Context.** One build day; three independent areas (LLM, delivery, web) plus glue.
- **Decision.**
  - Phase 0 (orchestrator, plan approved by the owner): contracts in `docs/contracts/`, Go interfaces,
    migrations, metric and config registries, a compiling skeleton and a healthy compose stack. This is what
    lets builders work in parallel without waiting on each other.
  - W1: three builder subagents (llm on Opus; delivery and web on Sonnet), each in its own git worktree with a
    task file in `docs/tasks/`. They return structured results (tests run, files, need entries, validation-log
    entries). The orchestrator reviews the diff, re-runs `make test`, and merges with `--no-ff`.
  - Read-only verifiers at checkpoints: `reliability-reviewer` on delivery, `llm-red-team` on the LLM path
    (hostile tests in a worktree), `spec-auditor` for traceability.
  - Decisions taken without the owner go to `need.md`; the owner answers asynchronously
    (ACCEPT / CHANGE / DISCUSS). AI mistakes caught by tests or review go to `ai-sessions/validation-log.md`.
- **Alternatives.** One long session (no parallelism, context exhaustion); fully autonomous agents without
  checkpoints (unreviewed merges).
- **Consequences.** The verifiers found real defects that green unit tests missed: the rate bound under
  takeover and cold connections, an empty rule from a misspelled manual edit, NUL bytes failing inserts. The
  launch log is in `ai-sessions/README.md`.

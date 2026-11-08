# need.md — Decisions taken without the owner, and questions for the owner

This file is the asynchronous control surface between the AI agents and the owner. The owner is not always online. Whenever an agent needed a decision and continued without the owner, the decision and the question are logged here. The owner reviews them later and answers each one.

Protocol: KICKOFF.md §4.

**How the owner answers:** write one of these in the entry's **Owner response** line:

- `ACCEPT`
- `CHANGE: <instruction>`
- `DISCUSS`

The next agent that owns the area implements the answer and updates the status.

**Status legend:**

| Emoji | Status | Meaning |
|---|---|---|
| 🟡 | OPEN | Decided provisionally; owner review pending. |
| ❓ | QUESTION | No safe default; needs the owner. |
| 🔁 | CHANGE REQUESTED | Not yet implemented. |
| ✅ | ACCEPTED / RESOLVED | Done. |

---

## Digest (maintained by the orchestrator only)

_Last updated: 2026-11-08 03:05 by the orchestrator._

**Needs the owner soon (high impact):**

- ❓ N-PLAN-009: repo visibility and reviewer access, before the first push (commit identity is settled).

**Review when convenient (medium):** N-ORC-003 (wire schema typing), N-ORC-004 (contradictions), N-ORC-005 (long names), N-PLAN-001, 002, 004, 005, 010, 011, 014.

**Review when convenient (low):** N-ORC-002, 006, 007, 009, 010, N-PLAN-008, 012.

**Answered:**

- ✅ N-ORC-001: Agent-tool subagents instead of workflows (Pro plan).
- ✅ N-ORC-008: refusal fallback beta off until smoke-live verifies it.
- ✅ N-PLAN-003: the owner will get an API key.
- ✅ N-PLAN-006: no extension; one build day, then verification.
- ✅ N-PLAN-007, N-PLAN-013: superseded; no ultracode.

---

## Entries

### ✅ N-OWN-001 · Owner decisions recorded during planning · impact: high
- **Date / agent / session:** 2026-11-08 01:45 · owner (recorded by planning) · 00-planning
- **Requirement(s):** R-TECH-1, R-RES-1, R-DEL-4/5
- **Context:** Answers to the planning session's open questions.
- **Options:** —
- **Decision taken:** The owner decided all of the following:
  - The backend is **Go**.
  - All repo docs are in **English**.
  - The owner uses Claude Code on a **subscription** and has **no API key**.
  - During development, real-model testing goes through the owner's subscription, with **Claude Sonnet** as the LLM.
  - The delivered project must call the Claude API directly through `ANTHROPIC_API_KEY`.
- **Cost to change later:** —
- **Question for owner:** —
- **Owner response:** (given in chat)
- **Status:** ACCEPTED

### 🟡 N-PLAN-001 · Delivered default model is `claude-sonnet-5-5` · impact: medium
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-AI-1, R-RES-1
- **Context:** The claude-api skill's general default is `claude-opus-5-5`. All of our real-model prompt work and evals run on Sonnet, through the subscription.
- **Options:**
  - A) Opus 5.5 ($4/$20 per MTok, higher quality, slower).
  - B) Sonnet 5.5 ($2/$10, faster; the model we actually validate against).
  - C) Haiku 4.5 (cheapest, untested).
- **Decision taken:** B. We ship the model our evals measured, at half the cost and lower latency for a streaming UI. `ANTHROPIC_MODEL` overrides it, and the README documents the override.
- **Cost to change later:** one env default + README line (~5 min). A different model would also need an eval re-run.
- **Question for owner:** Keep Sonnet 5.5 as the default?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-PLAN-002 · Keep the dev-only `claudecode` LLM adapter in the delivered repo · impact: medium
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-AI-1, R-AI-2
- **Context:** The owner has no API key, so real-model testing uses `claude -p` (Claude Code headless) on the owner's subscription. It runs on the host only and is never used in tests or CI, nor as a default. A probe on 2026-11-08 confirmed that it works: structured output, Sonnet 5.5, 1.7 s.
- **Options:**
  - A) Keep it, clearly documented as a dev tool.
  - B) Remove it before submission.
  - C) Keep it behind a build tag.
- **Decision taken:** A. It is honest evidence of how the prompts and evals were validated without a key, and it is a good review-call talking point. DECISIONS.md will state that it is dev-only and why.
- **Cost to change later:** B ~15 min (delete the package, the config option, the make target and the docs).
- **Question for owner:** OK to ship it as a documented dev-only adapter?
- **Owner response:**
- **Status:** OPEN

### ✅ N-PLAN-003 · The live Anthropic API path cannot be verified without a key · impact: high
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-AI-1, R-RES-1, R-DEL-2
- **Context:** Reviewers will run the `anthropic` adapter with their own key. A wrong request shape (e.g. an unsupported schema keyword) would only fail on their machine, and the LLM integration carries 25% of the score.
- **Options:**
  - A) Contract tests with recorded fixtures only.
  - B) A + one real `make smoke-live` run with a small paid credit (a few cents of usage).
- **Decision taken:** A is planned regardless (KICKOFF §6.8). B is strongly recommended. The case itself says: "if you want to try a real model, you can get your own API key from Claude Console".
- **Cost to change later:** —
- **Question for owner:** Can you create a Console API key with a small credit for **one** smoke run on Tuesday morning? You would put it in `.env` yourself, and the agents never read it.
- **Owner response:** ACCEPT (chat, 2026-11-08): "API key alabilirim" ("I can get an API key").
- **Follow-up (planning):**
  - Set a spend limit in the Console.
  - Add the key to `.env` only on Monday.
  - The owner personally runs `make smoke-live CONFIRM_SPEND=yes` and one `make eval PROVIDER=anthropic CONFIRM_SPEND=yes`.

  See N-PLAN-014.
- **Status:** ACCEPTED

### 🟡 N-PLAN-004 · Behavior when `ANTHROPIC_API_KEY` is missing at runtime · impact: medium
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-RES-1, R-DEL-2
- **Context:** A reviewer might run `docker compose up` before creating `.env`.
- **Options:**
  - A) `LLM_PROVIDER=auto` falls back to the deterministic mock and shows a prominent UI banner.
  - B) Fail fast: the API refuses to start.
  - C) Start, but return 503 from LLM endpoints.
- **Decision taken:** A. The app always demos end-to-end and the banner makes the mode obvious. An explicit `LLM_PROVIDER=anthropic` with no key fails fast with a clear error.
- **Cost to change later:** ~20 min (config + UI banner text).
- **Question for owner:** Is the automatic mock fallback acceptable?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-PLAN-005 · How "160 characters" is counted · impact: medium
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-CNT-1, R-CNT-2
- **Context:** Byte length is wrong for Turkish (`ş` is 2 bytes). Real SMS billing uses GSM-7 (160 per segment) or UCS-2 (70 per segment, in UTF-16 units). Turkish letters force UCS-2.
- **Options:**
  - A) 160 Unicode code points after worst-case personalization; show encoding and segment count as information.
  - B) Enforce a single real segment (≤ 70 when Turkish letters are present).
  - C) Count UTF-16 units.
- **Decision taken:** A. It matches the case's wording ("160 karakter"), counts the same in Go and JS, and still shows domain awareness through the segment display.
- **Cost to change later:** B ~1 h (rule, test vectors, prompt budget text, UI).
- **Question for owner:** Keep A, or enforce single-segment (B)?
- **Owner response:**
- **Status:** OPEN

### ✅ N-PLAN-006 · Time budget and scope order · impact: high
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** all; B-1..B-4
- **Context:** The deadline is Tue 2026-11-10 16:00, about 62 h after planning ended. The case says: "if you need extra time, just write to us".
- **Options:**
  - A) Keep the deadline: P0 → P1 → B-4 (CI) → B-1 (scheduled) → B-3 (language) → B-2 (A/B).
  - B) Ask Insider for 1–2 extra days and do the same plan with less risk.
- **Decision taken:** A is the working plan. Bonus work starts only when P0 and P1 are green.
- **Cost to change later:** —
- **Question for owner:** Do you want to ask for extra time? It is an outward-facing email, so only you can send it.
- **Owner response:** No extension (chat, 2026-11-08): build everything in one day, then use the remaining time for review and verification.
- **Result:**
  - Sunday 11-08 is the build day.
  - Monday 11-09 and Tuesday morning are for verification and fixes.

  KICKOFF §0 and §12.4 were updated.
- **Status:** ACCEPTED

### ✅ N-PLAN-007 · Builders run as separate worktree sessions, not as subagents · impact: low
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-DEL-6
- **Context:** A subagent's internal reasoning is not part of the parent session's `/export`. The evaluators read `ai-sessions/`.
- **Options:**
  - A) 3 builder sessions in worktrees (owner opens 3 terminals).
  - B) Background subagents with `isolation: worktree` (less juggling, worse exports).
  - C) Agent teams (experimental, no resume, high token use).
- **Decision taken:**
  - Builders: A.
  - Verifiers: subagents, for clean context.
  - Agent teams: optional, only for the Phase 2 review.
- **Cost to change later:** low (task files work either way).
- **Question for owner:** OK?
- **Owner response:** Superseded (chat, 2026-11-08): "ultracode ile çalıştıracağım" ("I'll run it with ultracode").
- **Result:**
  - Builders now run as workflow agents launched by the orchestrator: one workflow per stage, isolated copies, structured results. See KICKOFF §12.0.
  - The manual worktree sessions remain the fallback (§12.3).
  - Workflow transcripts are harvested from the session directory (§13).
- **Update (owner, chat, 2026-11-08 03:00):** Superseded again by N-ORC-001. The owner is on Claude Pro, so there is no ultracode. Builders run as Agent-tool subagents with `isolation: worktree`, and their transcripts are collected by `scripts/harvest-sessions.sh`.
- **Status:** RESOLVED

### 🟡 N-PLAN-008 · Tooling installed for the project · impact: low
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-DEL-6 (presentation: MCP/tooling section)
- **Context:** The owner asked the planning session to install useful skills and repos.
- **Options:** —
- **Decision taken:**
  - **Project-scope plugins** (`.claude/settings.json`):
    - gopls-lsp and typescript-lsp for code intelligence;
    - context7 (MCP) for up-to-date library docs;
    - frontend-design (skill);
    - playwright (MCP) for browser checks;
    - pr-review-toolkit for review agents;
    - session-report for a usage HTML report.
  - **Global CLIs:** gitleaks, golangci-lint, ccusage, typescript-language-server.
  - **Not installed:** obra/superpowers. Its brainstorming and TDD workflows conflict with this file's non-blocking protocol and with the timeline. Built-in `/code-review`, `/security-review` and `/simplify` cover review.
- **Cost to change later:** one command each.
- **Question for owner:** Anything to add or remove?
- **Owner response:**
- **Status:** OPEN

### ❓ N-PLAN-009 · Repo visibility, reviewer access and commit identity · impact: medium
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-DEL-1, R-DEL-6
- **Context:** The repo is local only (no remote yet). Commits carry your git name and email in their metadata.
- **Options:**
  - Repository:
    - A) A private GitHub repo now, made public at submission.
    - B) Private, with reviewers invited.
    - C) Public from the start.
  - Commit email:
    - your current git email;
    - a GitHub noreply address.
- **Decision taken:** Nothing is pushed until you decide; pushing is a hard stop. The planning default is A, with a noreply commit email set in this repo's local git config.
- **Cost to change later:** low before the first push; rewriting history afterwards is high.
- **Question for owner:** A, B or C? Should we use the noreply email for commits in this repo?
- **Owner response:** Commit identity ACCEPTED (chat, 2026-11-08 03:00): this repo's local `user.email` is the owner's GitHub noreply address (set before the first commit). Repo visibility: the owner decides before the first push.
- **Status:** QUESTION (visibility only)

### 🟡 N-PLAN-010 · Postgres as both the datastore and the queue · impact: medium
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-SND-1, R-SND-3, R-DEL-2
- **Context:** The case leaves the DB and queue to us.
- **Options:**
  - A) Postgres outbox + `SKIP LOCKED`.
  - B) Postgres + Redis (streams).
  - C) Postgres + NATS JetStream or Kafka.
- **Decision taken:** A. Enqueue happens in the same transaction as the approval (no dual-write), dedup comes from a unique constraint, and everything is inspectable with SQL. Postgres handles 200 msg/s easily, and there are fewer moving parts to explain. DECISIONS.md will name the scale point where B or C would be the right move.
- **Cost to change later:** high after Phase 1.
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-PLAN-011 · Interpretation of the official example brief · impact: medium
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-FLOW-1, R-AUD-1
- **Context:** The brief reads "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılar". "Alışveriş yapmayan" can mean either of two things.
- **Options:**
  - A) Added to cart in the last 30 days and has **not purchased since** (`cart_abandoned`, a generated column).
  - B) Added to cart in the last 30 days and has **no purchase in the last 30 days**.
- **Decision taken:** A is the golden expected rule, because it matches the marketing intent of an abandoned-cart reminder. The model must surface the other reading in `assumptions`, and the UI shows it.
- **Cost to change later:** ~10 min (golden eval expectation + few-shot example).
- **Question for owner:** A or B?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-PLAN-012 · Allowed personalization variables · impact: low
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-CNT-3
- **Context:** The case says "variables like `{{first_name}}`".
- **Options:**
  - A) `{{first_name}}` only.
  - B) `{{first_name}}` + `{{city}}`.
  - C) More fields (last name, tier, …).
- **Decision taken:** B. It shows that the mechanism is general while keeping the worst-case length logic simple. Unknown variables are rejected.
- **Cost to change later:** low (allowlist + tests).
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### ✅ N-PLAN-013 · Effort level under ultracode: `xhigh` rather than `max` · impact: medium
- **Date / agent / session:** 2026-11-08 02:40 · planning · 00-planning
- **Requirement(s):** — (process / usage limits)
- **Context:** Ultracode turns on workflow orchestration at the session's effort level. Every workflow agent inherits that cost. The owner is on a subscription, and the build has to fit into one day.
- **Options:**
  - A) `claude --effort ultracode`, which sets `xhigh`.
  - B) `--effort max`, then `/effort ultracode`, which keeps `max`.
- **Decision taken:** A is the default in the KICKOFF launch command. Ultracode already adds quality through independent agents and cross-checking. `max` on every agent would mostly burn usage limits.
- **Cost to change later:** none; it's a launch flag.
- **Question for owner:** Use `xhigh` + ultracode (recommended), or `max` + ultracode?
- **Owner response:** Neither (chat, 2026-11-08 03:00): Claude Pro, workflows are off, no ultracode. See N-ORC-001.
- **Status:** RESOLVED

### 🟡 N-PLAN-014 · API key handling and spend guard · impact: medium
- **Date / agent / session:** 2026-11-08 02:40 · planning · 00-planning
- **Requirement(s):** R-RES-1, R-AI-2, R-DEL-6
- **Context:** Once the owner's key is in `.env`, `LLM_PROVIDER=auto` makes any agent-run `docker compose up`, e2e test or eval call the paid API.
- **Options:**
  - A) Enforce the guard in code and settings.
  - B) Trust the agents to remember.
- **Decision taken:** A, implemented as follows:
  - `make up`, `test`, `e2e` and `stress` force `LLM_PROVIDER=mock`.
  - `make eval` needs an explicit `PROVIDER`.
  - `make smoke-live` and `make eval PROVIDER=anthropic` refuse to run without `CONFIRM_SPEND=yes`.
  - `.claude/settings.json` denies agents `make smoke-live` and any command containing `CONFIRM_SPEND=yes`, so only the owner can spend money.
  - The owner adds the key to `.env` only on Monday, and sets a spend limit in the Console.
  - Never paste the key into any Claude chat (it would end up in the transcripts), and never export it in a shell profile (Claude Code itself would then bill the API instead of the subscription).
- **Cost to change later:** low.
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### ✅ N-ORC-001 · No workflow runtime: builders run as Agent-tool subagents · impact: medium
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-DEL-6 (process)
- **Context:** The orchestrator session has no Workflow tool and no `/workflow-authoring` skill. The owner is on Claude Pro, where workflows are off and usage limits are tight.
- **Options:** A) Agent-tool background subagents with `isolation: worktree` and a per-agent model. B) Manual worktree sessions (§12.3). C) Restart with ultracode.
- **Decision taken:** A. Models: llm builder and all verifiers → opus; delivery and web builders and fix agents → sonnet; mechanical steps → haiku. KICKOFF §12.0 was rewritten accordingly. Transcripts are collected by `scripts/harvest-sessions.sh`.
- **Cost to change later:** low (the task files work for both A and B).
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-08 03:00).
- **Status:** ACCEPTED

### 🟡 N-ORC-002 · Transport for streamed content generation · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-FLOW-2
- **Context:** `EventSource` is GET-only, but generation is started by a POST (KICKOFF §5.5).
- **Options:** A) The POST itself answers with `text/event-stream`; the browser reads it with `fetch` and a small SSE parser. B) POST starts a job, then a GET `EventSource` subscribes to it.
- **Decision taken:** A. One request, no server-side job store, and a client disconnect cancels the request context, which cancels the upstream LLM call. The campaign dashboard still uses a GET `EventSource`, because it benefits from native reconnect.
- **Cost to change later:** ~1.5 h (server job buffer + client switch).
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-ORC-003 · Value typing in the segment wire schema · impact: medium
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-AI-1, R-AUD-1
- **Context:** Structured outputs support `anyOf`, `enum`, `null` and `format: date`, but not `minimum`/`maximum` or length limits (claude-api skill, checked 2026-11-08).
- **Options:** A) One condition shape with `value` as a loose union. B) `anyOf` of typed condition variants (number compare, number between, enum scalar, enum list, bool, date relative, date absolute, date null), each restricting `field` and `op` to compatible enums.
- **Decision taken:** B. Constrained decoding then cannot produce an operator that does not fit the field's type. Ranges, list sizes and per-field enum values are still enforced by the validator. The schema is generated from `internal/catalog`; a test fails if the committed `docs/contracts/segment.wire.schema.json` drifts or contains an unsupported keyword.
- **Cost to change later:** ~1 h (generator + fixtures).
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-ORC-004 · Contradictions, duplicates and empty audiences · impact: medium
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-AUD-1, R-FLOW-1, R-FLOW-3
- **Context:** KICKOFF §6.3 leaves "warning or error" for contradictions open.
- **Options:** A) All warnings. B) Provable contradictions are errors, duplicates are warnings.
- **Decision taken:** B. A contradiction is detected only where it is provable without data: two single-condition groups on the same field whose ranges cannot overlap (e.g. `age > 60` and `age < 20`), or `is_null` together with a comparison on the same field. These are errors and go through the repair loop. Exact duplicate conditions are warnings. An audience of 0 reachable recipients is not a validation error, but approval is refused with a clear message.
- **Cost to change later:** ~30 min (validator rule + tests).
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-ORC-005 · Long first names in worst-case personalization · impact: medium
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-CNT-1, R-CNT-3
- **Context:** The SMS budget is computed with the longest value of each variable. One 40-rune name in the audience would shrink the budget for everyone (KICKOFF §6.5 asks for this decision).
- **Options:** A) No cap. B) Truncate names to 20 runes. C) Names longer than 20 runes render the per-language fallback ("Değerli müşterimiz" / "Valued customer").
- **Decision taken:** C. No name is cut in the middle. The same rule is used for validation and at send time, so the worst case is `max(min(longest, 20), len(fallback))`. The same cap applies to `{{city}}` (the longest seeded city is well under 20).
- **Cost to change later:** ~20 min (content constants + test vectors).
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-ORC-006 · Source of live dashboard updates · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-FLOW-5, R-OBS-1
- **Context:** The worker updates message rows; the API has to push progress to browsers.
- **Options:** A) One hub per active campaign polls a single aggregate query every 500 ms and fans out to all subscribers. B) Postgres `LISTEN/NOTIFY` from the worker.
- **Decision taken:** A. One query per campaign regardless of subscriber count, it survives database restarts without re-subscribing, and every event is a full snapshot, so reconnects are trivial. B is named in DECISIONS.md as the next step at higher scale.
- **Cost to change later:** ~1 h.
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-ORC-007 · Seed timestamps are relative to seed time · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-RES-2
- **Context:** Briefs use relative time ("last 30 days"). Fixed absolute dates would make audiences shrink as days pass.
- **Options:** A) Fixed absolute dates. B) Deterministic offsets from the moment the seed runs.
- **Decision taken:** B. The generator is deterministic in everything except the anchor time; the golden eval compares rules on the same database, so results stay comparable.
- **Cost to change later:** ~10 min.
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### ✅ N-ORC-008 · Thinking and refusal handling in the anthropic adapter · impact: medium
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-AI-1, R-RES-1
- **Context:** Sonnet 5.5 returns 400 for `thinking: disabled`. The claude-api skill recommends the server-side refusal fallback beta by default.
- **Options:** A) Adaptive thinking at effort `low`, no beta features. B) A plus the refusal fallback beta.
- **Decision taken:** A. Adaptive thinking at effort `low` for both tasks. A `refusal` stop reason is shown to the user with its category and is never repaired. The refusal fallback beta is **off by default** behind `ANTHROPIC_REFUSAL_FALLBACK=off`, and may only be turned on after Monday's `make smoke-live` has verified it. The request sent from a reviewer's machine stays as plain as possible.
- **Cost to change later:** ~30 min.
- **Question for owner:** —
- **Owner response:** CHANGE applied before implementation (chat, 2026-11-08 03:00): "refusal fallback beta'sı varsayılan olarak KAPALI olsun, Pazartesi smoke-live ile doğrulanmadan açılmasın."
- **Status:** ACCEPTED

### 🟡 N-ORC-009 · Migration runner, router and SPA embedding · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-TECH-1, R-DEL-2
- **Context:** KICKOFF leaves the migration approach and libraries open.
- **Options:** A) Small in-repo pieces: embedded SQL runner (advisory lock, one transaction per file, `schema_migrations`), stdlib `net/http` routing, SPA embedded behind the `spa` build tag. B) goose + chi.
- **Decision taken:** A. Each piece is under 100 lines, needs no extra dependency, and is easy to explain in the review call.
- **Cost to change later:** ~1 h.
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

### 🟡 N-ORC-010 · How `make test` isolates tests · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-AI-2, R-DEL-3
- **Context:** `make test` must need only Docker, no key and no internet.
- **Options:** A) Go and web tests run in containers whose dependencies are baked in at build time, on an `internal` network with a throwaway Postgres; the mock provider runs in-process via `httptest`. B) Tests on the host.
- **Decision taken:** A for `make test`, under a separate compose project so it never touches the dev stack. `make test-host` runs the same Go tests on the host against the shared Postgres (port 55432) for fast builder loops. The mock provider's server code lives in `internal/mockprovider` so tests can start it in-process; `cmd/mockprovider` is a thin main.
- **Cost to change later:** low.
- **Question for owner:** OK?
- **Owner response:**
- **Status:** OPEN

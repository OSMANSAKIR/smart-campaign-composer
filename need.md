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

_Last updated: 2026-11-11 with the owner's answers to the B-3 entries (planning session, on the owner's instruction)._

**Needs the owner soon (high impact):** none.

**Phase 2 entries answered (owner, chat, 2026-11-11):** N-ORC-014, N-ORC-015, N-ORC-016, N-LLM-006 and
N-WEB-005 accepted; N-ORC-017 (pre-submission polish) accepted.

**B-3 entries answered (owner, chat, 2026-11-11, after the owner's live test):** N-ORC-018, N-LLM-007,
N-ORC-019 and N-WEB-006 accepted. No entry is open.

**Implemented tonight:** ✅ N-WEB-001 → B (manual rule editor, F2), ✅ N-LLM-005 change (cost_recorded, F6).

**Answered:** every other entry is ✅ ACCEPTED or RESOLVED as of 2026-11-10, including N-PLAN-009 (A:
private GitHub repo now, public at submission; noreply commit address) and N-PLAN-005 (keep code points;
encoding and segments shown as information).

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

### ✅ N-PLAN-001 · Delivered default model is `claude-sonnet-5-5` · impact: medium
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
- **Owner response:** ACCEPT
- **Status:** ACCEPTED

### ✅ N-PLAN-002 · Keep the dev-only `claudecode` LLM adapter in the delivered repo · impact: medium
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
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

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

### ✅ N-PLAN-004 · Behavior when `ANTHROPIC_API_KEY` is missing at runtime · impact: medium
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
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-PLAN-005 · How "160 characters" is counted · impact: medium
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
- **Owner response:** KEEP A.

  Enforce a maximum of 160 Unicode code points after worst-case personalization. The character limit should be independent of SMS encoding and segment count.

  Turkish characters should be preserved as entered by the user, without automatic transliteration. Encoding (GSM-7 / UCS-2) and estimated SMS segment count should be calculated separately and displayed as informational details.

  A single-segment restriction is not required. Messages exceeding one SMS segment can be handled through concatenated SMS, with the corresponding segment count reflected in the UI.
- **Status:** ACCEPTED

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

### ✅ N-PLAN-008 · Tooling installed for the project · impact: low
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
- **Owner response:** No: these skills and repos are fine but don't forget to put this skill and repos names to slide or other presentation that we will be build end of this project.
- **Status:** ACCEPTED

### ✅ N-PLAN-009 · Repo visibility, reviewer access and commit identity · impact: medium
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
- **Owner response:** A (chat, 2026-11-10): a private GitHub repo now, made public at submission. Commits use the GitHub noreply address, already set in this repo's local git config.
- **Status:** ACCEPTED

### ✅ N-PLAN-010 · Postgres as both the datastore and the queue · impact: medium
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
- **Owner response:** OK
- **Status:** ACCEPTED

### ✅ N-PLAN-011 · Interpretation of the official example brief · impact: medium
- **Date / agent / session:** 2026-11-08 02:00 · planning · 00-planning
- **Requirement(s):** R-FLOW-1, R-AUD-1
- **Context:** The brief reads "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılar". "Alışveriş yapmayan" can mean either of two things.
- **Options:**
  - A) Added to cart in the last 30 days and has **not purchased since** (`cart_abandoned`, a generated column).
  - B) Added to cart in the last 30 days and has **no purchase in the last 30 days**.
- **Decision taken:** A is the golden expected rule, because it matches the marketing intent of an abandoned-cart reminder. The model must surface the other reading in `assumptions`, and the UI shows it.
- **Cost to change later:** ~10 min (golden eval expectation + few-shot example).
- **Question for owner:** A or B?
- **Owner response:** A
- **Status:** ACCEPTED

### ✅ N-PLAN-012 · Allowed personalization variables · impact: low
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
- **Owner response:** Not sure. Go with B for now.
- **Status:** ACCEPTED

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

### ✅ N-PLAN-014 · API key handling and spend guard · impact: medium
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
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

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

### ✅ N-ORC-002 · Transport for streamed content generation · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-FLOW-2
- **Context:** `EventSource` is GET-only, but generation is started by a POST (KICKOFF §5.5).
- **Options:** A) The POST itself answers with `text/event-stream`; the browser reads it with `fetch` and a small SSE parser. B) POST starts a job, then a GET `EventSource` subscribes to it.
- **Decision taken:** A. One request, no server-side job store, and a client disconnect cancels the request context, which cancels the upstream LLM call. The campaign dashboard still uses a GET `EventSource`, because it benefits from native reconnect.
- **Cost to change later:** ~1.5 h (server job buffer + client switch).
- **Question for owner:** OK?
- **Owner response:** OK
- **Status:** ACCEPTED

### ✅ N-ORC-003 · Value typing in the segment wire schema · impact: medium
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-AI-1, R-AUD-1
- **Context:** Structured outputs support `anyOf`, `enum`, `null` and `format: date`, but not `minimum`/`maximum` or length limits (claude-api skill, checked 2026-11-08).
- **Options:** A) One condition shape with `value` as a loose union. B) `anyOf` of typed condition variants (number compare, number between, enum scalar, enum list, bool, date relative, date absolute, date null), each restricting `field` and `op` to compatible enums.
- **Decision taken:** B. Constrained decoding then cannot produce an operator that does not fit the field's type. Ranges, list sizes and per-field enum values are still enforced by the validator. The schema is generated from `internal/catalog`; a test fails if the committed `docs/contracts/segment.wire.schema.json` drifts or contains an unsupported keyword.
- **Cost to change later:** ~1 h (generator + fixtures).
- **Question for owner:** OK?
- **Owner response:** APPROVED. KEEP B.

  Use separate typed condition variants for each supported field/value type. This approach provides stronger guarantees during structured output generation by preventing incompatible field and operator combinations.

  Keep backend validation as an additional layer for constraints that cannot be fully enforced by the schema, including numeric ranges, list sizes, string length limits, and field-specific enum values.

  The schema should continue to be generated from internal/catalog, with automated tests ensuring that the committed docs/contracts/segment.wire.schema.json remains synchronized and contains only supported JSON Schema keywords.
- **Status:** ACCEPTED

### ✅ N-ORC-004 · Contradictions, duplicates and empty audiences · impact: medium
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-AUD-1, R-FLOW-1, R-FLOW-3
- **Context:** KICKOFF §6.3 leaves "warning or error" for contradictions open.
- **Options:** A) All warnings. B) Provable contradictions are errors, duplicates are warnings.
- **Decision taken:** B. A contradiction is detected only where it is provable without data: two single-condition groups on the same field whose ranges cannot overlap (e.g. `age > 60` and `age < 20`), or `is_null` together with a comparison on the same field. These are errors and go through the repair loop. Exact duplicate conditions are warnings. An audience of 0 reachable recipients is not a validation error, but approval is refused with a clear message.
- **Cost to change later:** ~30 min (validator rule + tests).
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-ORC-005 · Long first names in worst-case personalization · impact: medium
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-CNT-1, R-CNT-3
- **Context:** The SMS budget is computed with the longest value of each variable. One 40-rune name in the audience would shrink the budget for everyone (KICKOFF §6.5 asks for this decision).
- **Options:** A) No cap. B) Truncate names to 20 runes. C) Names longer than 20 runes render the per-language fallback ("Değerli müşterimiz" / "Valued customer").
- **Decision taken:** C. No name is cut in the middle. The same rule is used for validation and at send time, so the worst case is `max(min(longest, 20), len(fallback))`. The same cap applies to `{{city}}` (the longest seeded city is well under 20).
- **Cost to change later:** ~20 min (content constants + test vectors).
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-ORC-006 · Source of live dashboard updates · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-FLOW-5, R-OBS-1
- **Context:** The worker updates message rows; the API has to push progress to browsers.
- **Options:** A) One hub per active campaign polls a single aggregate query every 500 ms and fans out to all subscribers. B) Postgres `LISTEN/NOTIFY` from the worker.
- **Decision taken:** A. One query per campaign regardless of subscriber count, it survives database restarts without re-subscribing, and every event is a full snapshot, so reconnects are trivial. B is named in DECISIONS.md as the next step at higher scale.
- **Cost to change later:** ~1 h.
- **Question for owner:** OK?
- **Owner response:** ACCEPT A (chat, 2026-11-10), after polling vs LISTEN/NOTIFY was explained.
- **Status:** ACCEPTED

### ✅ N-ORC-007 · Seed timestamps are relative to seed time · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-RES-2
- **Context:** Briefs use relative time ("last 30 days"). Fixed absolute dates would make audiences shrink as days pass.
- **Options:** A) Fixed absolute dates. B) Deterministic offsets from the moment the seed runs.
- **Decision taken:** B. The generator is deterministic in everything except the anchor time; the golden eval compares rules on the same database, so results stay comparable.
- **Cost to change later:** ~10 min.
- **Question for owner:** OK?
- **Owner response:** ACCEPT B (chat, 2026-11-10), after the trade-offs were explained. README should say that audience counts drift on a long-lived database (320 on a fresh seed, 312 two days later).
- **Status:** ACCEPTED

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

### ✅ N-ORC-009 · Migration runner, router and SPA embedding · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-TECH-1, R-DEL-2
- **Context:** KICKOFF leaves the migration approach and libraries open.
- **Options:** A) Small in-repo pieces: embedded SQL runner (advisory lock, one transaction per file, `schema_migrations`), stdlib `net/http` routing, SPA embedded behind the `spa` build tag. B) goose + chi.
- **Decision taken:** A. Each piece is under 100 lines, needs no extra dependency, and is easy to explain in the review call.
- **Cost to change later:** ~1 h.
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-ORC-010 · How `make test` isolates tests · impact: low
- **Date / agent / session:** 2026-11-08 02:45 · orchestrator · 01-orchestrator
- **Requirement(s):** R-AI-2, R-DEL-3
- **Context:** `make test` must need only Docker, no key and no internet.
- **Options:** A) Go and web tests run in containers whose dependencies are baked in at build time, on an `internal` network with a throwaway Postgres; the mock provider runs in-process via `httptest`. B) Tests on the host.
- **Decision taken:** A for `make test`, under a separate compose project so it never touches the dev stack. `make test-host` runs the same Go tests on the host against the shared Postgres (port 55432) for fast builder loops. The mock provider's server code lives in `internal/mockprovider` so tests can start it in-process; `cmd/mockprovider` is a thin main.
- **Cost to change later:** low.
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-ORC-011 · W1 verification split and recovery when a builder hits the usage limit · impact: medium
- **Date / agent / session:** 2026-11-08 10:10 · orchestrator · 01-orchestrator
- **Requirement(s):** R-DEL-6 (AI process), all P0 IDs indirectly
- **Context:** The owner approved W1 (three background builders in worktrees: llm/opus, delivery/sonnet, web/sonnet). Pro usage limits can stop a builder mid-task.
- **Options:** A) One verifier per area at CP1 plus spec-auditor at CP1. B) Targeted verifiers at CP1, spec-auditor once everything is integrated.
- **Decision taken:** B, per the owner. CP1: `llm-red-team` on the llm branch, `reliability-reviewer` on the delivery branch; web is verified by the orchestrator's diff review + `npm test`/`typecheck`/`build` at merge. `spec-auditor` runs at CP3 (Monday) after full integration. If a builder stops on the usage limit, the orchestrator resumes the same agent with `SendMessage` after the reset; if that fails, it starts a fresh builder with the task file plus `git log` of that branch so it continues from the last commit. Cost reports quote `ccusage` measurements, not estimates.
- **Cost to change later:** low.
- **Question for owner:** —
- **Owner response:** ACCEPT (chat, 2026-11-08 10:09)
- **Status:** ACCEPTED

### ✅ N-WEB-001 · Catalog response shape is not in the contract · impact: low
- **Date / agent / session:** 2026-11-08 10:20 · web · builder-web (W1 subagent)
- **Requirement(s):** R-FLOW-1
- **Context:** `GET /api/catalog` is listed in api.md without a shape. P0 UI does not need it (audience uses server rendering; limits come from the validation report).
- **Options:** A) Opaque object, unused. B) Define the shape so P1 manual rule editing can use it.
- **Decision taken:** A for now. Orchestrator note: the shape is `catalog.Doc()` in `internal/catalog`; it will be written into api.md before P1 rule editing starts.
- **Cost to change later:** ~1 h (typed catalog + rule form).
- **Question for owner:** —
- **Owner response:** CHANGE → B (chat, 2026-11-10): define the catalog shape in api.md and build manual segment-rule editing in the UI (field / operator / value from the catalog, server validation, audience recomputed).
- **Implemented (2026-11-10, phase 2 F2):** catalog shape in api.md §9.1; `POST /api/segment/preview` (same validator, never saves); the stored segment keeps `original_rule` and `edited_at`; catalog-driven editor in the audience step with debounced preview and an "Edited" badge. Tests: `TestManualRuleEdit_KeepsClaudesRuleForAudit_RFLOW1`, `TestPreviewSegment_SameValidatorNeverSaves_RFLOW1`, `TestSegmentPreview_HTTP_RFLOW1`, web `RuleEditor.test.ts`, e2e rule-editor test, acceptance A9. DECISIONS D-19.
- **Status:** RESOLVED

### ✅ N-WEB-002 · Does saving content move the campaign to content_ready? · impact: low
- **Date / agent / session:** 2026-11-08 10:20 · web · builder-web (W1 subagent)
- **Requirement(s):** R-FLOW-3
- **Context:** api.md did not say whether `PUT …/content` sets `content_ready`.
- **Options:** A) Re-fetch the campaign after save/generate and gate on its status. B) Assume.
- **Decision taken:** A. Orchestrator confirms: every saved version (PUT or generate `result`) sets `content_ready` (`campaign.saveVersion`); approve re-validates anyway.
- **Cost to change later:** ~5 min.
- **Question for owner:** —
- **Owner response:** — (resolved by the orchestrator against the implementation)
- **Status:** RESOLVED

### ✅ N-WEB-003 · Proxy for mock-provider fault controls (P1) · impact: low
- **Date / agent / session:** 2026-11-08 10:20 · web · builder-web (W1 subagent)
- **Requirement(s):** R-OBS-1 (P1 UI)
- **Context:** The P1 fault-control panel needs an API proxy to the provider's `/admin/config`.
- **Options:** A) `GET/PUT /api/system/provider-config` forwarding to the mock provider. B) Skip the panel.
- **Decision taken:** Not built; deferred to after P0 integration (CP2). Orchestrator leans to A, since it makes the S-scenarios demoable from the UI.
- **Cost to change later:** ~1.5 h UI + ~30 min API.
- **Question for owner:** —
- **Owner response:**
- **Implemented (2026-11-09):** option A as `GET/PUT /api/system/provider-config` (commit "mock-provider fault controls"); System page panel with Calm / Default / Chaos presets. Test: `TestProviderConfig_ProxyReadsAndUpdates_RRES3`.
- **Status:** RESOLVED

### ✅ N-WEB-004 · Dashboard first paint from GET progress, then SSE · impact: low
- **Date / agent / session:** 2026-11-08 10:20 · web · builder-web (W1 subagent)
- **Requirement(s):** R-FLOW-5
- **Context:** `GET /api/campaigns/{id}` carries a `progress` snapshot once approved; the SSE stream also starts with a full snapshot.
- **Options:** A) SSE only. B) Show GET progress first, every SSE snapshot replaces it.
- **Decision taken:** B.
- **Cost to change later:** —
- **Question for owner:** —
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-DEL-001 · 25 ms guard on the limiter window · impact: medium
- **Date / agent / session:** 2026-11-08 · delivery · builder-delivery (W1 subagent)
- **Requirement(s):** R-SND-2
- **Context:** The limiter frees the next send exactly 1 s after the send N places back, but the provider measures arrivals; jitter makes about half of such pairs arrive < 1 s apart, so the provider would count 101.
- **Options:** A) window = 1 s + guard (default 25 ms ≈ 97.6/s). B) Only `RATE_LIMIT_SAFETY_MARGIN` (costs 1/s per unit). C) No guard.
- **Decision taken:** A, env `RATE_LIMIT_WINDOW_GUARD_MS` (read in `cmd/worker`; orchestrator added it to `.env.example`). S1: provider-measured max = 100 in any 1 s window, 0 provider-limiter rejections. **Orchestrator update:** on the real compose stack the provider measured 101 requests in a 1 s window on sms (1 provider-limiter rejection) at 100/s + 25 ms guard, so the default `RATE_LIMIT_SAFETY_MARGIN` was set to 5 (95/s) as a stopgap. **Superseded by N-ORC-012:** the limiter now paces real departures; margin is back to 0. Re-measured on compose at 95/s (all 5000 users, 7542 messages, default faults): provider max 95 requests in any 1 s window per channel, 0 provider-limiter rejections, 0 duplicate deliveries, completed in 65 s.
- **Cost to change later:** low (one env var).
- **Question for owner:** OK to trade ~2.4 % throughput for never exceeding 100/s at the provider?
- **Owner response:** ACCEPT (chat, 2026-11-10). Note: the safety-margin stopgap described above was reverted to 0 by N-ORC-012 (commit 8e0f514); today's default is margin 0 plus the 25 ms window guard.
- **Status:** ACCEPTED

### ✅ N-DEL-002 · A burst of in-flight 429s is one overload event · impact: medium
- **Date / agent / session:** 2026-11-08 · delivery · builder-delivery (W1 subagent)
- **Requirement(s):** R-SND-4
- **Context:** With ~30 requests in flight, one overload returns many 429s; restarting the Retry-After pause for each (literal reading of KICKOFF §7.3) kept a channel paused for many seconds (S2 fell to ~17 msg/s).
- **Options:** A) Every 429 restarts the pause. B) 429s for requests sent before the current pause began do not extend it.
- **Decision taken:** B. The pause is never shortened; throttles still do not count as attempts; cap of 20 throttles per message unchanged. Test `TestThrottle_BurstOfInFlight429sIsOneEvent_RSND4`. Update after CP1: a late in-flight 429 with a longer Retry-After extends the pause to pauseStart + its Retry-After (still never restarts it).
- **Cost to change later:** ~15 min.
- **Question for owner:** Accept B as the reading of "pause the whole channel until Retry-After"?
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-DEL-003 · What `messages.attempts` counts · impact: low
- **Date / agent / session:** 2026-11-08 · delivery · builder-delivery (W1 subagent)
- **Requirement(s):** R-SND-3, R-SND-4
- **Context:** KICKOFF §7.3: throttles and config errors do not count toward max attempts, but the claim increments `attempts`.
- **Options:** A) Claim increments; throttled/config/shutdown releases decrement. B) Separate counter.
- **Decision taken:** A. Throttle-cap failures end with `attempts` 0 and `last_error=too_many_throttles`; a lease expiring after 5 attempts → `failed`, `lease_expired` (no poison loop); late results from a stale lease holder are dropped by the guard `status='sending' AND attempts=<claimed>`.
- **Cost to change later:** low.
- **Question for owner:** —
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-DEL-004 · Wiring outside delivery paths · impact: medium
- **Date / agent / session:** 2026-11-08 · delivery · builder-delivery (W1 subagent)
- **Requirement(s):** R-DEL-2, R-SND-2, R-OBS-3
- **Context:** `make stress` was a stub; worker settings were missing from `.env.example`.
- **Options:** —
- **Decision taken:** Orchestrator wired `make stress` (`go test -tags stress -run TestStress ./cmd/worker/` against Postgres on 55432, mock LLM forced) and `RATE_LIMIT_WINDOW_GUARD_MS` in `.env.example`. Compose already passes the worker env through `.env`; defaults live in code. `make test` passes with `cmd/worker`'s `go build` test (Go toolchain is in the test image).
- **Cost to change later:** —
- **Question for owner:** —
- **Owner response:** — (resolved by the orchestrator)
- **Status:** RESOLVED

### ✅ N-DEL-005 · Private email HTML helper until `content.RenderEmailHTML` lands · impact: low
- **Date / agent / session:** 2026-11-08 · delivery · builder-delivery (W1 subagent)
- **Requirement(s):** R-CNT-3
- **Context:** The llm builder owns `content.RenderEmailHTML`; it did not exist yet.
- **Options:** A) Private helper in `delivery/render.go` (render, then escape, paragraphs, `<br>`). B) Wait.
- **Decision taken:** A. Orchestrator swaps it for `content.RenderEmailHTML` when the llm branch is merged; `TestRenderMessage_RCNT3` pins the output.
- **Cost to change later:** one function.
- **Question for owner:** —
- **Owner response:** Resolved (chat, 2026-11-10): the private helper is gone; `internal/delivery/render.go` calls `content.RenderEmailHTML`.
- **Status:** RESOLVED

### ✅ N-DEL-006 · Heartbeat rows are deleted on clean shutdown · impact: low
- **Date / agent / session:** 2026-11-08 · delivery · builder-delivery (W1 subagent)
- **Requirement(s):** R-OBS-3
- **Context:** No ghost workers in the status panel.
- **Options:** A) Delete on SIGTERM. B) Keep stale rows.
- **Decision taken:** A. `GET /api/system/status` already reports `down` with no rows and `stale` with old ones.
- **Cost to change later:** —
- **Question for owner:** —
- **Owner response:** — (resolved by the orchestrator)
- **Status:** RESOLVED

### ✅ N-DEL-007 · Long provider outages exhaust the retry budget · impact: medium
- **Date / agent / session:** 2026-11-08 · delivery · builder-delivery (W1 subagent)
- **Requirement(s):** R-SND-4, R-RES-3
- **Context:** Connection errors are "ambiguous" tries that count toward max 5 (KICKOFF §7.3). S7's 10 s outage caused 0 unexpected failures, but an outage of ~30 s or more ends messages as `failed`.
- **Options:** A) Keep the §7.3 table. B) A refused connection (nothing was sent) does not count and pauses the channel like 401/403, so queued messages survive any outage. C) Longer backoff.
- **Decision taken:** A for now (it is the locked table). The orchestrator leans to B because it is safe: a refused TCP connection proves nothing reached the provider.
- **Cost to change later:** B ~1 h plus tests.
- **Question for owner:** Should a refused connection stop counting as an attempt and pause the channel instead?
- **Owner response:** CHANGE → B (2026-11-09): a refused connection does not count as an attempt and pauses the channel; with a test.
- **Implemented (2026-11-09):** the HTTP client marks a send error `ErrUnreachable` when no connection was ever assigned to the request (httptrace `GotConn` never fired: refused, DNS failure, dial timeout), so not one byte can have reached the provider. Class `unreachable` pauses the channel for `UnreachablePause` (2 s) and does not count an attempt; a connection dropped after it was made stays `ambiguous` and counts. Tests: `TestHTTPClient_ClassifiesTransportFailures_RSND4`, `TestDispatcher_RetryClasses_RSND4` (8 refused tries, then `sent` with attempts = 1).
- **Status:** RESOLVED

### ✅ N-LLM-001 · Worst case when the audience is unknown, and which fallback counts · impact: low
- **Date / agent / session:** 2026-11-08 11:00 · llm · builder-llm (W1 subagent)
- **Requirement(s):** R-CNT-1, R-CNT-3
- **Context:** `content.Validate` can run without audience data, and send-time rendering may pick the campaign or the recipient locale for the fallback.
- **Options:** A) Optimistic: the locale fallback only. B) Conservative: a missing variable counts as the 20-rune cap; when a fallback applies, `WorstCaseVars` uses the longest fallback of any locale.
- **Decision taken:** B, safest for the hard 160 limit whichever locale delivery picks.
- **Cost to change later:** ~15 min.
- **Question for owner:** OK to stay conservative?
- **Owner response:** ACCEPT (chat, 2026-11-10): stay conservative.
- **Status:** ACCEPTED

### ✅ N-LLM-002 · max_tokens stop retries the same turn with a doubled limit · impact: low
- **Date / agent / session:** 2026-11-08 11:00 · llm · builder-llm (W1 subagent)
- **Requirement(s):** R-AI-1
- **Context:** KICKOFF §6.3: max_tokens = invalid plus one repair with a larger limit.
- **Options:** A) Echo the truncated output and ask for a fix. B) Re-send the same conversation with max_tokens doubled (cap 32768); counts toward the 2-repair budget, recorded as purpose `repair`.
- **Decision taken:** B; truncated JSON is useless context and B keeps the limit of 3 calls.
- **Cost to change later:** ~10 min.
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-LLM-003 · claudecode fixture reconstructed, not freshly probed · impact: medium
- **Date / agent / session:** 2026-11-08 11:40 · llm · builder-llm (W1 subagent)
- **Requirement(s):** R-AI-1, R-DEL-6
- **Context:** KICKOFF §6.9 asks for a sanitized probe fixture; a subagent running `claude -p` would spend the owner's subscription unattended.
- **Options:** A) Probe from the subagent. B) Reconstruct from the §6.9 notes, mark it as reconstructed (first line of the file), owner captures a real one with `make dev-sub`.
- **Decision taken:** B; the parser ignores unknown line types and a real capture just replaces the file.
- **Cost to change later:** ~10 min.
- **Question for owner:** Will you run one claudecode probe so the fixture can be replaced by a real capture?
- **Owner response:** CHANGE (2026-11-09): the orchestrator may run the claudecode probe and `make eval PROVIDER=claudecode` (subscription, not the paid API). Make the real capture the fixture; put the eval report in docs/eval.
- **Implemented (2026-11-09):** the orchestrator ran `make eval PROVIDER=claudecode` (17 briefs, real Sonnet through the subscription) with the CLI's stdout teed to a scratch file. The official-brief segment capture, sanitized (ids replaced, init reduced to non-personal keys, rate-limit details dropped), replaced the reconstructed fixture; the tests now assert its real values. Differences from the reconstruction: the result line says `stop_reason: tool_use` (StructuredOutput tool) with `num_turns: 2`, and there are `system/status`, `assistant`, `user` and `rate_limit_event` lines; the parser already ignored them. Eval report: `docs/eval/REPORT-claudecode-sonnet-2026-11-09.md` (16/16 valid first try, 14/14 exact audiences, p50 11.6 s, $0.35 API-equivalent).
- **Status:** RESOLVED

### ✅ N-LLM-004 · Repairs only for requested channels; body limit on raw text; allowed control characters · impact: low
- **Date / agent / session:** 2026-11-08 11:10 · llm · builder-llm (W1 subagent)
- **Requirement(s):** R-CNT-1, R-CNT-2
- **Context:** KICKOFF §6.5 leaves open which content errors trigger a repair, how the body is measured, which control characters are allowed.
- **Options:** A) Every error triggers a repair. B) Only errors on fields of requested channels trigger a repair (`valid_for` still reports both); body 1..5000 on raw runes; `\n` allowed in SMS, `\n`/`\t` in the body, none in the subject.
- **Decision taken:** B; an email-only campaign should not burn repairs on SMS copy, and the contract shape stays exact.
- **Cost to change later:** ~20 min.
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-LLM-005 · A failed llm_calls insert does not fail the generation · impact: low
- **Date / agent / session:** 2026-11-08 11:10 · llm · builder-llm (W1 subagent)
- **Requirement(s):** R-AI-1, R-OBS-2
- **Context:** One row per call is required; the database can fail independently of the model call.
- **Options:** A) Fail the request. B) Log an error with campaign_id and purpose and continue; the insert uses `context.WithoutCancel` with a 5 s timeout so client disconnects are still recorded.
- **Decision taken:** B; a paid generation is not thrown away over an accounting row.
- **Cost to change later:** ~5 min.
- **Question for owner:** OK?
- **Owner response:** CHANGE (chat, 2026-11-10): keep B (the generation is not failed), but the user must be told. When the llm_calls row cannot be written, the response carries a flag and the UI shows a non-blocking notice such as "The content is ready, but its cost could not be recorded."
- **Implemented (2026-11-10, phase 2 F6):** `llm.cost_recorded` on the segment response and the content/refine `result` event; non-blocking notice in the UI. Tests: `TestCostRecordedFlag_NLLM005`, `TestCostRecorded_NLLM005`, web `panels.test.ts`. DECISIONS D-20.
- **Status:** RESOLVED

### ✅ N-ORC-012 · How the per-channel rate bound is guaranteed · impact: high
- **Date / agent / session:** 2026-11-08 · orchestrator · 01-orchestrator (after CP1 reliability review)
- **Requirement(s):** R-SND-2 (I3)
- **Context:** The reviewer showed the limiter pacing slot reservations; DNS/dial/timer delays and replica takeover let the provider see up to 141 requests in 1 s. A count margin is not a bound.
- **Options:** A) Two-phase limiter: slot s may start only W = 1 s + 25 ms after the recorded *departure* (`httptrace.WroteRequest`) of slot s−N; holds if one-way network + provider decode ≤ 25 ms (sub-ms on a Docker bridge); ~97.6/s. B) Strict: commit at response receipt; holds with no network assumption; ~84/s at default latency, ~61/s at S2 latency. C) Keep the margin.
- **Decision taken:** A, plus: per-lock-term context so queued sends are released when the lock is lost; claim/reclaim on the lock connection; wait W + lock-loss detection bound (~4 s) after acquiring the lock before sending; disable net/http's hidden replay of POSTs (`GetBody = nil`); provider stamps arrivals before decode and never encodes stats under its mutex. Margin back to 0 after the fix. B stays a one-line switch if the owner prefers a proof without a network assumption. **Implemented (merge a21d3ab) and measured on compose at 100/s, margin 0:** all 5000 users, 7542 messages with default faults, provider max 100 requests in any 1 s window per channel, 0 provider-limiter rejections, 0 duplicate deliveries, approve 0.16 s, completed in 72 s.
- **Cost to change later:** A→B: one call-site change.
- **Question for owner:** A (≈97.6/s, bounded under a stated sub-25 ms network assumption) or B (≈84/s, no assumption)?
- **Owner response:** ACCEPT A (2026-11-09). Explain the assumption and the one-line switch to B in DECISIONS.md.
- **Status:** ACCEPTED

### ✅ N-DEL-008 · Quiet time after winning the channel lock · impact: medium
- **Date / agent / session:** 2026-11-08 · delivery · builder-delivery fix round (subagent)
- **Requirement(s):** R-SND-2 (I3)
- **Context:** A new lock holder cannot know what the previous holder sent in the last second, so starting at once let the provider see 129–182 requests in 1 s on takeover.
- **Options:** A) Wait W + lock-loss detection bound (1.025 s + 1 s check + 2 s statement timeout ≈ 4.025 s) after acquiring the lock, also at first start. B) Share limiter state through Postgres. C) No wait.
- **Decision taken:** A. Statement timeout on the lock connection is 2 s (not 5 s) to keep the bound short; a claim that times out counts as lock lost. Cost: a worker sends nothing for ~4 s after start or takeover; S1 averages ~89/s instead of 96/s.
- **Cost to change later:** constants `lockCheckEvery`, `lockOpTimeout`, `Dispatcher.TakeoverWait`.
- **Question for owner:** —
- **Owner response:** ACCEPT (chat, 2026-11-10), after the trade-off was explained: ~4 s without sends on start or takeover, in exchange for never exceeding 100/s at the provider.
- **Status:** ACCEPTED

### ✅ N-DEL-009 · Stress invariant I5 tolerates "delivered but failed" after unanswered tries · impact: medium
- **Date / agent / session:** 2026-11-08 · delivery · builder-delivery fix round (subagent)
- **Requirement(s):** R-SND-3 (I5)
- **Context:** Under chaos faults a provider can record a message whose every answer was lost (hang after record, then retries exhausted), so DB says `failed` while the provider counted it. S2 hit this in 2 of 7 runs.
- **Options:** A) Allow a provider-vs-DB gap up to the number of `failed` messages that had an ambiguous attempt; still fail if the DB has more `sent` than the provider. B) Fix the root cause: an expired or exhausted-but-ambiguous message gets one final idempotent replay (needs a separate lease-expiry counter, schema change).
- **Decision taken:** A for now (the stress report prints when the allowance is used). B is the real fix; scheduled after CP2 if time allows.
- **Cost to change later:** B ~1–2 h (migration + dispatcher + test).
- **Question for owner:** —
- **Owner response:** CHANGE → B if time allows (2026-11-09).
- **Implemented (2026-11-09):** migration `20261109010000_confirm_replays.sql` adds `unanswered_tries` and `confirm_replays`. A try without an answer (ambiguous, or a lease that expired mid-send) increments `unanswered_tries`. When the 5 counted attempts are used up and some try went unanswered, the message gets up to `MaxConfirmReplays` = 3 more tries with the same Idempotency-Key (backoff as before); the provider's stored answer confirms a recorded delivery. Only after those does it fail, with `last_error` saying the delivery is unconfirmed. Messages with only answered failures still fail at 5. Lease expiry follows the same rule (poison messages stay bounded at 8 claims). Tests: `TestDispatcher_ConfirmationReplay_RSND3`, `TestLeaseExpiry_ConfirmationReplays_RSND3`; stress I6 allows 5 + 3 tries. The I5 allowance stays as a bound for the residual case (all replays fault too).
- **Status:** RESOLVED

### ✅ N-ORC-013 · Concurrent approves enqueue one at a time · impact: medium
- **Date / agent / session:** 2026-11-09 · orchestrator · 01-orchestrator (stress S3)
- **Requirement(s):** R-SND-1, R-FLOW-3
- **Context:** Stress S3 approved 10 all-user campaigns at once. Each bulk `INSERT … SELECT` (~3.8k rows) took 14–55 s in parallel on the compose Postgres (CPU-bound; BufferContent, LockManager, extend and spinlock waits), reproduced in plain SQL without the app and without the users foreign key. All ten hit the 60 s request timeout and rolled back: nothing was enqueued. One at a time, all ten took 3.5 s.
- **Options:** A) Transaction-scoped advisory lock right before the enqueue INSERTs, so approves of different campaigns enqueue sequentially. B) Batch the INSERT in chunks. C) Leave it (rare in a single-marketer demo).
- **Decision taken:** A. Same transaction, same guarantees; approve of the same campaign still fails fast with `NOWAIT`. Measured after the fix: all 10 succeed, 75 420 messages, slowest approve 7.1 s; a single approve is unchanged.
- **Cost to change later:** one statement in `internal/campaign/approve.go`.
- **Question for owner:** —
- **Owner response:** ACCEPT (chat, 2026-11-10).
- **Status:** ACCEPTED

### ✅ N-ORC-014 · A/B split formula · impact: medium
- **Date / agent / session:** 2026-11-10 23:15 · orchestrator + builder-delivery · 01-orchestrator (phase 2)
- **Requirement(s):** B-2
- **Context:** The owner asked for a deterministic hash(campaign_id, user_id) mod 2.
- **Options:** A) `md5` of `campaign_id:user_id`, first byte mod 2, in the enqueue SQL; B) Postgres `hashtextextended` (internal, not specified across versions); C) assign in Go after enqueue (a second pass).
- **Decision taken:** A. Built in, set-based in the same statement, recomputable outside Postgres (the test recomputes it with `crypto/md5`), uniform (B share 45–55 % on the seed; acceptance A8: 49.8 %). Not a security use of md5.
- **Cost to change later:** one SQL expression; stored per message, so old campaigns keep their split.
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-11).
- **Status:** ACCEPTED

### ✅ N-ORC-015 · Cancel: in-flight bound without a pre-send status check · impact: medium
- **Date / agent / session:** 2026-11-10 23:30 · builder-delivery · phase 2 F4
- **Requirement(s):** F4, R-SND-1
- **Context:** Messages already claimed when a cancel commits can still be sent.
- **Options:** A) let them finish; bound = `MaxInFlight` per channel (32 by default), sweep stragglers to `cancelled`; B) look up the campaign status before every send (bound ~0, one DB read per send on the rate-limited path).
- **Decision taken:** A. The bound is tight and tested (`TestCancel_InFlightBoundIsTight_F4`, race test mid-batch); acceptance A11 measured 0 deliveries after the cancel returned. B is a one-line addition before `Limiter.Acquire`. DECISIONS D-17.
- **Cost to change later:** ~15 min.
- **Question for owner:** Is ≤ 32 per channel after a cancel acceptable?
- **Owner response:** ACCEPT (chat, 2026-11-11): at most MaxInFlight (32) per channel after a cancel is acceptable.
- **Status:** ACCEPTED

### ✅ N-ORC-016 · Scheduled send: audience frozen at approve, consent at send time, 50 s minimum · impact: medium
- **Date / agent / session:** 2026-11-10 23:15 · orchestrator + builder-delivery · phase 2 F5
- **Requirement(s):** B-1, R-AUD-2
- **Context:** The owner asked to decide and log whether the audience is frozen at approve and whether consent is re-checked at send time.
- **Options:** A) enqueue at approve (frozen audience), claim re-checks consent; B) re-run the rule at send time.
- **Decision taken:** A. The marketer approves the audience they saw, and the claim's consent check (R-AUD-2) already covers withdrawals while scheduled (`TestScheduler_StartsOnlyWhenDue_B1`). The minimum is now + 50 s (1 minute minus 10 s for clock skew and request latency), checked on the database clock; the UI offers now + 1 min and defaults to now + 10 min. DECISIONS D-18.
- **Cost to change later:** B ~1 h (enqueue moves into the scheduler).
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-11).
- **Status:** ACCEPTED

### ✅ N-LLM-006 · Content v2 details: labels, token limit, refine status codes · impact: low
- **Date / agent / session:** 2026-11-10 23:40 · builder-llm · phase 2 F1/F3
- **Requirement(s):** B-2, F3
- **Context:** Details the contract left open.
- **Options:** —
- **Decision taken:** Labels are 1–40 code points from the model; a hand-saved label may be empty but not longer than 40 (422 on a bad label). Content `max_tokens` 8192 → 12288 because one answer holds two variants. A bad refine instruction is 422 `validation_failed` (like every other validation error), a malformed body 400. The other variant is copied on refine with its source and instruction, and its report is recomputed for the current audience. `LLM_CONTENT_TIMEOUT` stays 60 s; the owner may raise it for the live run (two variants take longer). DECISIONS D-20.
- **Cost to change later:** low.
- **Question for owner:** —
- **Owner response:** ACCEPT (chat, 2026-11-11).
- **Status:** ACCEPTED

### ✅ N-WEB-005 · Approve dialog defaults · impact: low
- **Date / agent / session:** 2026-11-10 23:40 · builder-web · phase 2
- **Requirement(s):** B-1, B-2, R-FLOW-3
- **Context:** Defaults for the new choices.
- **Options:** —
- **Decision taken:** The send choice defaults to "Send A" (the contract default), so an A/B test is always an explicit choice; B options are disabled when B is missing or invalid for a selected channel. "Schedule" defaults to now + 10 minutes (a now + 1 min default would be too soon by the time it is submitted). Refine is blocked while the editor has unsaved edits. After an audience change the save button reads "Re-check and save".
- **Cost to change later:** minutes.
- **Question for owner:** —
- **Owner response:** ACCEPT (chat, 2026-11-11).
- **Status:** ACCEPTED

### ✅ N-ORC-017 · Final polish: cancelled metric, test rename, step bar · impact: low
- **Date / agent / session:** 2026-11-11 · polish agent · pre-submission
- **Requirement(s):** R-OBS-2, F4, B-1, R-AUD-1
- **Context:** Five small items before submission needed three small choices.
- **Options:** (1) `messages_total{status="cancelled"}` in the api (which performs the cancel) or the worker; (2) which requirement `TestErrorModel_ROBS1` covers; (3) wrap or shrink the step bar at 390 px.
- **Decision taken:** (1) The api counts it, in `campaign.Service.Cancel`, after the commit, per channel; the worker's own cancel of a retry result or expired lease is not counted (the series is not pre-created at 0 in the api). `observability.md` lists the process. (2) R-AUD-1: the test's requirement-bearing cases are the strict segment edit over HTTP; renamed `TestErrorModel_RAUD1`. KICKOFF has no error-model requirement. (3) Two rows of two at ≤ 520 px. RT-8b never skipped (control-character briefs were already a structured 4xx); it now fails instead of skipping.
- **Cost to change later:** minutes each.
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-11), after the three choices were explained.
- **Status:** ACCEPTED

### ✅ N-ORC-018 · B-3 data model: one row per locale, computed staleness, language changes · impact: medium
- **Date / agent / session:** 2026-11-11 · B-3 session (branch feat/b3-recipient-language) · bonus B-3
- **Requirement(s):** B-3, R-FLOW-3
- **Context:** The spec fixes the columns (`campaign_content.locale`, PK with locale, a hash of the primary copy on localized rows, `messages.locale`, `campaigns.language_mode`) but leaves open how "primary" is told from "localized", where staleness lives, and what happens when the campaign language changes after content exists.
- **Options:** (1) a primary row is the one with `locale = campaigns.language`; (2) a primary row is the one with `adapted_from IS NULL`. Staleness stored (a flag set by every writer) or computed on read.
- **Decision taken:** (2). The primary row's `locale` records the language at save time and is informational; `adapted_from IS NULL` is what makes it primary, so regenerating the segment in another language (which changes `campaigns.language` but leaves content alone, as before B-3) cannot orphan the primary copy. A localized row whose locale equals the campaign's current language is ignored on read. Staleness is computed on read as `adapted_from ≠ sha256 of the variant's current primary texts` (length-prefixed fields; the label is not part of it), so no writer has to know about it. Migration defaults: existing content rows get the campaign's language, else `tr`; `messages.locale` is back-filled from `users.locale` (the spec said "set at enqueue", an old message has no better source); the column defaults stay (`'tr'`, `'campaign'`) so a plain INSERT keeps working. A fresh generation (§5) starts a version without adaptations (new copy, nothing to adapt from); refine and PUT carry adaptations over (stale by hash where the primary changed); the v1 single-variant PUT body drops them, as it drops variant B. API section is §9.10 (§9.9 was already the acceptance suite).
- **Cost to change later:** low (a flag column would be a small migration; the hash stays the source of truth).
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-11), after the owner's live test of B-3.
- **Status:** ACCEPTED

### ✅ N-LLM-007 · B-3 localize: both variants, fidelity checks beyond the spec, mock adaptation · impact: medium
- **Date / agent / session:** 2026-11-11 · B-3 session · bonus B-3
- **Requirement(s):** B-3, R-AI-1
- **Context:** The spec says "same repair loop and budget as content", variables exactly the same, no new URLs, phone numbers or claims, and that the mock returns a deterministic adaptation. It does not say what a one-variant version does, how "no new claims" is checked, or what the mock adapts.
- **Options:** nullable variant b in the schema, or require both; check claims with the model or not at all.
- **Decision taken:** Localize needs a version with both variants (422 `variant_missing` otherwise; the wire schema keeps the fixed keys a and b like content_v2, so "exactly two" stays a schema property). It is not checkable by code that "no new claim" holds; the backend checks what is checkable: variables per field as sets (`variable_mismatch`), no link/email/phone the source field lacks (`new_url`, `new_email`, `new_phone`), copy not identical to the source (`not_adapted`, the model answered in the wrong language), plus the usual language and length checks under the target locale. Meaning is the marketer's to review in the EN tab and, live, acceptance A14's semantic checks (English letters only, differs from TR, valid) cover it. The primary copy reaches the model as `json.Marshal`-escaped JSON in `<source_variants>`, the brief as before; the system prompt says both are data and that an order inside the copy is part of the copy. The mock adaptation is the demo copy of the target language with the source's variables put back field by field, and never repeats source text, so a hostile source cannot be reflected. The request has no `base_version`: the base is the latest version at plan time and a save against a newer one is 409 `stale_content_version` (an edit made while the model wrote wins, like refine). `GoldenContent` was exported for the external test package. DECISIONS D-22.
- **Cost to change later:** low.
- **Question for owner:** OK?
- **Owner response:** ACCEPT (chat, 2026-11-11), with a README next-steps line for an LLM judge call that checks meaning.
- **Status:** ACCEPTED

### ✅ N-ORC-019 · B-3 approve gate and delivery: which locales, fallbacks, no silent fallback · impact: high
- **Date / agent / session:** 2026-11-11 · B-3 session · bonus B-3
- **Requirement(s):** B-3, R-FLOW-3, R-DEL-4
- **Context:** "'recipient' requires, for every sending variant and channel, a valid and non-stale copy for every locale present among the recipients" can be read per channel or over all recipients. The spec also says "fallback values per message.locale (already the case)", but the worker used the campaign language (`COALESCE(c.language, u.locale)`) for them, so the user's locale was never used.
- **Options:** locales over all recipients vs per channel's consented recipients; fallback by campaign language vs message locale; a message without its copy falls back to the primary or fails.
- **Decision taken:** Per channel: the locales among the *consented recipients of that channel* are the ones that need a copy valid *for that channel* (an English reader with no SMS consent does not block an SMS-valid adaptation). Details are one `{path, code, message}` per problem (`missing` | `stale` | `invalid`), deduplicated across channels. A campaign whose recipients all read the campaign language needs no adaptation. In `recipient` mode the worker renders the copy and the fallback of `message.locale`; in `campaign` mode nothing changes (campaign language for both), because "campaign behaves exactly as today" outranks the "already the case" remark. A message whose locale has no copy fails permanently as `content_missing`; it never falls back to the other language (the approve gate makes this unreachable, the failure is the safety net). Content is not re-validated by the worker. `messages.locale` is set in both modes. The same key replays the stored answer whatever `language_mode` the replay says (idempotency unchanged). `language_mode` is validated before the transaction (400); `missing_localization` is checked after `content_invalid`.
- **Cost to change later:** medium (the gate is one function; the worker query is one CASE).
- **Question for owner:** OK? (especially the per-channel reading)
- **Owner response:** ACCEPT (chat, 2026-11-11), after the four rules were explained with examples.
- **Status:** ACCEPTED

### ✅ N-WEB-006 · B-3 UI details · impact: low
- **Date / agent / session:** 2026-11-11 · B-3 session · bonus B-3
- **Requirement(s):** B-3, R-FLOW-1
- **Context:** The spec gives the shape of the UI; these were left open.
- **Options:** —
- **Decision taken:** "N recipients" is the number of people of that language in the audience (`by_locale.<l>.total`, before consent), the same figure the audience step shows; consent counts per channel are in its table. The tabs are always in the order TR | EN, the campaign's language tab is the one open at first. An unsaved edit of the primary texts marks the adaptation out of date at once (the server confirms after the save). "Re-adapt" and "Write the … version" are blocked while there are unsaved edits (the model adapts the saved version, like refine) and for a one-variant version. The language choice appears in the approve step only when somebody in the audience reads the other language; "each recipient in their language" is the default when it is approvable, a marketer's explicit choice sticks, and the option is disabled with the server-equivalent reason while it cannot be approved. The approve request sends `language_mode` only for `recipient` (`campaign` is the server default), so every earlier request body is unchanged. The dashboard table per language shows for every campaign that has messages (`messages.locale` is set in both modes).
- **Cost to change later:** minutes.
- **Question for owner:** —
- **Owner response:** ACCEPT (chat, 2026-11-11).
- **Status:** ACCEPTED

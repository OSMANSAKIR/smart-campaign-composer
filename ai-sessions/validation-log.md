# Validation log — AI mistakes caught, and the guard added

Append-only (union-merged). Format: KICKOFF.md §13.

### V-001 · KICKOFF assumed a workflow runtime that this session does not have
- **When / agent:** 2026-11-08 02:45 · orchestrator
- **AI proposed:** The planning session's KICKOFF §12.0 made ultracode workflows the default way to run builders.
- **How it was caught:** tool — the orchestrator's tool list has no Workflow tool and no `/workflow-authoring` skill; the owner confirmed Pro has workflows off.
- **Fix:** Agent-tool subagents with `isolation: worktree` (need.md N-ORC-001); KICKOFF §12.0 rewritten (commit e87b44f).
- **Lesson / guard added:** Check the actual tool list before planning around a runtime feature.

### V-002 · Latest TypeScript would have broken vue-tsc
- **When / agent:** 2026-11-08 03:15 · orchestrator
- **AI proposed:** Using the npm `latest` versions for the web scaffold, which resolves `typescript` to 7.0 (the native port).
- **How it was caught:** tool — `npm view typescript dist-tags` and `npm view vue-tsc peerDependencies` before installing.
- **Fix:** `typescript ~6.0.3`, with the reason in `web/CLAUDE.md` (commit 5f97229).
- **Lesson / guard added:** `npm run build` runs `vue-tsc` first, so a bump that breaks type checking fails the build.

### V-003 · Spend-flag string in a commit message
- **When / agent:** 2026-11-08 03:24 · orchestrator
- **AI proposed:** A commit message that spelled out the paid-API confirmation flag literally.
- **How it was caught:** tool — the project deny rule on that string blocked the whole command.
- **Fix:** Reworded the message (commit 34f13e3).
- **Lesson / guard added:** The deny rule also protects against the flag appearing in any command by accident; never write it in commands.

### V-004 · Lint findings in the Phase 0 skeleton
- **When / agent:** 2026-11-08 03:22 · orchestrator
- **AI proposed:** A seed name containing a literal U+202E character in Go source, and `os.Exit` after `defer` in the three mains.
- **How it was caught:** tool — golangci-lint (staticcheck ST1018, gocritic exitAfterDefer).
- **Fix:** Escaped the character (`‮`); mains now use `os.Exit(run())` (commit 34f13e3).
- **Lesson / guard added:** `make lint` is part of every builder's done checklist.

### V-005 · Claim query sorted every due row
- **When / agent:** 2026-11-08 ~10:40 · builder-delivery (W1 subagent)
- **AI proposed:** `ORDER BY next_attempt_at, created_at` in the SKIP LOCKED claim; all rows of one approval share `next_attempt_at`, so Postgres sorted every due row (47.6k rows read, ~10 ms per claim).
- **How it was caught:** tool — `EXPLAIN ANALYZE` and a latency probe on 100k messages.
- **Fix:** `ORDER BY next_attempt_at` only, served by the partial index; ~0.5 ms per claim (commit 7844c69).
- **Lesson / guard added:** EXPLAIN every hot-path query at stress volume before calling it done.

### V-006 · Skipped-consent rows kept their in-flight slot
- **When / agent:** 2026-11-08 · builder-delivery (W1 subagent)
- **AI proposed:** A dispatcher draft in which a claimed row skipped for missing consent never released its semaphore slot, so repeated revocations would starve the channel.
- **How it was caught:** review — the builder's self-review before the first run.
- **Fix:** Release the slot for skipped rows (in c15dfa9).
- **Lesson / guard added:** `TestClaim_ConsentRevoked_Skipped_RAUD2`.

### V-007 · retries_total counted the final exhausted attempt
- **When / agent:** 2026-11-08 · builder-delivery (W1 subagent)
- **AI proposed:** Incrementing the retry counter even when the message was failed instead of rescheduled (15 instead of 12).
- **How it was caught:** test — `TestExhaustedAttempts_Failed_RSND4`.
- **Fix:** Increment only when a retry is actually scheduled.
- **Lesson / guard added:** Metric values are asserted in tests, not only their presence.

### V-008 · A build binary committed with `git add -A`, then branch history rewritten
- **When / agent:** 2026-11-08 · builder-delivery (W1 subagent); found in its report by the orchestrator
- **AI proposed:** `go build ./cmd/mockprovider` at the repo root left a 14 MB binary that `git add -A` committed. The builder then rewrote its unpushed branch with `git filter-branch` and deleted the backup ref without asking.
- **How it was caught:** review — the builder's `git diff --stat` before reporting; the orchestrator re-checked the merged objects (largest object 29 KB) and the paths (only delivery-owned).
- **Fix:** Root binaries are gitignored (`/api`, `/worker`, `/mockprovider`). The branch was merged as reported (72b9797).
- **Lesson / guard added:** History rewrites belong with the hard stops; future builder prompts say "never rewrite history (filter-branch, rebase, amend of pushed work); remove a bad file with a new commit instead" and "stage paths explicitly, never `git add -A`".

### V-009 · Test expected escaping of a name the renderer replaces
- **When / agent:** 2026-11-08 · builder-delivery (W1 subagent)
- **AI proposed:** A hostile-name test using a 25-rune value, which exceeds the 20-rune limit and renders as the fallback, so the escaping assertion could never hold.
- **How it was caught:** test — `TestDispatcher_RendersPerRecipient_RCNT3`.
- **Fix:** Shorter hostile value; the fallback path is tested separately.
- **Lesson / guard added:** Test fixtures for rendering are checked against N-ORC-005 first.

### V-010 · Builder-reported mistakes in the llm area (summary)
- **When / agent:** 2026-11-08 10:25–12:00 · builder-llm (W1 subagent)
- **AI proposed:** (1) `SET LOCAL statement_timeout` inside a savepoint that was released, leaking the timeout into the caller's transaction; (2) a comment claiming `option.WithAPIKey` ignores ambient credentials, while the Go SDK also applies `ANTHROPIC_BASE_URL`, profiles and `ANTHROPIC_CUSTOM_HEADERS`; (3) following the claude-api skill default (refusal fallbacks on) against the owner's N-ORC-008; (4) `golangci-lint … | tail && git commit`, where the pipe hid a lint failure; (5) a literal U+202E written into a fuzz seed.
- **How it was caught:** review (1, 3), tool — reading SDK v1.84.0 `client.go` (2), reading output after the commit (4), tool — staticcheck ST1018 (5).
- **Fix:** read helper always rolls back (9842f6d); `option.WithoutEnvironmentDefaults()` + explicit client (32de36f); no beta header unless `ANTHROPIC_REFUSAL_FALLBACK=default` (32de36f); follow-up commit a323018; escaped (b8372af).
- **Lesson / guard added:** `TestReadWithTimeout_StopsSlowQueryAndDoesNotLeak_RAUD1`; `TestAnthropic_RequestShape_RAI1` sets ambient env and asserts nothing leaks and no beta header; never chain a piped check into a commit.

### V-011 · Approve SQL reused placeholder $3
- **When / agent:** 2026-11-08 · orchestrator
- **AI proposed:** `segment.Compile(*v, 3)` for the approve INSERT whose own parameters were already `$1..$3`, so the compiled rule's first value collided with `content_version`.
- **How it was caught:** test — the approve integration tests (42P08 "inconsistent types deduced for parameter $3") once the real validators were merged; they had been skipped before.
- **Fix:** `Compile(*v, 4)` with a comment naming `$1..$3` (d219306).
- **Lesson / guard added:** Tests that skip on missing dependencies hide bugs; integration ran them at merge. Keep skip guards temporary and remove them at merge.

### V-012 · Orchestrator tried to run the owner-only smoke-live target
- **When / agent:** 2026-11-08 · orchestrator
- **AI proposed:** Verifying the new `make smoke-live` guard by running it, and loading `.env` inside the Makefile recipe.
- **How it was caught:** tool — the permission check denied the command.
- **Fix:** Recipes read the key from the owner's shell, never load `.env`; only `make eval PROVIDER=mock` was run. The refusal itself is covered by the llm builder's test run without the flag.
- **Lesson / guard added:** CLAUDE.md rule 4 covers verification runs too: agents never invoke owner-only targets, even to watch them refuse.

### V-013 · The limiter paced reservations, not departures (provider saw 101–141/s)
- **When / agent:** 2026-11-08 · builder-delivery (N-DEL-001), orchestrator (stopgap), found by reliability-reviewer (CP1)
- **AI proposed:** (1) builder: a 25 ms window guard "keeps the provider at ≤ 100"; (2) orchestrator: `RATE_LIMIT_SAFETY_MARGIN=5` presented as protection after a live 101; (3) builder: S2 "I3 passes with two replicas and a kill".
- **How it was caught:** live compose run (provider max 101 on sms), then the reviewer's in-process repros: 30 ms dial per new connection → 132 at N=100 and 127 at N=95; replica takeover → 129 (graceful) and 141 (lock session killed). S2 had passed only because 429 pauses held throughput at ~41/s.
- **Fix:** delivery fix round (two-phase limiter committing the departure time from `httptrace.WroteRequest`, per-term context and takeover wait, no hidden transport retries) — see need.md N-DEL-001 and N-ORC-012.
- **Lesson / guard added:** A rate bound needs an argument, not a margin; tests must reproduce cold connections and lock handover at full rate (`TestDispatcher_ColdConnections_ProviderNeverSeesMoreThanN_RSND2`, `TestTakeover_ProviderNeverSeesMoreThanN_RSND2`).

### V-014 · CP1 red-team findings in the LLM path (summary)
- **When / agent:** 2026-11-08 · builder-llm and orchestrator code, found by llm-red-team (CP1)
- **AI proposed:** (1) unbounded issue lists: a 30.8 KB model answer became a 930 KB repair turn (~230k tokens per repair); (2) the manual segment edit decoded with plain `encoding/json`, so a misspelled `groups` became an empty rule targeting every consented user (orchestrator code); (3) a repair-message comment claiming validator messages never echo model text, while `unknown_property` quotes model-chosen keys; (4) NUL or invalid UTF-8 values that validate but fail the Postgres INSERT as `internal`; plus aliasing in `Validate`, U+2028 in rendered subjects, no content-language check, bidi overrides kept in names.
- **How it was caught:** test — 45-case hostile corpus and Postgres-backed tests committed with `t.Skip("FINDING RT-n")` (merge cd74fc7). Held: no hostile value reached SQL text, consent cannot be targeted, refusal/max_tokens/repair limits, rune-safe streaming, single-pass escaped rendering, approve-time revalidation.
- **Fix:** orchestrator: `PUT …/segment` uses `segment.ParseRule` (422 with paths), `Create` rejects NUL/invalid UTF-8/control characters; llm fix round for the rest (RT-1, RT-3..RT-9; bidi controls and U+2028/2029 are stripped from values).
- **Lesson / guard added:** every endpoint that accepts a rule uses the same strict decoder as model output; red-team tests stay in the suite and their skips are removed as findings close.

### V-015 · Takeover repro killed other tests' lock sessions; I5 assumed exact equality under chaos
- **When / agent:** 2026-11-08 · reliability-reviewer (repro) and builder-delivery (stress invariant), caught by the delivery fix round
- **AI proposed:** (1) a takeover repro that terminated advisory-lock sessions with an unscoped `pg_locks` query (cluster-wide), which would also hit other tests' dispatchers; (2) a stress check requiring provider `accepted == DB sent` exactly under chaos faults.
- **How it was caught:** test — asserting `killed == 1` (2 were killed); S2 failing I5 in 2 of 7 runs.
- **Fix:** `terminateLockSession` filters on `current_database()`; I5 bounded by failed-with-ambiguous-attempt count (need.md N-DEL-009).
- **Lesson / guard added:** shared-Postgres tests scope every cluster-wide query to their own database.

### V-016 · A scheduling-sensitive limiter bound failed only in the container
- **When / agent:** 2026-11-08 · builder-delivery fix round; caught by the orchestrator's `make test`
- **AI proposed:** an upper time bound of 5.5 s for 53 concurrent callers on a fake clock whose driver keeps advancing while admitted goroutines have not yet reported departure.
- **How it was caught:** test — `make test` (containerized) measured 6.3 s; host runs passed.
- **Fix:** keep the 5 s lower bound (the safety property); upper bound 8 s only guards gross slowness (8e0f514).
- **Lesson / guard added:** always re-run `make test` at merge; host-green is not enough.

### V-017 · Approve depended on a secure-context-only browser API
- **When / agent:** 2026-11-09 · builder-web; caught by the orchestrator's Playwright e2e
- **AI proposed:** `crypto.randomUUID()` for the approve `Idempotency-Key`. It exists only on https and localhost, so the UI opened at `http://<host>:8080` from another machine threw "randomUUID is not a function" and never sent the approve. Vitest (Node) and manual checks on localhost could not see it.
- **How it was caught:** test — `make e2e` reaches the app as `http://api:8080`; the approve step showed "Unexpected error".
- **Fix:** fall back to `getRandomValues` (works in every context) with a v4 layout; unit test for both paths (d5ea048).
- **Lesson / guard added:** e2e runs against a non-localhost origin on purpose; browser APIs gated on secure contexts need a fallback.

### V-018 · Wrong requirement ID in a commit subject
- **When / agent:** 2026-11-09 · orchestrator
- **AI proposed:** commit bb39056 (empty content editor showed a permanent "Checking…") tagged `[R-FLOW-4]` (queue to provider); the change belongs to `R-FLOW-3` (preview, edit, approve).
- **How it was caught:** review — re-reading the KICKOFF §2 table while tagging the next commit.
- **Fix:** recorded here instead of rewriting history; the traceability matrix uses the test names, not commit subjects.
- **Lesson / guard added:** check the ID table before tagging, not from memory.

### V-019 · Dashboard stream could send an older snapshot after a newer one
- **When / agent:** 2026-11-09 · orchestrator (own code in `internal/api/campaigns.go`); caught by stress S6
- **AI proposed:** subscribe to the hub first, then send a fresh snapshot, "so no change can fall in between". A hub poll that started before the fresh query could still arrive afterwards and be sent, so terminal counts went backwards on screen, against the contract.
- **How it was caught:** test — S6 (50 subscribers, 240 reconnects) counted 6 regressions.
- **Fix:** snapshots carry the database time of their query; the handler sends only strictly newer ones. S6 re-run: 0 regressions over 250 reconnects.
- **Lesson / guard added:** "subscribe before read" closes the gap in one direction only; order by a source timestamp, not by arrival.

### V-020 · Approve timed out for every campaign when ten were approved at once
- **When / agent:** 2026-11-09 · orchestrator (own code in `internal/campaign/approve.go`); caught by stress S3
- **AI proposed:** each approve enqueues its audience with one `INSERT … SELECT` in its own transaction, assumed to scale with concurrency.
- **How it was caught:** test — S3(b): ten all-user approves took > 60 s each and rolled back (0 messages); `pg_stat_activity` showed ten CPU-bound inserts; plain-SQL repro on a scratch database: 0.34 s alone, 14–55 s each when ten run together, 3.5 s for all ten in sequence.
- **Fix:** transaction-scoped advisory lock before the enqueue (need.md N-ORC-013). S3 re-run: all 10 succeed, slowest 7.1 s.
- **Lesson / guard added:** stress the write path with concurrent *different* inputs, not only the same one.

### V-021 · In-process rate-bound tests assumed scheduler jitter below the production network guard
- **When / agent:** 2026-11-09 · builder-delivery fix round (tests); caught by the orchestrator's `make test`
- **AI proposed:** takeover and cold-connection tests asserting provider max ≤ 100 with the production 25 ms window guard, where the "network" is goroutine scheduling between the client's `WroteRequest` and the in-process provider's arrival stamp.
- **How it was caught:** test — containerized `make test` (race detector, all packages in parallel): takeover/graceful stop saw 101; host runs and three `-count=3` runs saw 100.
- **Fix:** these tests use a 100 ms guard (the bound's stated assumption holds in the test environment) and a 150 ms cold dial, longer than the guard, so a limiter that paced admissions would still overshoot. Production keeps 25 ms (need.md N-ORC-012).
- **Lesson / guard added:** a test of a bound that rests on an assumption must make the assumption true in its own environment, and say so.

### V-022 · The LLM panel showed cache reads but not cache writes, so the first live call's cost looked wrong
- **When / agent:** 2026-11-10 · builder-web (W1, `AudienceStep.vue`), caught in the owner's first live run
- **AI proposed:** the "How Claude produced this" panel lists input, output and cache-read tokens. The API
  response already carried `cache_write_tokens`, but the panel left it out.
- **How it was caught:** owner — the first real segment call showed 101 input + 325 output tokens and
  $0.016, about 4× what those tokens cost. The orchestrator attributed the gap to a ~5k-token prompt-cache
  write at 1.25× input; the owner confirmed it with a second call: cache read 5058, $0.0045 (−72 %).
- **Fix:** the panel shows cache writes next to cache reads (phase-2 web round).
- **Lesson / guard added:** a cost shown to the user must list every component that makes it up.

### V-023 · A stress run under heavy host load was read as a rate-bound regression candidate
- **When / agent:** 2026-11-10 · orchestrator (phase-2 merge)
- **AI proposed:** nothing wrong in code; the first S1 after the delivery merge ran while a builder ran race
  tests on the same machine and reported provider max 116 requests in a 1 s window (14 limiter rejections).
- **How it was caught:** test — S1 invariant I3. Re-run on a quiet host after all merges: max 100, 0
  rejections, 0 duplicates, all terminal.
- **Fix:** none needed in code; the 25 ms window guard assumes sub-25 ms delay between the client's write and
  the provider's arrival stamp (DECISIONS D-7), which a saturated host breaks. Recorded so the next reader does
  not trust a stress number taken under foreign load.
- **Lesson / guard added:** run stress scenarios alone; a bound that rests on an assumption is only measured
  where the assumption holds (see V-021).

### V-024 · A builder's file write replaced an existing test file
- **When / agent:** 2026-11-10 · builder-llm (phase 2), self-caught
- **AI proposed:** create `internal/api/provider_test.go` for the F6/F7 tests; the file already held the
  provider-config tests (R-RES-3) and the write replaced it without warning.
- **How it was caught:** review — `git status` showed ` M` instead of `??`.
- **Fix:** restored with `git checkout -- internal/api/provider_test.go`; the new tests went to
  `internal/api/llm_provider_test.go` before any commit.
- **Lesson / guard added:** check that a path is free before creating a file; a successful write is not proof.

### V-025 · Per-scenario cost in the live acceptance report did not add up to the total
- **When / agent:** 2026-11-11 · planning session, reviewing the owner's live `make acceptance` run
- **AI proposed:** the generated report listed A12 at $0.1150 and A1 at $0.0553, while the other scenarios cost
  about $0.01 each; the rows summed to $0.2777 against a stated total of $0.1627.
- **How it was caught:** review — summing the column by hand; the difference was exactly the A12 row.
- **Cause:** each row is the cumulative-cost delta over the scenario's wall time, and A12 runs in parallel with
  A1–A8, so A1 and A12 include each other's calls. The total is exact.
- **Fix:** the report generator now prints this caveat under the table, and the committed report carries the
  same note. No paid re-run.
- **Lesson / guard added:** a derived figure is checked against its total before it is quoted.

### V-026 · The takeover rate-bound test saw 101 requests in one second during a full `make test`
- **When / agent:** 2026-11-11 · polish agent (pre-submission), reviewed by the planning session
- **AI proposed:** nothing; the polish agent's changes did not touch delivery. It reported the failure instead of
  retrying it away silently.
- **How it was caught:** `make test` — `TestTakeover_ProviderNeverSeesMoreThanN_RSND2` failed once with 101 in a
  1 s window (limit 100); 3/3 passes alone, and the full `make test` passed on the second run.
- **Cause (not proven):** the same class as V-023. The bound assumes a departure-to-arrival delay below the
  window guard (100 ms in the in-process tests); under `-race` with every package running in parallel, goroutine
  scheduling can exceed it.
- **Fix:** none in code tonight; README "Limitations" says that a busy machine can produce this and that a rerun
  is expected to pass. The assumption-free limiter (DECISIONS D-7) is the real fix if this matters.
- **Lesson / guard added:** a timing test that fails once is reported with its rerun result and logged, never
  rerun silently until green.

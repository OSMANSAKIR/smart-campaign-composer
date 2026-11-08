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

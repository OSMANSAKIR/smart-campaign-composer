# Task: builder-delivery — worker, rate limiter, retries, mock provider

You are **builder-delivery** (model: sonnet). You run either as a W1 subagent in your own git worktree or as
the manual session `03-builder-delivery` (`claude -w delivery`). Either way, follow this file.

## Goal
Messages that the approve transaction puts into `messages` reach the mock provider exactly once per
(campaign, user, channel), never faster than 100/s per channel, never to a non-consented user, with the retry
policy of KICKOFF §7.3, and the campaign ends in `completed` or `completed_with_failures`.

## Read first (in this order)
1. `CLAUDE.md`, `need.md` (N-ORC-010, N-PLAN-010).
2. `KICKOFF.md` §7 (all of it), §9, §10.2 (invariants I1–I7), §10.3 rows R-SND-*, R-RES-3, R-OBS-*.
3. `docs/contracts/provider-api.md`, `docs/contracts/observability.md`, `docs/contracts/api.md` §3 and §7.
4. The skeleton: `internal/delivery`, `internal/mockprovider`, `cmd/worker`, `cmd/mockprovider`,
   `internal/store/migrations/*_init.sql`, `internal/clock`, `internal/metrics`, `internal/content` (`Render`).
5. Use context7 for pgx and the Prometheus client.

## You own (write only here)
`internal/delivery/**`, `internal/mockprovider/**`, `cmd/worker/**`, `cmd/mockprovider/**`, `docs/stress/**`,
and new migration files `internal/store/migrations/<timestamp>_delivery_*.sql` if you need indexes.
Read-only for you: everything else. Need a change elsewhere? Put it in `need_entries` (manual session: need.md
entry with agent code `DEL`).

## Work items (P0 first; stop and report before P1 if time or limits are tight)
**P0 — mock provider** (`internal/mockprovider`)
- [ ] All fault modes of provider-api.md §3 with a seeded RNG; hangs (half of them after recording the delivery).
- [ ] Own per-channel sliding-window limiter → 429 + `Retry-After`; idempotency rules of §2.
- [ ] `/stats` complete incl. `max_requests_in_any_1s_window` / `max_deliveries_in_any_1s_window`;
      `/stats/delivered`; `/admin/config` validation; `/admin/reset`.
**P0 — worker** (`internal/delivery`, `cmd/worker`)
- [ ] `SlidingWindowLimiter` (ring buffer of the last N send times, injected clock; retries go through it).
- [ ] `RetryPolicy.Classify` + `Backoff` (full jitter, base 1 s, ×2, cap 60 s; max 5 attempts; 429 → pause the
      channel for `Retry-After` (default 1 s, cap 30 s), throttles don't count as attempts, cap 20 throttles;
      401/403 → pause channel and alert; timeouts → retry with the same key).
- [ ] Provider HTTP client (`PROVIDER_TIMEOUT`, `Idempotency-Key` = message id).
- [ ] Dispatcher per channel: `pg_try_advisory_lock` on a dedicated connection (standby replicas retry);
      claim with `UPDATE … WHERE id IN (SELECT … FOR UPDATE SKIP LOCKED LIMIT n) RETURNING …` setting
      `sending`, `lease_until`, `attempts+1`; join `users` to re-check consent → `skipped_no_consent`; reclaim
      expired leases; semaphore of `MAX_IN_FLIGHT_PER_CHANNEL`; render per recipient with `content.Render` /
      the email HTML renderer from the approved `content_version`; SMS over 160 → permanent
      `content_too_long` + metric; record `delivery_attempts`; update message status.
- [ ] Reconciler: `sending` campaigns with no non-terminal messages → `completed` / `completed_with_failures`.
- [ ] Heartbeat rows with real `holds_lock` and `paused_until`; `/readyz` per observability.md; all worker
      metrics, with known label values initialized so they appear at zero.
- [ ] Graceful SIGTERM: stop claiming, drain ≤ 10 s, release leases of unsent rows. DB outage: back off, recover.
**P1** — `make stress` scenarios S1–S7 writing `docs/stress/REPORT.md` with the invariants of §10.2.

## Tests that must exist and pass (names carry requirement IDs)
`TestLimiter_NeverMoreThanNInAnyWindow_RSND2` (fake clock, bursts, boundary), `TestClassify_Table_RSND4`,
`TestBackoff_FullJitterBounds_RSND4`, `TestThrottle_PausesChannelHonorsRetryAfter_RSND4`,
`TestExhaustedAttempts_Failed_RSND4`, `TestClaim_SkipLockedNoDoubleClaim_RSND3` (integration, 2 claimers),
`TestClaim_ConsentRevoked_Skipped_RAUD2`, `TestLeaseExpiry_Reclaimed_RSND3`,
`TestDispatcher_EndToEnd_RFLOW4` (integration: insert campaign + content + messages directly, run both
dispatchers against an in-process mock provider, all messages terminal, provider `duplicate_recipient_deliveries == 0`,
max window ≤ limit), `TestAdvisoryLock_SingleActiveDispatcher_RSND2`, `TestMockProvider_FaultModes_RRES3`,
`TestMockProvider_IdempotentHang_RSND3`, `TestWorkerMetrics_Exposed_ROBS2`, `TestShutdown_ReleasesLeases`
(goleak in packages that start goroutines). No `time.Sleep` in unit tests.

## Environment
- Shared Postgres from the main checkout: `TEST_DATABASE_URL=postgres://scc:scc@localhost:55432/postgres?sslmode=disable`
  (`make test-host` sets it). Each test gets its own database. If Postgres is down, report it; do not run compose.
- Your own binaries use ports 8200–8299. Never run `docker compose up`, `make up`, `make smoke-live`, anything with
  the spend flag, or read `.env`.
- Commit small and often on your branch: `feat(delivery): sliding-window limiter [R-SND-2]`, with the
  Co-Authored-By trailer.

## Done when
All P0 boxes ticked, `go vet ./... && go test -race ./internal/delivery/... ./internal/mockprovider/... ./cmd/...`
green with output quoted, `golangci-lint run` clean for your packages.

## Result (subagent mode: end with exactly this JSON)
```json
{ "area": "delivery", "requirement_ids_done": [], "tests": [{ "cmd": "", "exit_code": 0, "summary": "" }],
  "files_changed": [], "need_entries": [], "validation_log_entries": [], "open_issues": [] }
```
`need_entries` use the KICKOFF §4.2 fields; `validation_log_entries` the §13 format (AI mistakes you caught).

## Status log (manual sessions append here)

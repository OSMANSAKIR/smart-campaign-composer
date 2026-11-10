# Phase 2 — rules for every builder (2026-11-10)

Contract: `docs/contracts/api.md` §9 (read it first, all of it), migration
`internal/store/migrations/20261110230000_phase2.sql` (already on main; do not add migrations — ask through
`need_entries`). Project rules: `CLAUDE.md`. Owner decisions: see the task files.

- **Deadline:** feature freeze 00:50 (Europe/Istanbul). Stop starting new items at **00:35**; by **00:45**
  everything is committed and you return your result. Work in the priority order of your task file; a half
  feature is worse than a missing one — commit only green, finished steps.
- Write only inside the paths your task file grants. Need something else? Return a `need_entries` item.
- Tests: table-driven, requirement IDs in names (B-1, B-2 → `_B1`, `_B2`; others use the R-IDs given).
  Integration tests use `TEST_DATABASE_URL=postgres://scc:scc@localhost:55432/postgres?sslmode=disable`
  (`internal/store/storetest` makes a random DB per test). No `time.Sleep` in unit tests; injected clocks.
- Before each commit: `go build ./... && go vet ./...`, `gofmt -l` empty, and the tests of the packages you
  touched (`go test -race -count=1 ./internal/<pkg>/...`). Web: `npm run test` and `npx vue-tsc --noEmit` in
  `web/`. Check exit codes directly (do not pipe a linter into `tail`).
- Commits: small, Conventional Commits, requirement IDs in the subject, stage files explicitly (never
  `git add -A`), never rewrite history, end every message with
  `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- **Forbidden:** reading or printing `.env` or any key; `docker compose up` (the dev stack is the
  orchestrator's; do not start, stop or rebuild it); any paid API call; any command containing the spend
  flag; push, remotes, PRs; `git reset --hard`, `git clean`, deleting branches; building binaries into the
  repo; `rm -rf` outside temp dirs.
- Do not start the claudecode adapter or `make eval PROVIDER=claudecode` (it uses the owner's subscription).
- Return, as the last message, JSON:
  `{ "status": "done"|"partial", "commits": [ "sha subject" ], "done": [ "F…: what, which test proves it" ],
     "not_done": [ … ], "tests": [ "command → result line" ], "decisions": [ "one paragraph per decision for
     DECISIONS.md" ], "need_entries": [ … ], "validation_log_entries": [ … ] }`

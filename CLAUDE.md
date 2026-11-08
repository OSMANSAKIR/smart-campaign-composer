# Smart Campaign Composer

A marketer writes a campaign brief; Claude turns it into a validated audience segment and streamed SMS/email
copy; a human edits and approves; messages go through a Postgres queue to a mock provider; a dashboard shows
delivery live. Go backend, Vue 3 + TypeScript frontend, Postgres. Only synthetic users, only a mock provider.

## Where things are decided

- `KICKOFF.md` — full brief: requirements (IDs like `R-SND-2`), locked decisions, architecture. Read the
  section you need; it is the source of truth.
- `need.md` — decisions taken without the owner and questions for the owner. Read it at session start.
- `DECISIONS.md` — curated, final decisions (ADR-lite).
- `docs/contracts/` — API, SSE events, segment DSL, wire schemas, provider API, metrics, shared test vectors.
  Code against these; change them only through the orchestrator.
- `docs/tasks/<area>.md` — builder task files.

## Commands

| Command | What it does |
|---|---|
| `make up` / `make down` / `make logs` | Compose stack with `LLM_PROVIDER=mock` forced |
| `make test` | Go vet + race tests + web unit tests in containers; no key, no internet |
| `make test-host` | Same Go tests on the host against Postgres on `localhost:55432` (fast loop) |
| `make lint` | golangci-lint + vue-tsc |
| `make schema` | Regenerate `docs/contracts/*.wire.schema.json` from `internal/catalog` |
| `make dev-sub` | Compose dependencies + API on the host with `LLM_PROVIDER=claudecode` |
| `make sanitize-check` | gitleaks + personal-term grep over the repo and `ai-sessions/` |
| `go test ./internal/<pkg>/...` | One package; integration tests need `TEST_DATABASE_URL` |

Ports: api 8080 · worker 9091 · mockprovider 8090 · Postgres 55432 (user/password/db `scc`).
`TEST_DATABASE_URL=postgres://scc:scc@localhost:55432/postgres?sslmode=disable` — each test creates and
drops its own random database (`internal/store/storetest`).

## Ownership (write only inside your area)

| Area | Paths |
|---|---|
| orchestrator | `docs/contracts/`, `internal/{config,clock,store,catalog,campaign,api,metrics,seed}`, `cmd/api`, `Dockerfile`, `docker-compose.yml`, `Makefile`, root docs, `.claude/`, `scripts/`, need.md digest, merges |
| llm | `internal/llm/**`, `internal/segment/**`, `internal/content/**`, `docs/eval/**` |
| delivery | `internal/delivery/**`, `internal/mockprovider/**`, `cmd/worker/**`, `cmd/mockprovider/**`, `docs/stress/**` |
| web | `web/**` |

Need a change outside your area? Return it as a `need_entries` item (subagents) or append a need.md entry
(sessions) and tell the orchestrator.

## Rules

1. **Precedence:** case requirements > locked decisions (KICKOFF §3) > `docs/contracts/` > accepted need.md
   entries > your judgement.
2. **Never block on the owner.** Decide (safest for hard requirements → most reversible → simplest to
   explain), log it in need.md with the KICKOFF §4.2 format, continue.
3. **Hard stops — ask in chat and wait:** secrets; pushes, remotes, PRs, publishing, paid API calls;
   `git reset --hard`, force-push, deleting branches/worktrees with unmerged work, `rm -rf` outside
   build/temp dirs; changing a locked decision or dropping a P0 requirement.
4. **Secrets:** never read, print or copy `.env` or any key. Use `make up` (forces mock), never a bare
   `docker compose up`. Only the owner runs `make smoke-live` or anything with `CONFIRM_SPEND=yes`.
5. **Evidence before "done":** a requirement is done when a test proves it and you have seen its output.
   Quote the command and result.
6. **Caught an AI mistake** (yours or another agent's)? Add it to `ai-sessions/validation-log.md`
   (subagents: return it as a `validation_log_entries` item).
7. **Facts from sources:** invoke the `claude-api` skill before writing Anthropic SDK code; use context7
   for Vue, Vite, pgx, Prometheus and other library docs. Docs beat memory.
8. **Commits:** small, Conventional Commits, requirement IDs in the subject, e.g.
   `feat(delivery): sliding-window limiter [R-SND-2]`. Commit after every completed step.
9. **Language:** files, code, comments, commits in English. Chat with the owner in Turkish.
10. **Data:** synthetic seed users only; never write the owner's personal information into repo files.

## Code conventions

- Table-driven tests; test names carry requirement IDs (`TestLimiter_NeverExceedsN_RSND2`).
- Injected clocks (`internal/clock`); no `time.Sleep` in unit tests. goleak where goroutines start.
- LLM output is never trusted: strict decode → validate → only `segment.ValidRule` compiles → `$n` params.
- Every non-obvious choice gets a one-line *why* in a comment or in DECISIONS.md.
- Logs: `log/slog` JSON with `campaign_id`, `message_id`, `attempt`; never message bodies at info level.

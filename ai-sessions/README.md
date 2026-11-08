# AI sessions — how Claude was used to build this project (R-DEL-6)

Every Claude Code session that worked on this repo, what it did, and how its output was validated.
Raw exports and harvested transcripts stay in the gitignored `_raw/`; only sanitized copies are committed
(`sessions/`, `subagents/`). Process: KICKOFF.md §13 (`/export` → `scripts/harvest-sessions.sh` →
`scripts/transcript2md.py` → `scripts/sanitize-sessions.sh` → gitleaks → human skim).

## Session index

| Session | Role | Model / effort | Date, duration | Purpose | Key outcomes | What was validated, and how |
|---|---|---|---|---|---|---|
| 00-planning | planning | Opus 5.5 | 2026-11-08 ~00:00–02:40 | Turn the case into a build brief with the owner | `KICKOFF.md`, `need.md`, project settings and plugins | Claude Code headless probe for the `claudecode` adapter (structured output, 1.7 s) |
| 01-orchestrator | orchestrator | Opus 5.5 | 2026-11-08 02:40– | Phase 0: contracts, skeleton, compose, task files; W1–W5 orchestration | see commits `docs:`/`feat:`/`build:` of 2026-11-08 | `make test` green in containers; compose healthy; Go and Vitest run the same test vectors; golangci-lint clean |

## Agent launch log

Each Agent-tool launch by the orchestrator (KICKOFF §12.0). Transcripts: `subagents/`.

| Stage | Agent | Model | Isolation | Prompt | Result |
|---|---|---|---|---|---|
| W1 | builder-llm | opus | worktree | `docs/tasks/llm.md` | running (launched 2026-11-08 10:12) |
| W1 | builder-delivery | sonnet | worktree | `docs/tasks/delivery.md` | running (launched 2026-11-08 10:12) |
| W1 | builder-web | sonnet | worktree | `docs/tasks/web.md` | done 10:23 · P0 complete, 106 Vitest tests · merged f194e8a (typecheck/test/build re-run on main) |

## How the agents were connected

```
                    owner (async, through need.md and chat sign-offs between stages)
                                          │
                                 01-orchestrator (main checkout)
              contracts · campaign/api glue · merges · need.md digest · checkpoints
        ┌──────────────────┬──────────────┴───────┬──────────────────────┐
        ▼                  ▼                      ▼                      ▼
  builder-llm        builder-delivery        builder-web        verifiers (read-only)
  opus, worktree     sonnet, worktree        sonnet, worktree   spec-auditor · reliability-reviewer
  docs/tasks/llm.md  docs/tasks/delivery.md  docs/tasks/web.md  llm-red-team (worktree, tests only)
        │                  │                      │                      │
        └──── structured JSON results (tests run, files, need_entries, validation_log_entries) ────┘
                                          │
                     orchestrator reviews diffs, runs make test, merges --no-ff
```

The backbone that lets builders work in parallel without waiting: `docs/contracts/` plus the Go interfaces,
migrations, metric and config registries written in Phase 0.

## Tooling and connections

| Kind | Items |
|---|---|
| Plugins | gopls-lsp, typescript-lsp, context7, frontend-design, playwright, pr-review-toolkit, session-report |
| MCP | context7 (library docs), playwright (browser), Claude in Chrome |
| Skills | claude-api (Anthropic SDK facts), frontend-design |
| CLIs | gh, gitleaks, golangci-lint, ccusage |
| App APIs | Anthropic Messages API via the Go SDK; Claude Code headless (`claude -p`) as the dev-only LLM |

## Usage data

`usage/` holds daily `ccusage daily --json` and `ccusage session --json` snapshots. On a subscription these are
**API-equivalent estimates**, not invoices. App-side LLM usage is recorded per call in the `llm_calls` table.

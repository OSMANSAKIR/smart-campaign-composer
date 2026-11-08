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
| W1 | builder-llm | opus | worktree | `docs/tasks/llm.md` | done ~10:48 · P0 + P1 (eval set, smoke-live test) · merged b6b93d3, integrated d219306 |
| W1 | builder-delivery | sonnet | worktree | `docs/tasks/delivery.md` | done 10:39 · P0 complete + stress S1/S2/S7 · merged 72b9797 (`go test -race ./...` and `make test` re-run on main) |
| W1 | builder-web | sonnet | worktree | `docs/tasks/web.md` | done 10:23 · P0 complete, 106 Vitest tests · merged f194e8a (typecheck/test/build re-run on main) |
| CP1 | reliability-reviewer (as general-purpose; project agent types load at session start) | opus | none, read-only | `.claude/agents/reliability-reviewer.md` + delivery diff 46a3fc1..72b9797 | stopped by the orchestrator at the usage limit before reporting; to be re-run |
| CP1 | reliability-reviewer, re-run (as general-purpose) | opus | none, read-only | `.claude/agents/reliability-reviewer.md` + delivery diff + live 101/s finding | done · 2 blockers (rate bound: cold connections, takeover), 1 major (hidden transport retry), 5 minor → fix round |
| CP1 | llm-red-team (as general-purpose) | opus | worktree | `.claude/agents/llm-red-team.md` + llm/segment/content/campaign | done · 45 cases, 2 major + 7 minor findings as skipped tests · merged cd74fc7 |
| CP1 fix | builder-delivery fix round | sonnet | worktree | reliability findings 1–8 (rate bound, takeover, transport retry, …) | done · all 8 · stress S1/S2/S7 at margin 0, provider max 100 · merged a21d3ab |
| CP1 fix | builder-llm fix round | sonnet | worktree | red-team RT-1, RT-3..RT-9 | done · all fixed, 0 red-team skips left · merged 408c8e9 |
| CP3 (early) | spec-auditor (as general-purpose) | opus | none, read-only | `.claude/agents/spec-auditor.md` + owner's evidence request (test + command + output per requirement) | done · 27 green, 2 gaps (R-DEL-1 repo = owner, R-DEL-6 exports), P1/P2 list · `docs/review/traceability.md` |

## Exports

Sanitized Markdown of every harvested transcript (`scripts/harvest-sessions.sh` → `scripts/transcript2md.py` →
`scripts/sanitize-sessions.sh`; gitleaks clean). Tool inputs and results are truncated to 2000 characters, images
and base64 are dropped. `make sanitize-check` fails if a file listed here is missing or an export is unlisted.

| File | Session / agent |
|---|---|
| `sessions/01-orchestrator.md` | 01-orchestrator (main session, all stages; includes the compacted history) |
| `subagents/02-builder-llm.md` | W1 builder-llm |
| `subagents/03-builder-delivery.md` | W1 builder-delivery |
| `subagents/04-builder-web.md` | W1 builder-web |
| `subagents/05-review-reliability-cp1-stopped.md` | CP1 reliability-reviewer, stopped at the usage limit |
| `subagents/05-review-reliability-cp1.md` | CP1 reliability-reviewer, re-run |
| `subagents/05-review-llm-red-team-cp1.md` | CP1 llm-red-team |
| `subagents/06-fix-delivery-rate-bound.md` | CP1 fix round, delivery |
| `subagents/06-fix-llm-red-team.md` | CP1 fix round, llm |
| `subagents/07-review-spec-auditor-cp3.md` | CP3 spec-auditor |

`00-planning` ran in the home directory, outside this project's transcript folder; its export is the owner's
`/export` (KICKOFF §13 step 1).

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

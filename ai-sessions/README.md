# AI sessions — how Claude was used to build this project (R-DEL-6)

Every Claude Code session that worked on this repo, what it did, and how its output was validated.
Raw exports and harvested transcripts stay in the gitignored `_raw/`; only sanitized copies are committed
(`sessions/`, `subagents/`). Process: KICKOFF.md §13 (`/export` → `scripts/harvest-sessions.sh` →
`scripts/transcript2md.py` → `scripts/sanitize-sessions.sh` → gitleaks → human skim).

## Session index

| Session | Role | Model / effort | Date, duration | Purpose | Key outcomes | What was validated, and how |
|---|---|---|---|---|---|---|
| 00-planning | planning | Opus 5.5 | 2026-11-05 – 2026-11-11 (with long breaks; build brief 2026-11-08 ~00:00–02:40) | Turn the case into a build brief with the owner; later the owner's review desk: independent checks of the orchestrator's reports, need.md answers, live-run review, submission; after submission the B-3 prompt, its review, the owner's live B-3 test and the interview notes and slides (kept outside the repo) | `KICKOFF.md`, `need.md`, project settings and plugins | Claude Code headless probe for the `claudecode` adapter (structured output, 1.7 s); B-3: re-ran lint, test, e2e, acceptance and sanitize-check, read the diff, checked the live run in the database (V-027) |
| 01-orchestrator | orchestrator | Opus 5.5 | 2026-11-08 02:40– | Phase 0: contracts, skeleton, compose, task files; W1–W5 orchestration | see commits `docs:`/`feat:`/`build:` of 2026-11-08 | `make test` green in containers; compose healthy; Go and Vitest run the same test vectors; golangci-lint clean |
| 03-b3 | B-3 builder (fresh session, small context) | Sonnet 5.5 | 2026-11-11 ~09:55–10:38 | Bonus B-3 on the branch `feat/b3-recipient-language` after submission, merged into main the same day; prompt written by the planning session | `localize_v1`, one content row per locale with computed staleness, recipient-mode approve gate, delivery in the message's locale, per-locale progress, A14, e2e spec; need.md N-ORC-018/019, N-LLM-007, N-WEB-006 (accepted) | `make test`, `make lint`, `make e2e`, `make acceptance`, `make sanitize-check` green; it caught a wrong claim in its own prompt (V-028); the planning session re-ran everything and fixed A14 (V-027) |
| 02-polish | polish agent (fresh session, small context) | Sonnet 5.5 | 2026-11-11 ~00:55–01:30 | Five small pre-submission items from the README's not-done list, prompt written by the planning session | cancelled in `messages_total`, `scheduled_at` in the campaign list, step bar at 390 px, a test renamed to its requirement, RT-8b fails instead of skipping (N-ORC-017) | `make test`, `make lint`, `make e2e`, `make acceptance` green; one load-sensitive failure reported, not hidden (V-026); the planning session re-checked the diff |

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
| Phase 2 | builder-llm | sonnet | worktree | `docs/tasks/phase2-common.md` + `phase2-llm.md` (api.md §9) | done · content_v2 two variants, refine, cost_recorded, provider rejection · merged 66606ec |
| Phase 2 | builder-delivery | sonnet | worktree | `docs/tasks/phase2-common.md` + `phase2-delivery.md` | done · md5 split + per-variant progress, cancel (bound = MaxInFlight), scheduled send · merged b802ca4 |
| Phase 2 | builder-web | sonnet | worktree | `docs/tasks/phase2-common.md` + `phase2-web.md` | done · variant cards, rule editor, refine, stop sending, schedule, notices; 214 Vitest tests · merged 644e8c5 |

## Exports

Sanitized Markdown of every harvested transcript (`scripts/harvest-sessions.sh` → `scripts/transcript2md.py` →
`scripts/sanitize-sessions.sh`; gitleaks clean). Tool inputs and results are truncated to 2000 characters, images
and base64 are dropped. `make sanitize-check` fails if a file listed here is missing or an export is unlisted.

| File | Session / agent |
|---|---|
| `sessions/00-planning.md` | 00-planning (owner's planning and review session; includes the compacted history) |
| `sessions/01-orchestrator.md` | 01-orchestrator (main session, all stages; includes the compacted history) |
| `sessions/02-polish.md` | 02-polish (pre-submission polish agent) |
| `sessions/03-b3.md` | 03-b3 (B-3 builder, after the submission deadline) |
| `subagents/02-builder-llm.md` | W1 builder-llm |
| `subagents/03-builder-delivery.md` | W1 builder-delivery |
| `subagents/04-builder-web.md` | W1 builder-web |
| `subagents/05-review-reliability-cp1-stopped.md` | CP1 reliability-reviewer, stopped at the usage limit |
| `subagents/05-review-reliability-cp1.md` | CP1 reliability-reviewer, re-run |
| `subagents/05-review-llm-red-team-cp1.md` | CP1 llm-red-team |
| `subagents/06-fix-delivery-rate-bound.md` | CP1 fix round, delivery |
| `subagents/06-fix-llm-red-team.md` | CP1 fix round, llm |
| `subagents/07-review-spec-auditor-cp3.md` | CP3 spec-auditor |
| `subagents/08-builder-llm-phase2.md` | Phase 2 builder-llm |
| `subagents/08-builder-delivery-phase2.md` | Phase 2 builder-delivery |
| `subagents/08-builder-web-phase2.md` | Phase 2 builder-web |

`00-planning` ran in the home directory, outside this project's transcript folder, so `harvest-sessions` does
not find it; it was converted from its JSONL with `scripts/transcript2md.py` and sanitized the same way. It was
re-exported on the B-3 branch on 2026-11-11 ~14:00 and ends just before that commit. The interview notes and slides
the planning session made live in the owner's claude.ai account, not in this repo.

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
Snapshot 2026-11-10 ~23:55 (`usage/ccusage-*-2026-11-10.json`): this project's orchestrator session including
all its subagents, $106.38 API-equivalent; by day across the machine, 11-08 $55.76, 11-09 $37.92, 11-10 $30.63.
Snapshot 2026-11-11 ~14:00 (`usage/ccusage-*-2026-11-11.json`), API-equivalent: orchestrator $109.15, planning
$45.15, B-3 builder $7.60, polish $1.16; by day across the machine 11-05 $2.88, 11-08 $55.76, 11-09 $37.92,
11-10 $31.93, 11-11 $34.70 (still growing when taken), $163.19 in total.

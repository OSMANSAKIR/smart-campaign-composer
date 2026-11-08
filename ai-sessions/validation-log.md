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

# Task: builder-llm — segment DSL, content rules, LLM service and adapters

You are **builder-llm** (model: opus). You run either as a W1 subagent in your own git worktree or as the
manual session `02-builder-llm` (`claude -w llm`). Either way, follow this file.

## Goal
Make the LLM part of the core flow real and trustworthy: brief → validated segment + counts, and streamed,
validated content — through one pipeline that every adapter (mock, anthropic, claudecode) shares.
"The model proposes, the backend disposes." This area is 25% of the score.

## Read first (in this order)
1. `CLAUDE.md`, `internal/llm/CLAUDE.md`, `need.md` (N-ORC-003/004/005/008, N-PLAN-005/011/012).
2. `KICKOFF.md` §6 (all of it), §10.3 rows R-FLOW-1/2, R-AUD-1/2, R-CNT-*, R-AI-*.
3. `docs/contracts/segment-dsl.md`, `docs/contracts/api.md` §2 and §5, the wire schemas, `testvectors/*.json`.
4. The skeleton: `internal/{segment,content,llm}/**`, `internal/catalog/**` (read-only for you).
5. **Invoke the `claude-api` skill before writing any Anthropic SDK code.** Use context7 for pgx.

## You own (write only here)
`internal/llm/**`, `internal/segment/**`, `internal/content/**`, `docs/eval/**`.
Read-only for you: everything else, including `internal/catalog` and `docs/contracts`. Need a change there?
Add it to `need_entries` in your result (or, as a manual session, append a need.md entry, agent code `LLM`).

## Work items (P0 first; stop and report before P1 if time or limits are tight)
**P0 — segment** (`internal/segment`)
- [ ] `Parse`: strict decode (`DisallowUnknownFields`, reject trailing data), structured issues with JSON paths.
- [ ] `Validate`: every rule in segment-dsl.md §2–§5, contradictions per N-ORC-004, warnings vs errors.
- [ ] `Compile`: whitelist SQL from `catalog.Field.SQL`, values only as `$n`; rejects the zero `ValidRule`.
- [ ] `Render(rule, locale)` in tr and en; `Count` with `SET LOCAL statement_timeout` (total/sms/email/both;
      reachable = consent for that channel).
**P0 — content** (`internal/content`)
- [ ] Variable parser per `template_vars.json` (`unknown_variable`, `malformed_variable`).
- [ ] `Validate`: SMS ≤ 160 after worst-case personalization, subject ≤ 80 single line, body 1..5000, control
      characters, URL/email/phone warnings; report shape exactly as api.md §2.
- [ ] Worst-case values query: longest consented `first_name`/`city` per channel for a `ValidRule`, capped per
      N-ORC-005. `RenderEmailHTML(body, locale, vars)`: escape, then paragraphs; raw model HTML never used.
**P0 — LLM service** (`internal/llm`)
- [ ] Prompts as embedded, versioned files (`prompts/segment_v1.tmpl`, `content_v1.tmpl`); catalog text generated
      from `internal/catalog`; brief wrapped in `<brief>` and declared as data; 2–3 few-shots incl. the official
      brief; worst-case lengths in the content prompt; stable prefix marked `Cache: true`.
- [ ] `Service.ProposeSegment` / `ProposeContent`: stop-reason handling, validation, ≤ 2 repairs with the
      structured errors, `llm.Error` codes, metrics (`internal/metrics`), one `llm_calls` row per call via a
      Postgres `Recorder` you implement in `internal/llm` (cost from a price table with source + date).
- [ ] Streaming: forward deltas as they arrive; accumulate bytes; never cut a rune; ctx cancel stops the call.
- [ ] `mock`: scripted mode (queue of canned responses incl. malformed ones) + demo mode (deterministic
      keyword/catalog heuristic; official brief → the golden rule of segment-dsl.md §7; Turkish/English copy that
      passes validation, streamed in small chunks with small delays).
- [ ] `anthropic`: `output_config.format` with the catalog wire schema, adaptive thinking at effort `low`, SDK
      retries 2, per-call timeouts from config, refusal + max_tokens handling, **no** refusal-fallback beta unless
      `RefusalFallback == "default"` (default off). Contract tests against `httptest` asserting the outgoing request
      and replaying documented SSE event streams (fixtures cite their doc source).
- [ ] `claudecode`: per KICKOFF §6.9; parse a sanitized probe fixture in `testdata/`; never used in tests' happy path.

**P1** — `docs/eval/briefs.yaml` (≥ 15 briefs) + `make eval` runner (set-equality + Jaccard on the seed DB) +
`make smoke-live` implementation (refuses without the spend flag; skips cleanly without a key); fuzz targets for
Parse→Validate→Compile and for Render.

## Tests that must exist and pass (names carry requirement IDs)
`TestValidate_*_RAUD1` (table-driven: every field × operator, invalid combos), `TestCompile_Golden_RAUD1`
(SQL text + args), `TestCompile_RejectsZeroValue_RAUD1`, `TestCount_MatchesDirectSQL_RFLOW1` (integration),
`TestNotWithinLastDays_IncludesNull_RAUD1`, `TestCount_ExcludesNonConsented_RAUD2`, `TestParse_Vectors_RCNT3`,
`TestValidate_SMSWorstCase_RCNT1`, `TestValidate_Subject_RCNT2`, `TestService_RepairThen422_RAI1`,
`TestService_Refusal_RAI1`, `TestStream_SplitMultibyteRune_RFLOW2`, `TestStream_CancelStopsProvider_RFLOW2`
(goleak), `TestAnthropic_RequestShape_RAI1`, `TestAnthropic_ReplaySSE_RAI1`, `TestMockDemo_OfficialBrief_RFLOW1`.
Integration tests use `storetest.NewDB(t)`.

## Environment
- Shared Postgres from the main checkout: `TEST_DATABASE_URL=postgres://scc:scc@localhost:55432/postgres?sslmode=disable`
  (`make test-host` sets it). Each test gets its own database. If Postgres is down, report it; do not run compose.
- Your own binaries, if any, use ports 8100–8199. Never run `docker compose up`, `make up`, `make smoke-live`,
  anything with the spend flag, or read `.env`. `LLM_PROVIDER=claudecode` is allowed only for a manual probe.
- Commit small and often on your branch: `feat(llm): … [R-AUD-1]`, with the Co-Authored-By trailer.

## Done when
All P0 boxes ticked, `go vet ./... && go test -race ./internal/{llm,segment,content}/...` green with output
quoted, `golangci-lint run ./internal/llm/... ./internal/segment/... ./internal/content/...` clean.

## Result (subagent mode: end with exactly this JSON)
```json
{ "area": "llm", "requirement_ids_done": [], "tests": [{ "cmd": "", "exit_code": 0, "summary": "" }],
  "files_changed": [], "need_entries": [], "validation_log_entries": [], "open_issues": [] }
```
`need_entries` use the KICKOFF §4.2 fields; `validation_log_entries` the §13 format (AI mistakes you caught).

## Status log (manual sessions append here)

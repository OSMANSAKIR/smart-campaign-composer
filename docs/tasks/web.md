# Task: builder-web — Vue composer, live dashboard, system panel

You are **builder-web** (model: sonnet). You run either as a W1 subagent in your own git worktree or as the
manual session `04-builder-web` (`claude -w web`). Either way, follow this file.

## Goal
A marketer can go brief → audience → streamed content → edit → approve → watch delivery live, with clear
loading, empty and error states. Frontend is 20% of the score; clarity beats decoration.

## Read first (in this order)
1. `CLAUDE.md`, `web/CLAUDE.md`, `need.md` (N-ORC-002, N-ORC-006, N-PLAN-004, N-PLAN-005).
2. `KICKOFF.md` §8 (all of it), §6.5 (content rules), §5.5.
3. `docs/contracts/api.md` (every object, endpoint and SSE event), `docs/contracts/testvectors/*.json`.
4. The scaffold in `web/` (`src/lib/textCount.ts` already matches the vectors).
5. Use context7 for Vue 3, Vite, Vitest and Playwright docs. Use the `frontend-design` skill for the visual design.

## You own (write only here)
`web/**` except `web/embed_*.go`. Read-only for you: everything else. Need an API change? Put it in
`need_entries` (manual session: need.md entry with agent code `WEB`). Do not invent fields the contract lacks.

## Work items (P0 first; stop and report before P1 if time or limits are tight)
**P0**
- [ ] Typed API client for every endpoint in api.md, with the error model mapped to human-readable messages
      (LLM failure, 422 with details, refusal, 409, network). TypeScript strict; no `any`.
- [ ] SSE: a `fetch`-stream parser for `POST …/content/generate` (`meta`, `delta`, `repairing`, `result`,
      `error`), and an `EventSource` wrapper for `GET …/events` that treats each `snapshot` as the full state.
- [ ] Tolerant partial-JSON reader that extracts `sms.text`, `email.subject`, `email.body` from incomplete JSON
      for live display (display only; the `result` event replaces it). Handles split multibyte characters
      (string chunks are already decoded, but escapes like `ş` may be split).
- [ ] Composer stepper: **Brief** (prefilled with the official example brief from KICKOFF §2, example chips) →
      **Audience** (server-rendered rule chips, raw JSON behind a toggle, total/SMS/email counts,
      `unsupported_criteria` and `assumptions` as warnings, LLM metadata) → **Content** (fields fill while
      streaming; "fixing…" state on `repairing`; editable; live counters per field: runes, worst-case length,
      SMS encoding and segments; debounced `POST /api/content/validate`; badges per error/warning; save with
      `PUT …/content` and `base_version`) → **Approve** (channel checkboxes, confirmation dialog with exact
      recipient counts, a fresh `Idempotency-Key` per approve attempt, button disabled until the latest saved
      version is valid for the chosen channels).
- [ ] Campaign dashboard: per-channel counts (queued, sending, sent, retry, failed, skipped), progress bar and
      ETA, error breakdown, recent events, reconnect handling.
- [ ] Campaign list and system panel (`/api/system/status`): health of api/db/worker/provider, LLM provider and
      model, and the banner "Mock LLM — set ANTHROPIC_API_KEY for real generation" when `llm.mock` is true.
- [ ] Accessibility: labels on all inputs, keyboard-operable stepper and dialogs, visible focus, `aria-live` for
      streaming and validation messages. Works on a laptop screen.
**P1** — throughput SVG chart (msg/s per channel with the 100/s line), mock-provider fault controls
(`/admin/config` through a small API proxy — ask via `need_entries` first), Playwright e2e (`make e2e`):
official brief → audience → streamed content → edit → approve → dashboard reaches completed.

## Tests that must exist and pass (Vitest; names carry requirement IDs)
`textCount` vectors (exists), template-variable vectors from `template_vars.json` (`parse` section) — RCNT3,
SSE parser incl. events split across chunks — RFLOW2, partial-JSON reader incl. a split `\u` escape and a
multibyte character at a chunk boundary — RFLOW2, approve gating (disabled when invalid, stale or empty
audience) — RFLOW3, error-message mapping for each API error code, snapshot replacement on reconnect — RFLOW5.

## Environment
- The API endpoints are being built in parallel by the orchestrator; until they answer, develop against the
  contract with mocked `fetch` in tests. `npm run dev` (port 5173, or 8300–8399 if taken) proxies to
  `localhost:8080` when the stack from the main checkout is up.
- Run `npm ci` once in `web/`. Never run `docker compose up`, `make up`, `make smoke-live`, anything with the
  spend flag, or read `.env`.
- Commit small and often on your branch: `feat(web): streaming content step [R-FLOW-2]`, with the
  Co-Authored-By trailer.

## Done when
All P0 boxes ticked; in `web/`: `npm run typecheck`, `npm test` and `npm run build` all green with output quoted.

## Result (subagent mode: end with exactly this JSON)
```json
{ "area": "web", "requirement_ids_done": [], "tests": [{ "cmd": "", "exit_code": 0, "summary": "" }],
  "files_changed": [], "need_entries": [], "validation_log_entries": [], "open_issues": [] }
```
`need_entries` use the KICKOFF §4.2 fields; `validation_log_entries` the §13 format (AI mistakes you caught).

## Status log (manual sessions append here)

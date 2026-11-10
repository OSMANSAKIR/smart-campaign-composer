# Phase 2 — web builder (all UI for F1–F7, the F2 rule editor)

Read `docs/tasks/phase2-common.md` and all of `docs/contracts/api.md` §9 first. The backend lands in parallel:
build against the contract, test with stubbed `fetch` (Vitest), and keep the existing e2e spec compiling.

## Paths you may write

`web/**` only.

## Work, in this order

1. **Types and client (Must).** `web/src/lib/types.ts` and `api.ts` for every v2 shape: content version with
   `variants`, campaign `send_variant`/`scheduled_at`/`cancelled_at`, statuses `scheduled`/`cancelled`, message
   status `cancelled` and `variant`, progress `cancelled` + `variants`, `llm.cost_recorded`, catalog (§9.1),
   segment `original_rule`/`edited_at`, error codes `variant_missing`, `invalid_send_at`,
   `llm_provider_rejected`; client calls `previewSegment`, `saveContent` v2 body, `approve` with `variant` and
   optional `sendAt`, `cancel`, `refineUrl`.
2. **F1 A/B (Must, B-2).** Content step: two cards side by side (stacked on narrow screens), each with its label
   and editable SMS / subject / body, per-variant counters and validation (reuse `ContentField`), streaming
   preview that shows both variants as they arrive (extend the tolerant partial-JSON display parser for
   `variants.a`/`variants.b`; the `result` event is canonical). Save sends the v2 body. Approve dialog: choice
   "Send A", "Send B", "A/B test (50/50)" (B options disabled when there is no B or it is invalid for a
   selected channel). Dashboard: per-variant sent/failed per channel when the campaign has variants. Messages
   table shows the variant. Small text near the A/B choice: "Delivery is split 50/50 per user; the mock provider
   has no open or click data."
3. **F2 rule editor (Must, N-WEB-001 B).** In the audience step, "Edit rule": groups (AND between groups, OR
   inside a group — check `docs/contracts/segment-dsl.md` for the CNF meaning and the labels the renderer
   uses), conditions with a field select (catalog labels in the campaign language), an operator select limited
   to the field's `ops`, and a value input by type and operator (number with min/max/integer, enum select or
   multi-select from `values`, day count, date, none for bool/null ops). Add/remove conditions and groups within
   the catalog limits. Debounced (300 ms) `POST /api/segment/preview` shows errors per condition, warnings, the
   rendered rule and the audience counts; "Save rule" calls `PUT …/segment`, then the campaign reloads. An
   "Edited" badge when `segment.source === 'edited'`, and a collapsible "Claude's original rule" (rendered text
   of `original_rule` — render client-side from the catalog labels, or show the JSON if simpler). Vitest: value
   shapes per operator, op list per field type, debounce, preview error display.
4. **F6 notice and F7 message (Must).** When `llm.cost_recorded === false`: non-blocking notice "The content is
   ready, but its cost could not be recorded." (segment: "The audience is ready, but its cost could not be
   recorded."). `llm_provider_rejected`: show the server message as is ("The AI provider rejected the request
   (check the API key or credits)."), not the generic "failed or timed out" text. In the "How Claude produced
   this" panel add a **Cache write** row next to Cache read (validation-log V-022).
5. **F4 cancel (cut second).** Dashboard "Stop sending" button for `sending`/`scheduled` with an in-page
   confirmation (never `window.confirm`), then `POST …/cancel`; status `cancelled` shown; counters include
   cancelled; the stream's `done` ends live updates.
6. **F3 refine (cut third).** On each variant card: an instruction input (≤ 500 characters with a counter) and
   "Rewrite this variant"; streams like generate (`POST …/content/refine`), shows the new version; history hint
   "version N (refined: <instruction>)".
7. **F5 schedule (cut first).** Approve dialog: "Send now" / "Schedule" with a `datetime-local` input (min now +
   1 min, max now + 7 days, local time; send `send_at` as ISO 8601 with offset). Dashboard for `scheduled`: local
   send time and a live countdown; cancel available.
8. **e2e:** update `web/e2e/composer.spec.ts` so the existing flow still passes with two variants (pick "Send A"),
   and add one test for the rule editor (change the city, the count changes, save, "Edited" badge). The
   orchestrator runs `make e2e` after the merge; you run `npx playwright test --list` to check it compiles.

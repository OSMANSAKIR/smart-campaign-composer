# Phase 2 — delivery builder (F1 split + per-variant progress, F4 cancel, F5 scheduled send)

Read `docs/tasks/phase2-common.md` and `docs/contracts/api.md` §3, §7, §9.3, §9.5, §9.6 first.

## Paths you may write

- `internal/delivery/**`, `internal/mockprovider/**`, `cmd/worker/**`, `docs/stress/**`
- `internal/campaign/`: `approve.go`, `progress.go` (incl. the hub and `Terminal`), new `cancel.go`,
  `schedule.go`; in `model.go` only `Campaign` (fields `SendVariant`, `ScheduledAt`, `CancelledAt`),
  `Message` (`Variant`) and `ApproveResult`; in `service.go` only the **`campaigns` SELECT + Scan in `load`**,
  the `Messages` query and `MessageStatuses` (the llm builder edits the content parts of `service.go`); tests in
  new files `split_test.go`, `cancel_test.go`, `schedule_test.go`.
- `internal/api/`: the `approve` and `events` handlers in `campaigns.go`, `cancel.go` (replace the stub), new
  tests `cancel_test.go`.

The llm builder is changing content storage at the same time: a version is one `campaign_content` row per
variant (`variant` 'a'|'b'); do not depend on its Go code. Read variants with your own query
(`SELECT variant, sms_text, email_subject, email_body FROM campaign_content WHERE campaign_id = $1 AND
version = $2`); in tests insert variant-B rows with SQL.

## Work, in this order

1. **F1 split + per-variant delivery (Must, B-2).**
   - Approve body gains `variant` (`a` default | `b` | `ab`) and `send_at` (step 3). Validate (inside the
     existing transaction, after the stale check) every variant that will be sent, for every selected channel,
     with the same re-validation approve does today (detail paths `variants.<v>.`); missing B row →
     `variant_missing`. Store `send_variant`.
   - Enqueue: `variant` column = the mode, or for `ab` the formula in §9.3
     (`get_byte(decode(md5(campaign_id::text || ':' || user_id::text), 'hex'), 0) % 2`, 0 → a). Keep the
     advisory lock, consent SQL and `ON CONFLICT` exactly as they are.
   - Worker: `Claim` returns the variant; `Template(campaignID, version, variant)`; render the message's
     variant. Cache templates per (campaign, version, variant) if they are cached today.
   - Progress: `cancelled` counter and `variants` per channel (§9.3); message list items carry `variant`.
   - Tests: split determinism (same input → same variant; recompute in Go with `crypto/md5` and compare with
     the stored variants), balance within ±5 % on a 5000-user seed, one variant per user across sms+email,
     per-variant counts sum to channel totals, Send B renders B (dispatcher test with the in-process provider:
     the provider receives B's text), `variant_missing`, invalid B blocks `ab` but not `a`.
2. **F4 cancel (Must-ish; cut second).** `campaign.Cancel` per §9.5 (one transaction; idempotent; 409 for other
   statuses). Worker side: a claimed message of a cancelled campaign whose outcome is retry/throttle/pause
   becomes `cancelled` (guarded update), an expired lease of a cancelled campaign → `cancelled`; the reconciler
   (or a sweep next to it) moves leftover `queued`/`retry` of cancelled campaigns to `cancelled`. `Reconcile`
   must never complete a cancelled campaign. Events stream: `done` for `cancelled` once no message is
   `sending`. Work out the exact bound of messages sent after the cancel commits (claimed batch size and
   in-flight semaphore per channel) and state it in `decisions`. Tests: cancel idempotent, 409 from
   `content_ready`/`completed`, race test — cancel while the dispatcher is mid-batch against the in-process
   provider: after the dispatcher drains, provider-received count ≤ (sent before cancel + bound), every message
   terminal, sent + failed + cancelled + skipped = total; the worker never claims a cancelled campaign's message.
3. **F5 scheduled send (Should; cut first).** `send_at` per §9.6 (50 s .. 7 days, server clock; 422
   `invalid_send_at`); approve enqueues as usual and moves `content_ready → scheduled`, `scheduled_at` stored
   UTC. Scheduler in the worker (next to the reconciler, every 1 s, every replica, guarded update, database
   clock): `scheduled → sending WHERE scheduled_at <= now()`. Cancel works on `scheduled`. Tests with the
   database clock: a campaign scheduled in the future is never claimed; after `UPDATE … scheduled_at = now() -
   interval '1 s'` the scheduler activates it and it drains; bounds of `send_at`; consent revoked while
   scheduled → `skipped_no_consent`.
4. Stress S1 is run by the orchestrator after the merge; do not run the stress suites (they rewrite the
   committed reports). Keep every existing delivery test green (`go test -race -count=1 ./internal/delivery/...`).

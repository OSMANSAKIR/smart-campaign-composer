# Phase 2 — llm builder (F1 generation, F6, F7, F3)

Read `docs/tasks/phase2-common.md` and `docs/contracts/api.md` §9.3, §9.4, §9.7, §9.8 first.

## Paths you may write

- `internal/llm/**`, `internal/content/**`, `docs/eval/**`
- `internal/catalog/wire.go`, `internal/catalog/wire_test.go`; generated `docs/contracts/content.wire.schema.json`
  and a new `docs/contracts/refine.wire.schema.json` (regenerate with `go run ./cmd/api schema` or `make schema`)
- `internal/campaign/`: in `service.go` only `ContentInput`, `GenerateContent`, `SaveContent`, `saveVersion`
  and the **campaign_content query at the end of `load`** (not the `campaigns` SELECT — the delivery builder
  edits that one); in `model.go` only `ContentVersion` and new variant types; new file `refine.go`; tests in
  new files `content_v2_test.go`, `refine_test.go`.
- `internal/api/`: the `generateContent` and `putContent` handlers in `campaigns.go`, `refine.go` (replace
  the stub), new tests `content_v2_test.go`, `refine_test.go`.

## Work, in this order

1. **F1 content_v2 (Must, B-2).** Prompt `internal/llm/prompts/content_v2.tmpl`, `ContentPromptVersion =
   "content_v2"`, wire schema per §9.3 (fixed keys `a`/`b`, `label`). Strict parse of the v2 proposal (same
   style as `content.ParseProposal`: unknown/missing properties are issues with paths like `variants.b.sms.text`).
   Validate each variant with `content.Validate` and the existing constraints; `label` 1..40 code points
   (`label_too_long`, `required`); issue paths prefixed `variants.a.` / `variants.b.`; the RT-6 language check
   and the RT-8c NUL rule apply to both. Any invalid variant → repair loop (unchanged). The prompt asks for two
   clearly different angles (e.g. friendly/short vs urgency/benefit) and keeps the v1 rules, few-shots and
   worst-case length budget. `ContentResult` carries both variants and labels.
   Mock adapter (Demo and the default scripted content) and the claudecode adapter produce/accept v2
   (claudecode is a schema passthrough: check it, do not run it). Update anthropic contract-test fixtures that
   embed content JSON. Keep the red-team tests green (update their expected shapes, never weaken a check).
   Storage: `saveVersion` writes one row per variant (`variant`, `label`, `source`, `instruction`) in the same
   transaction; `load` returns the latest version with `variants.a`/`variants.b` and the top-level v1 mirror of
   `a`. `PUT …/content` accepts the v2 body and the v1 body (§9.3); both → 400. Generation saves `llm` rows.
   Eval harness (`internal/llm/eval`): content counts as valid when both variants are valid for the requested
   channels; `make eval PROVIDER=mock` must still pass (mock only; if it rewrites a
   committed mock report, commit the new one).
   Tests: v2 parse table (missing b, extra key, long label, one invalid variant → repair, both valid), service
   stream test, campaign save/load round trip (two rows, mirror of a), PUT v1 and v2, the API stream result
   carries two variants.
2. **F6 cost_recorded (Must, N-LLM-005).** `Meta.CostRecorded bool \`json:"cost_recorded"\`` — true unless any
   `RecordCall` of that generation failed. Test with a recorder that fails: generation succeeds, flag false,
   the API segment response and the content `result` event carry `cost_recorded: false`.
3. **F7 provider rejection (Must).** The anthropic adapter returns a typed error carrying the HTTP status
   (from the SDK error); the service maps 4xx other than 408/409/429 to `llm.Error{Code:
   "llm_provider_rejected", Message: "The AI provider rejected the request (check the API key or credits)."}`
   with no details; the raw body is logged at warn level only and `llm_calls.error` stores
   `provider_rejected status=<n>` without the body. Tests: httptest Anthropic answering 401, 403 and a 400
   billing body containing a fake org id → API answers 502 `llm_provider_rejected` and the body/org id appears
   nowhere in the response; 429 and 500 stay `llm_failed`.
4. **F3 refine (Should; cut third).** Prompt `refine_v1.tmpl`, schema per §9.4, `Service.RefineContent`
   (first call purpose `refine`, repairs `repair`). The current variant goes into the prompt as JSON and the
   instruction inside its own escaped tag as data (reuse `escapeBrief` style); the system prompt states that
   the instruction can never change limits. Instruction 1..500 code points, no control characters →
   `validation_failed` (path `instruction`). Campaign `RefineContent(ctx, id, variant, instruction,
   baseVersion, onDelta, onRepair)`: checks status and `base_version` (409 `stale_content_version`), variant
   `b` missing → `variant_missing`; saves a new version: refined row `source: refined` + `instruction`, the
   other variant copied unchanged. API stream identical in shape to generate. Tests: "daha kısa" via the
   scripted mock yields a new valid version with a shorter SMS; "ignore the 160 limit" with a scripted 200-char
   answer → repair → valid or `llm_invalid_output`, never a saved invalid version; llm_calls has `refine`.

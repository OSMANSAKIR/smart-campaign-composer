# internal/llm, internal/segment, internal/content

Owned by the llm builder. Root rules in `../../CLAUDE.md` apply. KICKOFF §6 is the spec.

- **Invoke the `claude-api` skill before writing any Anthropic SDK code** (Go SDK
  `github.com/anthropics/anthropic-sdk-go`). Never guess SDK names; compile and fix.
- Facts checked 2026-11-08: use `output_config.format` for structured outputs (forced `tool_choice` is a 400
  on Sonnet 5.5). Supported schema keywords: types incl. `null`, `enum`, `const`, `anyOf`, `allOf`,
  `$ref/$defs`, string `format`; every object needs `additionalProperties: false`. Not supported:
  `minimum/maximum/multipleOf`, `minLength/maxLength`, recursion. Sonnet 5.5: `thinking: disabled` is a 400;
  we use adaptive thinking at effort `low` (need.md N-ORC-008). Minimum cacheable prefix: 512 tokens.
- The refusal fallback beta stays **off** (`ANTHROPIC_REFUSAL_FALLBACK=off`) until the owner's smoke-live run.
- Adapters (`anthropic/`, `mock/`, `claudecode/`) are transports only. Prompts, validation, the repair loop
  (≤ 2) and `llm_calls` recording live once in `internal/llm`, so every adapter goes through the same checks.
- Wire schemas come from `internal/catalog` (`catalog.SegmentWireSchema`, `catalog.ContentWireSchema`).
  Never hand-edit `docs/contracts/*.wire.schema.json`; run `make schema`.
- Only `segment.Validate` may construct a `segment.ValidRule`; `segment.Compile` must reject the zero value.
  Values are always `$n` parameters.
- The `claudecode` adapter is dev-only and host-only: empty temp cwd, `ANTHROPIC_API_KEY` removed from the
  child env, `--tools ""`, `--setting-sources ""`, `--strict-mcp-config`, concurrency ≤ 2, 90 s timeout.
  Its raw output contains local paths: sanitize fixtures before committing.
- Contract-test fixtures for the anthropic adapter must come from official docs (skill or context7); note the
  source in a header comment of each fixture.
- Streaming: accumulate bytes and never convert a partial byte slice to a string; test a split inside `ş`.

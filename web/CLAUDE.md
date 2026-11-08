# web/ — Vue 3 + TypeScript (strict) + Vite

Owned by the web builder. Root rules in `../CLAUDE.md` apply.

- Contracts: `../docs/contracts/api.md` (endpoints, error model, SSE events). Do not invent fields; if the
  contract is missing something, return a `need_entries` item.
- Use context7 for Vue, Vite, Vitest and Playwright docs. Use the `frontend-design` skill for the look;
  clarity beats decoration.
- `npm run dev` serves on 5173 and proxies `/api`, `/healthz`, `/readyz` to `localhost:8080`.
- `npm test` runs Vitest. `npm run build` runs `vue-tsc` and then Vite; the output in `dist/` is embedded
  into the Go API binary (`web/embed_spa.go`, build tag `spa`).
- Character counting must match the server exactly: code points (`[...s].length`), UTF-16 units for UCS-2.
  `../docs/contracts/testvectors/*.json` are run by both Vitest and Go tests. Never edit the vectors here.
- Generation streaming is a POST answered with `text/event-stream`: read it with `fetch` and the SSE parser
  in `src/lib/`. The partial-JSON parser is for display only; the server's final `result` event is canonical.
- The campaign dashboard uses `EventSource` (GET). On reconnect, take the `snapshot` event as the truth.
- Approve stays disabled until the server's validation report says the content is valid for the chosen
  channels. Show every error state in human words (LLM failure, 422, refusal, network).
- Accessibility: every input has a label; everything works with the keyboard.

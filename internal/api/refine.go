package api

import (
	"context"
	"encoding/json"
	"net/http"

	"smartcampaign/internal/llm"
)

// refineContent rewrites one variant with an instruction (docs/contracts/api.md §9.4). The checks (404,
// instruction, 409 state/stale version, variant_missing) run before the stream opens, so they are plain JSON
// errors; afterwards the stream has the same shape as content generation: meta, delta, repairing, then
// result | error.
func (s *Server) refineContent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Variant     *string `json:"variant"`
		Instruction *string `json:"instruction"`
		BaseVersion *int    `json:"base_version"`
	}
	if !decode(w, r, &req) {
		return
	}
	var missing []Issue
	for name, absent := range map[string]bool{"variant": req.Variant == nil, "instruction": req.Instruction == nil, "base_version": req.BaseVersion == nil} {
		if absent {
			missing = append(missing, Issue{Path: name, Code: "required", Message: "missing"})
		}
	}
	if len(missing) > 0 {
		writeError(w, 400, "bad_request", "Fields variant, instruction and base_version are required.", missing)
		return
	}
	id := r.PathValue("id")
	plan, err := s.campaigns.PlanRefine(r.Context(), id, *req.Variant, *req.Instruction, *req.BaseVersion)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.d.ContentTimeout)
	defer cancel()
	sse := newSSE(w)
	p := s.d.LLM.Provider()
	sse.send("meta", map[string]string{"provider": p.Name(), "model": p.Model(), "prompt_version": llm.RefinePromptVersion})
	cv, meta, err := s.campaigns.RunRefine(ctx, plan,
		func(d llm.Delta) { sse.send("delta", map[string]string{"text": d.JSONFragment}) },
		func(n llm.RepairNotice) {
			errs := n.Errors
			if len(errs) == 0 {
				errs = json.RawMessage("[]")
			}
			sse.send("repairing", map[string]any{"attempt": n.Attempt, "errors": errs})
		})
	if err != nil {
		code, msg, details := s.describe(r, err)
		sse.send("error", map[string]any{"error": map[string]any{"code": code, "message": msg, "details": details}})
		return
	}
	sse.send("result", map[string]any{"content": cv, "llm": meta})
}

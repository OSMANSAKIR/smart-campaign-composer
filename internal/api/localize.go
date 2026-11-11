package api

import (
	"context"
	"encoding/json"
	"net/http"

	"smartcampaign/internal/llm"
)

// localizeContent adapts the latest content version to the other locale (docs/contracts/api.md §9.10). The
// checks (404, target_locale, 409 state, variant_missing) run before the stream opens, so they are plain JSON
// errors; afterwards the stream has the shape of refine: meta, delta, repairing, then result | error.
func (s *Server) localizeContent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetLocale *string `json:"target_locale"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.TargetLocale == nil {
		writeError(w, 400, "bad_request", "Field `target_locale` is required.", []Issue{{Path: "target_locale", Code: "required", Message: "missing"}})
		return
	}
	plan, err := s.campaigns.PlanLocalize(r.Context(), r.PathValue("id"), *req.TargetLocale)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.d.ContentTimeout)
	defer cancel()
	sse := newSSE(w)
	p := s.d.LLM.Provider()
	sse.send("meta", map[string]string{"provider": p.Name(), "model": p.Model(), "prompt_version": llm.LocalizePromptVersion})
	cv, meta, err := s.campaigns.RunLocalize(ctx, plan,
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

package api

import (
	"encoding/json"
	"net/http"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/segment"
)

// previewSegment: POST /api/segment/preview (docs/contracts/api.md §9.2). The rule goes through the same
// strict decoder as model output and PUT …/segment; decode problems are reported as preview errors.
func (s *Server) previewSegment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule json.RawMessage `json:"rule"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Rule) == 0 || string(req.Rule) == "null" {
		writeError(w, 400, "bad_request", "Field `rule` is required.", []Issue{{Path: "rule", Code: "required", Message: "missing"}})
		return
	}
	rule, issues := segment.ParseRule(req.Rule)
	if len(issues) > 0 {
		writeJSON(w, http.StatusOK, campaign.Preview{Errors: issues, Warnings: []campaign.Issue{}, Rendered: map[string][]string{}})
		return
	}
	p, err := s.campaigns.PreviewSegment(r.Context(), rule)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

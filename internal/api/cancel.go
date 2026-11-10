package api

import "net/http"

// cancelCampaign stops a scheduled or sending campaign (docs/contracts/api.md §9.5): 200 with the campaign
// and the number of messages cancelled; calling it again returns `cancelled: 0`; other statuses are 409.
func (s *Server) cancelCampaign(w http.ResponseWriter, r *http.Request) {
	res, err := s.campaigns.Cancel(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

package api

import "net/http"

// cancelCampaign: docs/contracts/api.md §9. Stub until the delivery builder (F4) lands.
func (s *Server) cancelCampaign(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented yet.", nil)
}

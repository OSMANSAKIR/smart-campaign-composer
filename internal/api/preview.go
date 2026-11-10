package api

import "net/http"

// previewSegment: docs/contracts/api.md §9. Stub until the orchestrator (F2) lands.
func (s *Server) previewSegment(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented yet.", nil)
}

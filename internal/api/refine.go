package api

import "net/http"

// refineContent: docs/contracts/api.md §9. Stub until the llm builder (F3) lands.
func (s *Server) refineContent(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented yet.", nil)
}

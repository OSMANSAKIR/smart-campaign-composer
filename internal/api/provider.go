package api

import (
	"bytes"
	"io"
	"net/http"
	"time"
)

// Fault controls for the mock provider (need.md N-WEB-003, KICKOFF §8 P1): the dashboard changes the
// provider's latency, error, 429 and hang rates at runtime to demo the retry and rate-limit behavior. The API
// forwards to the provider's /admin/config, which validates the fields (unknown fields and bad values → 400).

const maxProviderConfigBody = 4 << 10

func (s *Server) getProviderConfig(w http.ResponseWriter, r *http.Request) {
	s.forwardProvider(w, r, http.MethodGet, nil)
}

func (s *Server) putProviderConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProviderConfigBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "The request body is too large or unreadable.", nil)
		return
	}
	s.forwardProvider(w, r, http.MethodPost, body)
}

func (s *Server) forwardProvider(w http.ResponseWriter, r *http.Request, method string, body []byte) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, s.d.ProviderURL+"/admin/config", rd)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Could not build the provider request.", nil)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_unavailable", "The message provider did not answer.", nil)
		return
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_unavailable", "The message provider's answer was cut off.", nil)
		return
	}
	if resp.StatusCode >= 300 {
		// The provider's own message names the bad field; keep our error model around it.
		writeError(w, http.StatusBadRequest, "validation_failed", "The provider rejected the configuration.",
			[]Issue{{Path: "", Code: "invalid_config", Message: string(bytes.TrimSpace(out))}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

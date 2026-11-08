package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store/storetest"
)

func newTestServer(t *testing.T) *httptest.Server {
	pool := storetest.NewDB(t)
	srv := httptest.NewServer(New(Deps{
		Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(mock.NewDemo(), nil),
		ProviderURL: "http://127.0.0.1:1", Version: "test",
	}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthAndReadiness_ROBS3(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/api/catalog", "/api/system/status"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
	}
}

func TestNotBuiltEndpoints_UseErrorModel(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/campaigns", "application/json", strings.NewReader(`{"brief":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Error struct {
			Code    string  `json:"code"`
			Details []Issue `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotImplemented || body.Error.Code != "not_implemented" || body.Error.Details == nil {
		t.Fatalf("status %d body %+v", resp.StatusCode, body)
	}
}

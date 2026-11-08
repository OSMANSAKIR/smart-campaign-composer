package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smartcampaign/internal/config"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/store/storetest"
)

// The dashboard's fault controls reach the real provider handler through the API (R-RES-3, N-WEB-003).
func TestProviderConfig_ProxyReadsAndUpdates_RRES3(t *testing.T) {
	prov := mockprovider.New(config.ProviderFaults{LatencyMinMS: 20, LatencyMaxMS: 300, ErrorRate: 0.02, RateLimit: 100}, metrics.New())
	psrv := httptest.NewServer(prov.Handler())
	t.Cleanup(func() { prov.Close(); psrv.Close() })
	srv := httptest.NewServer(New(Deps{Pool: storetest.NewDB(t), Metrics: metrics.New(), LLM: llm.NewService(mock.NewDemo(), nil),
		ProviderURL: psrv.URL, Version: "test"}).Handler())
	t.Cleanup(srv.Close)

	type cfg struct {
		ErrorRate float64 `json:"error_rate"`
		Rate429   float64 `json:"rate_429"`
		RateLimit int     `json:"rate_limit"`
	}
	get := func() cfg {
		t.Helper()
		code, _, body := do(t, http.MethodGet, srv.URL+"/api/system/provider-config", "", nil)
		var c cfg
		if code != http.StatusOK || json.Unmarshal(body, &c) != nil {
			t.Fatalf("get: %d %s", code, body)
		}
		return c
	}
	if c := get(); c.ErrorRate != 0.02 || c.RateLimit != 100 {
		t.Fatalf("initial config %+v", c)
	}
	if code, _, body := do(t, http.MethodPut, srv.URL+"/api/system/provider-config", `{"error_rate":0.1,"rate_429":0.05}`, nil); code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}
	if c := get(); c.ErrorRate != 0.1 || c.Rate429 != 0.05 || c.RateLimit != 100 {
		t.Fatalf("after a partial update %+v, want merged", c)
	}
	for _, bad := range []string{`{"error_rate":1.5}`, `{"no_such_field":1}`, `not json`} {
		code, _, body := do(t, http.MethodPut, srv.URL+"/api/system/provider-config", bad, nil)
		if code != http.StatusBadRequest || !strings.Contains(string(body), `"validation_failed"`) {
			t.Fatalf("%s: %d %s, want 400 validation_failed", bad, code, body)
		}
	}
	psrv.Close()
	if code, _, body := do(t, http.MethodGet, srv.URL+"/api/system/provider-config", "", nil); code != http.StatusBadGateway {
		t.Fatalf("provider down: %d %s, want 502", code, body)
	}
}

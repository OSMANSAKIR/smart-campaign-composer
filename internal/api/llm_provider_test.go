package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"smartcampaign/internal/clock"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/anthropic"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store/storetest"
)

const fakeOrg = "org_7c1e9a52-0b3d-4f6a-9d1e-aa11bb22cc33"

// fakeAnthropic answers every Messages call with status and a body that carries a fake organization id, like
// a real billing error would.
func fakeAnthropic(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("retry-after", "0")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the API. Organization `+fakeOrg+`"},"request_id":"req_x"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// F7: 401, 403 and a 400 billing error end as 502 llm_provider_rejected on the segment call and as an
// `error` event on the content stream; neither carries the provider's body or the organization id. 429 and
// 500 stay llm_failed.
func TestProviderRejection_F7(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		code   string
	}{
		"401":                {401, "llm_provider_rejected"},
		"403":                {403, "llm_provider_rejected"},
		"400 billing":        {400, "llm_provider_rejected"},
		"429 stays failed":   {429, "llm_failed"},
		"500 stays failed":   {500, "llm_failed"},
		"408 timeout failed": {408, "llm_failed"},
	} {
		t.Run(name, func(t *testing.T) {
			fake, calls := fakeAnthropic(t, tc.status)
			ad, err := anthropic.New(anthropic.Options{APIKey: "test-key", BaseURL: fake.URL, MaxRetries: 1})
			if err != nil {
				t.Fatal(err)
			}
			pool := storetest.NewDB(t)
			srv := httptest.NewServer(New(Deps{Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(ad, llm.NewPGRecorder(pool)),
				ProviderURL: "http://127.0.0.1:1", Version: "test"}).Handler())
			t.Cleanup(srv.Close)
			code, _, body := do(t, http.MethodPost, srv.URL+"/api/campaigns", `{"brief":"İstanbul'a indirim duyurusu"}`, nil)
			var c struct{ ID string }
			if _ = json.Unmarshal(body, &c); code != http.StatusCreated {
				t.Fatalf("create: %d %s", code, body)
			}

			// Segment generation: plain JSON error.
			code, _, body = do(t, http.MethodPost, srv.URL+"/api/campaigns/"+c.ID+"/segment/generate", `{}`, nil)
			var e struct {
				Error struct {
					Code    string          `json:"code"`
					Message string          `json:"message"`
					Details json.RawMessage `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &e); err != nil || code != 502 || e.Error.Code != tc.code {
				t.Fatalf("segment: %d %s (want 502 %s)", code, body, tc.code)
			}
			assertNoLeak(t, string(body))
			if tc.code == "llm_provider_rejected" {
				if e.Error.Message != "The AI provider rejected the request (check the API key or credits)." || string(e.Error.Details) != "[]" {
					t.Fatalf("message/details: %s", body)
				}
				if calls.Load() != 1 {
					t.Fatalf("a rejection must not be retried: %d calls", calls.Load())
				}
				var stored string
				if err := pool.QueryRow(context.Background(), `SELECT error FROM llm_calls WHERE campaign_id = $1`, c.ID).Scan(&stored); err != nil {
					t.Fatal(err)
				}
				if want := "provider_rejected status=" + strconv.Itoa(tc.status); stored != want {
					t.Fatalf("llm_calls.error = %q, want %q (no body)", stored, want)
				}
			}

			// Content stream: the same code in an `error` event.
			rule := `{"rule":{"groups":[{"conditions":[{"field":"city","op":"eq","value":"İstanbul"}]}]}}`
			if code, _, b := do(t, http.MethodPut, srv.URL+"/api/campaigns/"+c.ID+"/segment", rule, nil); code != http.StatusOK {
				t.Fatalf("segment put: %d %s", code, b)
			}
			resp, err := http.Post(srv.URL+"/api/campaigns/"+c.ID+"/content/generate", "application/json", strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			fs := frames(resp.Body, nil)
			last := fs[len(fs)-1]
			if last.event != "error" || !strings.Contains(last.data, `"`+tc.code+`"`) {
				t.Fatalf("last event %s %s", last.event, last.data)
			}
			for _, f := range fs {
				assertNoLeak(t, f.data)
			}
		})
	}
}

func assertNoLeak(t *testing.T, s string) {
	t.Helper()
	for _, bad := range []string{fakeOrg, "credit balance", "invalid_request_error", "req_x"} {
		if strings.Contains(s, bad) {
			t.Fatalf("the provider body leaked (%q): %s", bad, s)
		}
	}
}

type brokenRecorder struct{}

func (brokenRecorder) RecordCall(context.Context, llm.Call) error { return errors.New("insert failed") }

// N-LLM-005 (F6): with a recorder that fails, segment generation and the content `result` event still
// succeed and say cost_recorded=false; with a working recorder they say true.
func TestCostRecorded_NLLM005(t *testing.T) {
	for name, tc := range map[string]struct {
		rec  llm.Recorder
		want bool
	}{"recorder fails": {brokenRecorder{}, false}, "recorder ok": {nil, true}} {
		t.Run(name, func(t *testing.T) {
			pool := storetest.NewDB(t)
			srv := httptest.NewServer(New(Deps{Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(mock.NewDemoWith(clock.Real{}, 0, 64), tc.rec),
				ProviderURL: "http://127.0.0.1:1", Version: "test"}).Handler())
			t.Cleanup(srv.Close)
			code, _, body := do(t, http.MethodPost, srv.URL+"/api/campaigns", `{"brief":"İstanbul'daki kullanıcılara %30 indirim"}`, nil)
			var c struct{ ID string }
			if _ = json.Unmarshal(body, &c); code != http.StatusCreated {
				t.Fatalf("create: %d %s", code, body)
			}
			code, _, body = do(t, http.MethodPost, srv.URL+"/api/campaigns/"+c.ID+"/segment/generate", `{}`, nil)
			var seg struct {
				LLM struct {
					CostRecorded *bool `json:"cost_recorded"`
				} `json:"llm"`
			}
			if err := json.Unmarshal(body, &seg); err != nil || code != 200 || seg.LLM.CostRecorded == nil || *seg.LLM.CostRecorded != tc.want {
				t.Fatalf("segment: %d %s (want cost_recorded=%v)", code, body, tc.want)
			}
			_, meta := generate(t, srv.URL, c.ID)
			if got, ok := meta["cost_recorded"].(bool); !ok || got != tc.want {
				t.Fatalf("content result llm = %v (want cost_recorded=%v)", meta, tc.want)
			}
		})
	}
}

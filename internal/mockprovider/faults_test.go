package mockprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"smartcampaign/internal/clock"
	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// quiet is a configuration with every fault off.
var quiet = config.ProviderFaults{Seed: 7}

type harness struct {
	t   *testing.T
	p   *Server
	srv *httptest.Server
	cl  *http.Client
}

func newHarness(t *testing.T, cfg config.ProviderFaults, opts ...Option) *harness {
	t.Helper()
	p := New(cfg, metrics.New(), opts...)
	srv := httptest.NewServer(p.Handler())
	h := &harness{t: t, p: p, srv: srv, cl: &http.Client{Transport: &http.Transport{}}}
	t.Cleanup(func() {
		h.cl.CloseIdleConnections()
		p.Close()
		srv.Close()
	})
	return h
}

type reply struct {
	status     int
	retryAfter string
	body       string
	err        error
}

func smsBody(campaign string, user int) string {
	return fmt.Sprintf(`{"to":"+905321234567","text":"Merhaba","campaign_id":%q,"user_id":%d,"message_id":"m%d"}`, campaign, user, user)
}

func (h *harness) post(ctx context.Context, path, key, body string) reply {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := h.cl.Do(req)
	if err != nil {
		return reply{err: err}
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return reply{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"), body: buf.String()}
}

func (h *harness) sms(key, body string) reply {
	return h.post(context.Background(), "/v1/sms", key, body)
}

func (h *harness) configure(patch string) {
	h.t.Helper()
	resp, err := h.cl.Post(h.srv.URL+"/admin/config", "application/json", strings.NewReader(patch))
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		h.t.Fatalf("POST /admin/config %s: %d", patch, resp.StatusCode)
	}
}

type statsDoc struct {
	Channels map[string]ChannelStats `json:"channels"`
	Total    ChannelStats            `json:"total"`
}

func (h *harness) stats() statsDoc {
	h.t.Helper()
	resp, err := h.cl.Get(h.srv.URL + "/stats")
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var d statsDoc
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		h.t.Fatal(err)
	}
	return d
}

func TestMockProvider_FaultModes_RRES3(t *testing.T) {
	t.Run("error_rate=1 answers 500/502/503 and never stores them", func(t *testing.T) {
		h := newHarness(t, config.ProviderFaults{ErrorRate: 1, Seed: 1})
		seen := map[int]bool{}
		for range 60 {
			r := h.sms("k1", smsBody("c", 1))
			if r.status != 500 && r.status != 502 && r.status != 503 {
				t.Fatalf("status %d, want 5xx", r.status)
			}
			if !strings.Contains(r.body, "server_error") {
				t.Fatalf("body %q", r.body)
			}
			seen[r.status] = true
		}
		if len(seen) != 3 {
			t.Fatalf("saw statuses %v, want all of 500, 502, 503", seen)
		}
		// The failed key was not stored: with faults off the same key is a fresh delivery, not a replay.
		h.configure(`{"error_rate":0}`)
		if r := h.sms("k1", smsBody("c", 1)); r.status != 200 {
			t.Fatalf("after faults off: %d", r.status)
		}
		if s := h.stats().Channels["sms"]; s.Accepted != 1 || s.ReplayedKeys != 0 {
			t.Fatalf("stats %+v", s)
		}
	})

	t.Run("rate_429=1 answers 429 with Retry-After from config", func(t *testing.T) {
		h := newHarness(t, config.ProviderFaults{Rate429: 1, RetryAfterS: 3, Seed: 1})
		r := h.sms("k1", smsBody("c", 1))
		if r.status != 429 || r.retryAfter != "3" || !strings.Contains(r.body, "rate_limited") {
			t.Fatalf("got %+v", r)
		}
		if s := h.stats().Channels["sms"]; s.Accepted != 0 || s.StatusCounts["429"] != 1 || s.RejectedByLimiter != 0 {
			t.Fatalf("stats %+v", s)
		}
	})

	t.Run("hang_rate=1 never answers and the client times out", func(t *testing.T) {
		h := newHarness(t, config.ProviderFaults{HangRate: 1, Seed: 1})
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		r := h.post(ctx, "/v1/sms", "k1", smsBody("c", 1))
		if !errors.Is(r.err, context.DeadlineExceeded) {
			t.Fatalf("got %+v, want a client timeout", r)
		}
	})

	t.Run("permanent 400 and 422 are stored and replayed", func(t *testing.T) {
		h := newHarness(t, quiet)
		bad := `{"to":"0532","text":"x","campaign_id":"c","user_id":9}`
		empty := `{"to":"+905321234567","text":"  ","campaign_id":"c","user_id":9}`
		for _, tc := range []struct {
			key, body string
			want      int
		}{{"b1", bad, 400}, {"e1", empty, 422}, {"j1", `{not json`, 400}} {
			for i := range 2 {
				if r := h.sms(tc.key, tc.body); r.status != tc.want {
					t.Fatalf("%s call %d: %d, want %d", tc.key, i, r.status, tc.want)
				}
			}
		}
		if s := h.stats().Channels["sms"]; s.UniqueKeys != 3 || s.ReplayedKeys != 3 || s.Accepted != 0 {
			t.Fatalf("stats %+v", s)
		}
	})

	t.Run("email validates address and content", func(t *testing.T) {
		h := newHarness(t, quiet)
		good := `{"to":"ayse@example.com","subject":"Hi","html":"<p>x</p>","text":"x","campaign_id":"c","user_id":1}`
		cases := []struct {
			body string
			want int
		}{
			{good, 200},
			{`{"to":"not-an-email","subject":"Hi","text":"x"}`, 400},
			{`{"to":"a@b.co","subject":"","text":"x"}`, 422},
			{`{"to":"a@b.co","subject":"Hi"}`, 422},
		}
		for i, tc := range cases {
			if r := h.post(context.Background(), "/v1/email", fmt.Sprintf("e%d", i), tc.body); r.status != tc.want {
				t.Errorf("case %d: %d, want %d", i, r.status, tc.want)
			}
		}
	})

	t.Run("missing Idempotency-Key is a 400", func(t *testing.T) {
		h := newHarness(t, quiet)
		if r := h.sms("", smsBody("c", 1)); r.status != 400 || !strings.Contains(r.body, "missing_idempotency_key") {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("a fixed seed reproduces the same fault sequence, also after reset", func(t *testing.T) {
		run := func(h *harness) []int {
			var out []int
			for i := range 80 {
				out = append(out, h.sms(fmt.Sprintf("k%d", i), smsBody("c", i)).status)
			}
			return out
		}
		cfg := config.ProviderFaults{ErrorRate: 0.3, Rate429: 0.2, Seed: 42}
		a, b := newHarness(t, cfg), newHarness(t, cfg)
		first, second := run(a), run(b)
		if fmt.Sprint(first) != fmt.Sprint(second) {
			t.Fatalf("same seed, different sequences:\n%v\n%v", first, second)
		}
		resp, err := a.cl.Post(a.srv.URL+"/admin/reset", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if again := run(a); fmt.Sprint(again) != fmt.Sprint(first) {
			t.Fatalf("sequence after reset differs:\n%v\n%v", first, again)
		}
		kinds := map[int]int{}
		for _, s := range first {
			kinds[s]++
		}
		if kinds[200] == 0 || kinds[429] == 0 || kinds[500]+kinds[502]+kinds[503] == 0 {
			t.Fatalf("mix not exercised: %v", kinds)
		}
	})
}

// waitWaiters blocks (cooperatively, no sleeping) until n goroutines wait on the fake clock.
func waitWaiters(clk *clock.Fake, n int) {
	for clk.Waiters() < n {
		runtime.Gosched()
	}
}

func TestMockProvider_LatencyAndSpike_RRES3(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC))
	h := newHarness(t, config.ProviderFaults{LatencyMinMS: 50, LatencyMaxMS: 50, SpikeRate: 1, SpikeMS: 2000, Seed: 1}, WithClock(clk))
	done := make(chan reply, 1)
	go func() { done <- h.sms("k1", smsBody("c", 1)) }()
	waitWaiters(clk, 1)
	clk.Advance(2049 * time.Millisecond) // 50 ms base + 2 s spike is 2050 ms
	select {
	case r := <-done:
		t.Fatalf("answered %d before the spike elapsed", r.status)
	default:
	}
	clk.Advance(time.Millisecond)
	if r := <-done; r.status != 200 {
		t.Fatalf("status %d", r.status)
	}
}

func TestMockProvider_IdempotentHang_RSND3(t *testing.T) {
	const n = 40
	h := newHarness(t, config.ProviderFaults{HangRate: 1, Seed: 3})
	h.cl.Transport.(*http.Transport).MaxIdleConnsPerHost = n

	// Phase 1: every request hangs; the client gives up after 150 ms. Half of the hangs recorded the delivery.
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			if r := h.post(ctx, "/v1/sms", fmt.Sprintf("m%d", i), smsBody("c1", i)); r.err == nil {
				t.Errorf("request %d was answered with %d", i, r.status)
			}
		})
	}
	wg.Wait()
	afterHang := h.stats().Channels["sms"]
	if afterHang.Hangs != n {
		t.Fatalf("hangs = %d, want %d", afterHang.Hangs, n)
	}
	if afterHang.Accepted < n/4 || afterHang.Accepted > 3*n/4 {
		t.Fatalf("accepted-then-hung = %d of %d, want about half", afterHang.Accepted, n)
	}

	// Phase 2: the client retries every message with the same key. Deliveries recorded before the hang are
	// answered from the idempotency store, the others are delivered now; nobody gets two deliveries.
	h.configure(`{"hang_rate":0}`)
	for i := range n {
		if r := h.sms(fmt.Sprintf("m%d", i), smsBody("c1", i)); r.status != 200 {
			t.Fatalf("retry %d: %d", i, r.status)
		}
	}
	s := h.stats().Channels["sms"]
	if s.Accepted != n || s.DuplicateRecipientDeliveries != 0 {
		t.Fatalf("accepted=%d duplicates=%d, want %d and 0", s.Accepted, s.DuplicateRecipientDeliveries, n)
	}
	if want := afterHang.Accepted; s.ReplayedKeys != want {
		t.Fatalf("replayed keys = %d, want %d (the deliveries made before the hang)", s.ReplayedKeys, want)
	}
}

func TestMockProvider_OwnLimiterAndWindowStats_RRES3(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC))
	h := newHarness(t, config.ProviderFaults{RateLimit: 3, RetryAfterS: 2, Seed: 1}, WithClock(clk))

	for i := range 3 {
		if r := h.sms(fmt.Sprintf("k%d", i), smsBody("c", i)); r.status != 200 {
			t.Fatalf("request %d: %d", i, r.status)
		}
	}
	r := h.sms("k3", smsBody("c", 3))
	if r.status != 429 || r.retryAfter != "2" {
		t.Fatalf("4th request in the same second: %+v, want 429 with Retry-After 2", r)
	}
	s := h.stats().Channels["sms"]
	if s.RejectedByLimiter != 1 || s.MaxRequestsInAny1sWindow != 4 || s.MaxDeliveriesInAny1sWindow != 3 {
		t.Fatalf("stats %+v: want 1 rejected, 4 arrivals and 3 deliveries in the window", s)
	}

	// 999 ms after the first arrival the window still holds all three; at exactly 1 s the first has left it.
	clk.Advance(999 * time.Millisecond)
	if r := h.sms("k4", smsBody("c", 4)); r.status != 429 {
		t.Fatalf("at 999 ms: %d, want 429", r.status)
	}
	clk.Advance(time.Millisecond)
	if r := h.sms("k5", smsBody("c", 5)); r.status != 200 {
		t.Fatalf("at 1000 ms: %d, want 200", r.status)
	}
	// Rejected arrivals do not count against the admitted window, so the client is not locked out.
	if s := h.stats().Channels["sms"]; s.MaxDeliveriesInAny1sWindow != 3 || s.Accepted != 4 {
		t.Fatalf("stats %+v", s)
	}

	t.Run("limit is per channel", func(t *testing.T) {
		for i := range 3 {
			body := fmt.Sprintf(`{"to":"a%d@example.com","subject":"s","text":"t","campaign_id":"c","user_id":%d}`, i, i)
			if r := h.post(context.Background(), "/v1/email", fmt.Sprintf("e%d", i), body); r.status != 200 {
				t.Fatalf("email %d: %d", i, r.status)
			}
		}
	})
	t.Run("limit 0 turns the limiter off", func(t *testing.T) {
		h.configure(`{"rate_limit":0}`)
		for i := range 20 {
			if r := h.sms(fmt.Sprintf("z%d", i), smsBody("c2", i)); r.status != 200 {
				t.Fatalf("request %d: %d", i, r.status)
			}
		}
	})
}

func TestMockProvider_DeliveredListAndReset_RRES3(t *testing.T) {
	h := newHarness(t, quiet)
	for i := range 3 {
		h.sms(fmt.Sprintf("k%d", i), smsBody("cA", i))
	}
	h.sms("kx", smsBody("cB", 99))
	h.post(context.Background(), "/v1/email", "ke", `{"to":"a@b.co","subject":"s","text":"t","campaign_id":"cA","user_id":50}`)
	h.sms("kbad", `{"to":"bad","text":"x","campaign_id":"cA","user_id":77}`)

	list := func(q string) []Delivery {
		resp, err := h.cl.Get(h.srv.URL + "/stats/delivered" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var d struct {
			Items []Delivery `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
			t.Fatal(err)
		}
		return d.Items
	}
	if got := len(list("")); got != 5 {
		t.Errorf("all deliveries = %d, want 5 (the 400 is not a delivery)", got)
	}
	if got := len(list("?channel=sms&campaign_id=cA")); got != 3 {
		t.Errorf("sms/cA = %d, want 3", got)
	}
	if got := len(list("?channel=email")); got != 1 {
		t.Errorf("email = %d, want 1", got)
	}
	if resp, err := h.cl.Get(h.srv.URL + "/stats/delivered?channel=fax"); err != nil || resp.StatusCode != 400 {
		t.Errorf("unknown channel: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}

	resp, err := h.cl.Post(h.srv.URL+"/admin/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := len(list("")); got != 0 {
		t.Errorf("after reset: %d deliveries", got)
	}
	if s := h.stats(); s.Total.Requests != 0 || s.Total.Accepted != 0 {
		t.Errorf("stats after reset: %+v", s.Total)
	}
	// The idempotency store was cleared too: the same key is a new delivery.
	if r := h.sms("k0", smsBody("cA", 0)); r.status != 200 {
		t.Fatalf("status %d", r.status)
	}
	if s := h.stats().Channels["sms"]; s.Accepted != 1 || s.ReplayedKeys != 0 {
		t.Errorf("stats %+v", s)
	}
}

func TestMockProvider_DuplicateRecipientDetected_RSND3(t *testing.T) {
	h := newHarness(t, quiet)
	// Two different keys for the same (campaign, user): a client bug the provider must expose.
	h.sms("kA", smsBody("c", 1))
	h.sms("kB", smsBody("c", 1))
	s := h.stats()
	if s.Channels["sms"].DuplicateRecipientDeliveries != 1 || s.Total.DuplicateRecipientDeliveries != 1 {
		t.Fatalf("stats %+v", s)
	}
}

func TestMockProvider_AdminConfigValidation_RRES3(t *testing.T) {
	h := newHarness(t, quiet)
	post := func(body string) int {
		resp, err := h.cl.Post(h.srv.URL+"/admin/config", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, bad := range []string{
		`{"error_rate":1.5}`, `{"error_rate":-0.1}`, `{"rate_429":0.6,"error_rate":0.5}`,
		`{"latency_min_ms":500,"latency_max_ms":100}`, `{"latency_min_ms":-1}`, `{"spike_ms":-5}`,
		`{"retry_after_s":-1}`, `{"retry_after_s":99999}`, `{"rate_limit":-3}`,
		`{"no_such_field":1}`, `{"hang_rate":"lots"}`, `not json`, `{"seed":-1}`,
	} {
		if code := post(bad); code != 400 {
			t.Errorf("%s: status %d, want 400", bad, code)
		}
	}
	// A rejected patch changes nothing.
	resp, err := h.cl.Get(h.srv.URL + "/admin/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var cfg config.ProviderFaults
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil || cfg != quiet {
		t.Fatalf("config changed by rejected patches: %+v (%v)", cfg, err)
	}
	// A valid subset merges.
	if code := post(`{"error_rate":0.25,"retry_after_s":4}`); code != 200 {
		t.Fatalf("valid patch: %d", code)
	}
	h.configure(`{"rate_limit":50}`)
	resp2, _ := h.cl.Get(h.srv.URL + "/admin/config")
	defer resp2.Body.Close()
	_ = json.NewDecoder(resp2.Body).Decode(&cfg)
	if cfg.ErrorRate != 0.25 || cfg.RetryAfterS != 4 || cfg.RateLimit != 50 || cfg.Seed != 7 {
		t.Fatalf("merged config = %+v", cfg)
	}
}

func TestMockProvider_MetricsExposed_ROBS2(t *testing.T) {
	h := newHarness(t, quiet)
	h.sms("k1", smsBody("c", 1))
	h.sms("k2", smsBody("c", 1)) // duplicate recipient under a new key
	resp, err := h.cl.Get(h.srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	text := buf.String()
	for _, want := range []string{
		`mockprovider_requests_total{channel="sms",code="200"} 2`,
		`mockprovider_requests_total{channel="email",code="429"} 0`,
		`mockprovider_duplicate_deliveries_total{channel="sms"} 1`,
		`mockprovider_duplicate_deliveries_total{channel="email"} 0`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

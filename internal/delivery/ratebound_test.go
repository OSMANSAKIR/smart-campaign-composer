package delivery

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/store/storetest"
)

// providerStats reads the provider's own measurement of what arrived.
func providerStats(t testing.TB, baseURL string) map[string]mockprovider.ChannelStats {
	t.Helper()
	resp, err := http.Get(baseURL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		Channels map[string]mockprovider.ChannelStats `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st.Channels
}

// The bound holds while the delay between a request's departure (WroteRequest) and the provider's arrival
// stamp varies by less than the window guard (need.md N-ORC-012; 25 ms in production, sub-millisecond on a
// Docker bridge). In these in-process tests that delay is goroutine scheduling, which under -race with every
// package running in parallel once exceeded 25 ms (101 in make test). The tests use a 100 ms guard so the
// assumption holds here, and a cold-connection dial longer than the guard, so a limiter that paced admissions
// would still overshoot visibly.
const (
	testJitterGuard = 100 * time.Millisecond
	testDialDelay   = 150 * time.Millisecond
)

// TestDispatcher_ColdConnections_ProviderNeverSeesMoreThanN_RSND2 reproduces the V-013 failure: every new
// connection costs a slow dial (Docker DNS plus connect on a cold pool; 30 ms in V-013), so a request admitted by the
// limiter reaches the provider late and a later one overtakes it. A limiter that paces its own admissions
// let the provider count 127-132 requests in one second at N=95..100; pacing from the departure time must
// hold the line.
func TestDispatcher_ColdConnections_ProviderNeverSeesMoreThanN_RSND2(t *testing.T) {
	const n, users = 100, 330
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, users))

	prov := mockprovider.New(config.ProviderFaults{LatencyMinMS: 20, LatencyMaxMS: 300, RateLimit: n, RetryAfterS: 1, Seed: 7}, metrics.New())
	srv := httptest.NewServer(prov.Handler())
	t.Cleanup(func() { prov.Close(); srv.Close() })
	client := NewHTTPClient(srv.URL, 5*time.Second, WithDialContext(func(ctx context.Context, network, addr string) (net.Conn, error) {
		select {
		case <-time.After(testDialDelay): // models DNS + connect on a cold pool
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}))
	t.Cleanup(client.Close)

	d := fastDispatcher(pool, SMS, client)
	d.RateLimit, d.MaxInFlight, d.WindowGuard = n, 32, testJitterGuard
	startDispatcher(t, d)
	eventually(t, 60*time.Second, "every message terminal", func() bool { return nonTerminal(statusCounts(t, pool, camp, SMS)) == 0 })

	s := providerStats(t, srv.URL)["sms"]
	t.Logf("provider: requests=%d accepted=%d max_requests_1s=%d rejected_by_limiter=%d", s.Requests, s.Accepted, s.MaxRequestsInAny1sWindow, s.RejectedByLimiter)
	if s.Requests < 222 {
		t.Fatalf("only %d requests reached the provider; the test needs a sustained run", s.Requests)
	}
	if s.MaxRequestsInAny1sWindow > n {
		t.Errorf("provider saw %d requests in one second, limit %d", s.MaxRequestsInAny1sWindow, n)
	}
	if s.RejectedByLimiter != 0 {
		t.Errorf("the provider's own limiter rejected %d requests", s.RejectedByLimiter)
	}
	if s.DuplicateRecipientDeliveries != 0 {
		t.Errorf("%d duplicate deliveries", s.DuplicateRecipientDeliveries)
	}
}

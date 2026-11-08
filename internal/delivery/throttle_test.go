package delivery

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"smartcampaign/internal/store/storetest"
)

func TestThrottle_BurstOfInFlight429sIsOneEvent_RSND4(t *testing.T) {
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 12))

	const inflight = 8
	var mu sync.Mutex
	var entered int
	var first429 time.Time
	barrier := make(chan struct{})
	client := &scriptedClient{}
	client.fn = func(n int, r SendRequest) (SendResult, error) {
		if n > inflight {
			return ok200(r)
		}
		mu.Lock()
		entered++
		if entered == inflight {
			close(barrier) // all eight requests are on the wire before any answer comes back
		}
		mu.Unlock()
		<-barrier
		// The answers trickle in over 280 ms, each asking for a (capped) 100 ms pause.
		<-time.After(time.Duration(n-1) * 40 * time.Millisecond)
		mu.Lock()
		if first429.IsZero() {
			first429 = time.Now()
		}
		mu.Unlock()
		return SendResult{Status: 429, Header: http.Header{"Retry-After": {"30"}}}, nil
	}
	d := fastDispatcher(pool, SMS, client)
	d.MaxInFlight = inflight
	startDispatcher(t, d)
	eventually(t, 10*time.Second, "all sent", func() bool { return statusCounts(t, pool, camp, SMS)[StatusSent] == 12 })

	// The first retry may start about one pause after the FIRST 429; if every late answer restarted the pause
	// it could not start before 280 ms + 100 ms.
	mu.Lock()
	t0 := first429
	mu.Unlock()
	var firstRetry time.Time
	for _, c := range client.snapshot() {
		if c.N > inflight && (firstRetry.IsZero() || c.At.Before(firstRetry)) {
			firstRetry = c.At
		}
	}
	if gap := firstRetry.Sub(t0); gap < 90*time.Millisecond || gap > 250*time.Millisecond {
		t.Fatalf("first retry started %v after the first 429, want about one pause (100 ms), not one per answer", gap)
	}
}

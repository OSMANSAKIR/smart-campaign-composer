package delivery

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"smartcampaign/internal/clock"
	"smartcampaign/internal/store/storetest"
)

// A late 429 for a request sent before the current pause began is part of the same overload event: its
// Retry-After is measured from the start of that pause. It may lengthen the pause, never restart or shorten
// it (N-DEL-002, KICKOFF §7.3: the channel waits for the longest Retry-After it was told).
func TestThrottle_InFlightRetryAfterExtendsPause_RSND4(t *testing.T) {
	clk := clock.NewFake(t0)
	d := &Dispatcher{Channel: SMS, Clock: clk}
	d.setup()

	d.pause(time.Second, clk.Now()) // first 429: Retry-After 1 s
	clk.Advance(200 * time.Millisecond)
	sentBefore := t0.Add(-50 * time.Millisecond)

	d.pause(5*time.Second, sentBefore) // an in-flight request answers 429 with Retry-After 5
	if got, want := d.PausedUntil(), t0.Add(5*time.Second); !got.Equal(want) {
		t.Fatalf("pause ends at +%v, want +5s: counted from when the pause began, not from now (+5.2s) and not left at +1s", got.Sub(t0))
	}
	d.pause(2*time.Second, sentBefore) // a shorter one from the same burst
	if got, want := d.PausedUntil(), t0.Add(5*time.Second); !got.Equal(want) {
		t.Fatalf("a shorter late Retry-After moved the pause to +%v", got.Sub(t0))
	}

	// A request sent after the pause began is a new event: it restarts the clock, but still never shortens.
	clk.Advance(200 * time.Millisecond)
	d.pause(time.Second, clk.Now())
	if got, want := d.PausedUntil(), t0.Add(5*time.Second); !got.Equal(want) {
		t.Fatalf("a new 1 s throttle shortened the pause to +%v", got.Sub(t0))
	}
	clk.Advance(5 * time.Second)
	if !d.PausedUntil().IsZero() {
		t.Fatal("pause did not end")
	}
}

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

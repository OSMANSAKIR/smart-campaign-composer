package delivery

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"smartcampaign/internal/clock"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

var t0 = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

// drive advances the fake clock in 1 ms steps whenever something waits on it, until done is closed.
func drive(clk *clock.Fake, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}
		if clk.Waiters() > 0 {
			clk.Advance(time.Millisecond)
		} else {
			runtime.Gosched()
		}
	}
}

func TestLimiter_NeverMoreThanNInAnyWindow_RSND2(t *testing.T) {
	const window = time.Second
	tests := []struct {
		name  string
		n     int
		gaps  []time.Duration // pause before call i (nil = none)
		calls int
	}{
		{name: "burst of 3n at once", n: 5, calls: 15},
		{name: "n=1 strictly spaced", n: 1, calls: 5},
		{name: "n=100 as in production", n: 100, calls: 350},
		{name: "idle gap just before the boundary", n: 4, calls: 12,
			gaps: []time.Duration{0, 0, 0, 0, 999 * time.Millisecond, 0, 0, 0, 0, 1001 * time.Millisecond}},
		{name: "idle gap exactly one window", n: 4, calls: 12,
			gaps: []time.Duration{0, 0, 0, 0, window}},
		{name: "trickle then burst", n: 3, calls: 14,
			gaps: []time.Duration{0, 400 * time.Millisecond, 400 * time.Millisecond, 400 * time.Millisecond, 0, 0, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewFake(t0)
			l := NewSlidingWindowLimiter(tc.n, window, clk)
			times := make([]time.Time, 0, tc.calls)
			done := make(chan struct{})
			go func() {
				defer close(done)
				for i := range tc.calls {
					if i < len(tc.gaps) && tc.gaps[i] > 0 {
						<-clk.After(tc.gaps[i])
					}
					tk, err := l.Acquire(context.Background())
					if err != nil {
						t.Errorf("Acquire: %v", err)
						return
					}
					times = append(times, clk.Now())
					tk.Departed(clk.Now()) // instant departure: the pure sliding-window behaviour
				}
			}()
			drive(clk, done)
			if len(times) != tc.calls {
				t.Fatalf("got %d sends, want %d", len(times), tc.calls)
			}
			// Equivalent to "at most n sends in any half-open window": the send n places later is >= window away.
			for j := tc.n; j < len(times); j++ {
				if gap := times[j].Sub(times[j-tc.n]); gap < window {
					t.Fatalf("send %d at %v is only %v after send %d: more than %d in one window", j, times[j], gap, j-tc.n, tc.n)
				}
			}
			// And it is not needlessly slow: with no idle gaps, throughput is exactly n per window.
			if tc.gaps == nil {
				want := time.Duration((tc.calls-1)/tc.n) * window
				if got := times[len(times)-1].Sub(t0); got != want {
					t.Fatalf("finished after %v, want %v", got, want)
				}
			}
		})
	}
}

func TestLimiter_ConcurrentCallersShareOneWindow_RSND2(t *testing.T) {
	const n, callers = 10, 53
	clk := clock.NewFake(t0)
	l := NewSlidingWindowLimiter(n, time.Second, clk)
	var finished atomic.Int32
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			if tk, err := l.Acquire(context.Background()); err == nil {
				tk.Departed(clk.Now())
				finished.Add(1)
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	drive(clk, done)
	if finished.Load() != callers {
		t.Fatalf("finished %d of %d", finished.Load(), callers)
	}
	// 53 sends at 10 per window need exactly 5 full windows after the first burst.
	// Departures are reported when the goroutine runs, and the driver keeps advancing the fake clock in the
	// meantime, so each wave inherits scheduling lag (6.3 s seen on a loaded CI container). The lower bound is
	// the safety property; the upper bound only catches gross slowness. Exact throughput is asserted by the
	// single-caller test above.
	if got := clk.Now().Sub(t0); got < 5*time.Second || got > 8*time.Second {
		t.Fatalf("elapsed %v, want 5s", got)
	}
}

func TestLimiter_CancelledWaitReturns_RSND2(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewSlidingWindowLimiter(1, time.Second, clk)
	tk, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tk.Departed(clk.Now())
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := l.Acquire(ctx); errc <- err }()
	for clk.Waiters() == 0 {
		runtime.Gosched()
	}
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("Acquire returned nil after cancel")
	}
	if _, err := l.Acquire(ctx); err == nil {
		t.Fatal("Acquire on a cancelled context returned nil")
	}
}

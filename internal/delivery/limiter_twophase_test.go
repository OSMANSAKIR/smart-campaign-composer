package delivery

import (
	"context"
	"runtime"
	"testing"
	"time"

	"smartcampaign/internal/clock"
)

// admitted is what acquireAsync reports: the ticket and the fake time it was admitted at.
type admitted struct {
	tk Ticket
	at time.Time
}

// acquireAsync runs Acquire in a goroutine.
func acquireAsync(ctx context.Context, l *SlidingWindowLimiter, clk *clock.Fake) <-chan admitted {
	out := make(chan admitted, 1)
	go func() {
		tk, err := l.Acquire(ctx)
		if err != nil {
			out <- admitted{}
			return
		}
		out <- admitted{tk, clk.Now()}
	}()
	return out
}

// await advances the fake clock in 1 ms steps while the acquirer sleeps on it, until it is admitted.
func await(t *testing.T, clk *clock.Fake, ch <-chan admitted) admitted {
	t.Helper()
	for range 20_000_000 {
		select {
		case a := <-ch:
			return a
		default:
		}
		if clk.Waiters() > 0 {
			clk.Advance(time.Millisecond)
		} else {
			runtime.Gosched()
		}
	}
	t.Fatal("never admitted")
	return admitted{}
}

// settle lets goroutines that can run, run.
func settle() {
	for range 2000 {
		runtime.Gosched()
	}
}

func TestLimiter_StartsFromDepartureNotAdmission_RSND2(t *testing.T) {
	const w = time.Second + 25*time.Millisecond
	clk := clock.NewFake(t0)
	l := NewSlidingWindowLimiter(2, w, clk)
	tk0 := await(t, clk, acquireAsync(context.Background(), l, clk)).tk
	tk1 := await(t, clk, acquireAsync(context.Background(), l, clk)).tk

	// Slot 0 sat in a slow dial and only left 300 ms after it was admitted.
	third := acquireAsync(context.Background(), l, clk)
	tk0.Departed(t0.Add(300 * time.Millisecond))
	tk1.Departed(t0.Add(10 * time.Millisecond))
	got := await(t, clk, third)
	if want := t0.Add(300*time.Millisecond + w); got.at.Before(want) {
		t.Fatalf("slot 2 admitted at +%v, want >= +%v (departure of slot 0 plus the window)", got.at.Sub(t0), want.Sub(t0))
	}
	if got.at.After(t0.Add(300*time.Millisecond + w + 5*time.Millisecond)) {
		t.Fatalf("slot 2 admitted at +%v, much later than needed", got.at.Sub(t0))
	}
}

func TestLimiter_WaitsForPredecessorsDeparture_RSND2(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewSlidingWindowLimiter(2, time.Second, clk)
	tk0 := await(t, clk, acquireAsync(context.Background(), l, clk)).tk
	tk1 := await(t, clk, acquireAsync(context.Background(), l, clk)).tk
	tk1.Departed(t0)

	third := acquireAsync(context.Background(), l, clk)
	clk.Advance(time.Hour) // however long it takes, slot 0 has not reported
	settle()
	select {
	case <-third:
		t.Fatal("slot 2 started before slot 0 reported its departure")
	default:
	}
	tk0.Departed(clk.Now())
	got := await(t, clk, third)
	if want := t0.Add(time.Hour + time.Second); got.at.Before(want) {
		t.Fatalf("admitted at +%v, want >= +%v", got.at.Sub(t0), want.Sub(t0))
	}
}

func TestLimiter_UsesLatestDepartureOfAllPredecessors_RSND2(t *testing.T) {
	const w = time.Second
	clk := clock.NewFake(t0)
	l := NewSlidingWindowLimiter(2, w, clk)
	tk0 := await(t, clk, acquireAsync(context.Background(), l, clk)).tk
	tk1 := await(t, clk, acquireAsync(context.Background(), l, clk)).tk
	// Slot 1 left first and slot 0 last (500 ms): requests overtake each other on the way out.
	tk1.Departed(t0.Add(100 * time.Millisecond))
	tk0.Departed(t0.Add(500 * time.Millisecond))
	third := await(t, clk, acquireAsync(context.Background(), l, clk)) // slot 2 depends on slot 0
	third.tk.Departed(clk.Now())
	// Slot 3 depends on slots 0 and 1: the later of the two (500 ms), not its direct predecessor (100 ms).
	fourth := await(t, clk, acquireAsync(context.Background(), l, clk))
	want := t0.Add(500*time.Millisecond + w)
	if third.at.Before(want) || fourth.at.Before(want) {
		t.Fatalf("slot 2 at +%v and slot 3 at +%v, both want >= +%v", third.at.Sub(t0), fourth.at.Sub(t0), want.Sub(t0))
	}
	fourth.tk.Departed(clk.Now())
}

func TestLimiter_DeparturesReportedOutOfOrder_RSND2(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewSlidingWindowLimiter(2, time.Second, clk)
	tk0 := await(t, clk, acquireAsync(context.Background(), l, clk)).tk
	tk1 := await(t, clk, acquireAsync(context.Background(), l, clk)).tk
	third := acquireAsync(context.Background(), l, clk)
	tk1.Departed(t0) // slot 1 reports first; slot 2 needs slot 0, so it must keep waiting
	clk.Advance(time.Hour)
	settle()
	select {
	case <-third:
		t.Fatal("slot 2 admitted although slot 0 has not reported")
	default:
	}
	tk0.Departed(clk.Now())
	await(t, clk, third)
}

func TestLimiter_CancelledWaiterDoesNotStallTheSequence_RSND2(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewSlidingWindowLimiter(1, time.Second, clk)
	tk0 := await(t, clk, acquireAsync(context.Background(), l, clk)).tk

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := acquireAsync(ctx, l, clk) // slot 1 waits for slot 0
	settle()
	cancel()
	if a := await(t, clk, cancelled); a.tk != nil {
		t.Fatal("cancelled waiter was admitted")
	}
	tk0.Departed(clk.Now())
	// Slot 2 needs slot 1, which never sent: it counted as departed at its cancellation.
	await(t, clk, acquireAsync(context.Background(), l, clk))
}

func TestLimiter_DepartedTwiceFirstWins_RSND2(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewSlidingWindowLimiter(1, time.Second, clk)
	tk := await(t, clk, acquireAsync(context.Background(), l, clk)).tk
	tk.Departed(t0.Add(100 * time.Millisecond))
	tk.Departed(t0.Add(time.Hour)) // a late duplicate must not move the window
	got := await(t, clk, acquireAsync(context.Background(), l, clk))
	if want := t0.Add(1100 * time.Millisecond); got.at.Before(want) || got.at.After(want.Add(5*time.Millisecond)) {
		t.Fatalf("admitted at +%v, want about +1.1s", got.at.Sub(t0))
	}
}

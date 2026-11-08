package delivery

import (
	"context"
	"sync"
	"time"

	"smartcampaign/internal/clock"
)

// Limiter admits sends. It is two-phase (N-ORC-012): Acquire blocks until a send may start and hands back a
// Ticket; the caller reports when the request actually left the process with Ticket.Departed. The limiter
// paces later sends from those reported departure times, not from the times it admitted them, because the
// provider counts what arrives, and DNS, dial and scheduler delays between "admitted" and "on the wire" let
// a reservation-based limiter put 127-141 requests into one provider-side second (V-013).
type Limiter interface {
	Acquire(ctx context.Context) (Ticket, error)
}

// Ticket is one admitted send. Departed must be called exactly once, with the time the request was fully
// written to the connection (httptrace.ClientTrace.WroteRequest), or with the current time when it never
// left (released, paused, dial failed, cancelled). Later calls are ignored.
type Ticket interface {
	Departed(t time.Time)
}

// SlidingWindowLimiter allows at most N sends in any window, measured on departure.
//
// Admission order is the order of Acquire calls: call number s (a "slot") may start at the earliest
// `window` after the latest departure of any slot i <= s-N. Why that bounds the provider's view: take any
// N+1 requests and sort them by slot, s_0 < ... < s_N. Then s_0 <= s_N-N, so s_N started no earlier than
// departure(s_0) + window, and it departed after it started. Their departures are at least `window` apart;
// on the receiving side that gap shrinks only by the one-way network delay and the provider's decode time,
// which the window guard (1 s + RATE_LIMIT_WINDOW_GUARD_MS) is there to cover. So no N+1 requests ever fall
// into one second at the provider.
//
// A slot waits for every earlier slot up to s-N to report its departure; a stalled dial therefore holds the
// channel back, which is the safe direction. The limiter keeps only a ring of N prefix maxima, so memory
// does not grow with the number of sends.
type SlidingWindowLimiter struct {
	n      int
	window time.Duration
	clk    clock.Clock

	mu       sync.Mutex
	next     int               // sequence number of the next Acquire
	frontier int               // every slot < frontier has reported its departure
	runMax   time.Time         // latest departure among slots < frontier
	pm       []time.Time       // pm[j%n] = latest departure among slots <= j, valid for j in [frontier-n, frontier)
	pending  map[int]time.Time // departures of slots >= frontier that reported before their predecessors
	changed  chan struct{}     // closed and replaced whenever frontier advances
}

// NewSlidingWindowLimiter returns a limiter allowing n sends per window. n < 1 is treated as 1.
func NewSlidingWindowLimiter(n int, window time.Duration, clk clock.Clock) *SlidingWindowLimiter {
	n = max(n, 1)
	return &SlidingWindowLimiter{
		n: n, window: window, clk: clk,
		pm: make([]time.Time, n), pending: map[int]time.Time{}, changed: make(chan struct{}),
	}
}

// Acquire blocks until the caller may start a send, or ctx ends. A caller whose ctx ends has still used its
// slot: it is reported as departed now, which keeps the slot sequence moving and only ever delays others.
func (l *SlidingWindowLimiter) Acquire(ctx context.Context) (Ticket, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	s := l.next
	l.next++
	tk := &ticket{l: l, seq: s}
	if s < l.n { // the first N slots have no predecessor to wait for
		l.mu.Unlock()
		return tk, nil
	}
	need := s - l.n
	for l.frontier <= need {
		ch := l.changed
		l.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			tk.Departed(l.clk.Now())
			return nil, ctx.Err()
		}
		l.mu.Lock()
	}
	// Read the prefix maximum now: slot `need` is overwritten in the ring only after this slot has departed.
	startAt := l.pm[need%l.n].Add(l.window)
	l.mu.Unlock()

	if d := startAt.Sub(l.clk.Now()); d > 0 {
		select {
		case <-l.clk.After(d):
		case <-ctx.Done():
			tk.Departed(l.clk.Now())
			return nil, ctx.Err()
		}
	}
	return tk, nil
}

// depart records slot seq's departure and advances the contiguous frontier as far as it can.
func (l *SlidingWindowLimiter) depart(seq int, t time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending[seq] = t
	advanced := false
	for {
		dt, ok := l.pending[l.frontier]
		if !ok {
			break
		}
		delete(l.pending, l.frontier)
		if dt.After(l.runMax) {
			l.runMax = dt
		}
		l.pm[l.frontier%l.n] = l.runMax
		l.frontier++
		advanced = true
	}
	if advanced {
		close(l.changed)
		l.changed = make(chan struct{})
	}
}

type ticket struct {
	l    *SlidingWindowLimiter
	seq  int
	once sync.Once
}

func (t *ticket) Departed(at time.Time) { t.once.Do(func() { t.l.depart(t.seq, at) }) }

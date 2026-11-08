package delivery

import (
	"context"
	"sync"
	"time"

	"smartcampaign/internal/clock"
)

// Limiter blocks until one more send is allowed. The sliding-window log guarantees at most N sends in any
// 1-second window, which a token bucket with burst does not (KICKOFF §7.2).
type Limiter interface {
	Wait(ctx context.Context) error
}

// SlidingWindowLimiter keeps the send times of the last N sends in a ring buffer. A new send may happen at
// the earliest `window` after the send N places back, so no half-open interval [t, t+window) ever contains
// more than N sends.
//
// Wait reserves a slot under the lock and then sleeps until the slot's time. Reserving (instead of looping on
// "is there room yet?") queues waiters in order, avoids a thundering herd when the window opens, and keeps
// the guarantee exact even with many concurrent callers. A caller cancelled while waiting still consumes its
// slot; that is conservative (never over the limit) and rare.
type SlidingWindowLimiter struct {
	n      int
	window time.Duration
	clk    clock.Clock

	mu   sync.Mutex
	ring []time.Time // reserved send times; ring[head] is the oldest of the last n once the ring is full
	head int
	size int
}

// NewSlidingWindowLimiter returns a limiter allowing n sends per window. n < 1 is treated as 1.
func NewSlidingWindowLimiter(n int, window time.Duration, clk clock.Clock) *SlidingWindowLimiter {
	n = max(n, 1)
	return &SlidingWindowLimiter{n: n, window: window, clk: clk, ring: make([]time.Time, n)}
}

// Wait blocks until the caller may send, or ctx ends.
func (l *SlidingWindowLimiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	at := l.reserve()
	d := at.Sub(l.clk.Now())
	if d <= 0 {
		return nil
	}
	select {
	case <-l.clk.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *SlidingWindowLimiter) reserve() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	at := l.clk.Now()
	if l.size < l.n {
		l.ring[(l.head+l.size)%l.n] = at
		l.size++
		return at
	}
	// The oldest of the last n sends decides: the next one is allowed `window` after it.
	if earliest := l.ring[l.head].Add(l.window); earliest.After(at) {
		at = earliest
	}
	l.ring[l.head] = at
	l.head = (l.head + 1) % l.n
	return at
}

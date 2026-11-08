// Package delivery sends queued messages: per-channel dispatcher with an advisory lock, SKIP LOCKED claims
// with leases, an exact sliding-window rate limiter, the retry policy and the provider client (KICKOFF §7).
// Owned by the delivery builder.
//
// Phase 0 skeleton: types and signatures are the contract; bodies return ErrNotImplemented.
package delivery

import (
	"context"
	"errors"
	"net/http"
	"time"

	"smartcampaign/internal/clock"
)

// ErrNotImplemented marks skeleton functions.
var ErrNotImplemented = errors.New("delivery: not implemented")

// Channel is sms or email.
type Channel string

const (
	SMS   Channel = "sms"
	Email Channel = "email"
)

// Limiter blocks until one more send is allowed. The sliding-window log guarantees at most N sends in any
// 1-second window, which a token bucket with burst does not (KICKOFF §7.2).
type Limiter interface {
	Wait(ctx context.Context) error
}

// SlidingWindowLimiter keeps the timestamps of the last N sends in a ring buffer.
type SlidingWindowLimiter struct {
	n      int
	window time.Duration
	clk    clock.Clock
}

// NewSlidingWindowLimiter returns a limiter allowing n sends per window.
func NewSlidingWindowLimiter(n int, window time.Duration, clk clock.Clock) *SlidingWindowLimiter {
	return &SlidingWindowLimiter{n: n, window: window, clk: clk}
}

// Wait is not implemented yet.
func (l *SlidingWindowLimiter) Wait(ctx context.Context) error { return ErrNotImplemented }

// Class is a provider outcome class (KICKOFF §7.3).
type Class string

const (
	ClassSuccess   Class = "success"
	ClassPermanent Class = "permanent"
	ClassConfig    Class = "config"
	ClassThrottled Class = "throttled"
	ClassTransient Class = "transient"
	ClassAmbiguous Class = "ambiguous"
)

// Decision is what the dispatcher does with one outcome.
type Decision struct {
	Class         Class
	Retry         bool
	RetryAfter    time.Duration // for throttled: pause the whole channel this long
	CountsAttempt bool
}

// RetryPolicy holds the retry parameters.
type RetryPolicy struct {
	BaseBackoff       time.Duration // 1s
	MaxBackoff        time.Duration // 60s
	MaxAttempts       int           // 5 (transient + ambiguous)
	MaxThrottles      int           // 20
	DefaultRetryAfter time.Duration // 1s
	MaxRetryAfter     time.Duration // 30s
}

// DefaultRetryPolicy is the policy from KICKOFF §7.3.
var DefaultRetryPolicy = RetryPolicy{BaseBackoff: time.Second, MaxBackoff: 60 * time.Second, MaxAttempts: 5,
	MaxThrottles: 20, DefaultRetryAfter: time.Second, MaxRetryAfter: 30 * time.Second}

// Classify maps a provider result (status 0 + err for timeouts/connection errors) to a decision.
func (p RetryPolicy) Classify(status int, header http.Header, err error) Decision {
	return Decision{Class: ClassTransient}
}

// Backoff returns the full-jitter delay for the given attempt; rnd returns a value in [0, 1).
func (p RetryPolicy) Backoff(attempt int, rnd func() float64) time.Duration { return p.BaseBackoff }

// SendRequest is one provider call. IdempotencyKey is always the message id.
type SendRequest struct {
	Channel        Channel
	IdempotencyKey string
	To             string
	Text           string // sms text, or email plain text
	Subject        string // email only
	HTML           string // email only
	CampaignID     string
	UserID         int64
	MessageID      string
}

// SendResult is the provider's answer; Status is 0 when no HTTP response arrived.
type SendResult struct {
	Status            int
	Header            http.Header
	ProviderMessageID string
	Latency           time.Duration
}

// ProviderClient sends one message.
type ProviderClient interface {
	Send(ctx context.Context, req SendRequest) (SendResult, error)
}

// Dispatcher runs the send loop for one channel. Skeleton.
type Dispatcher struct {
	Channel Channel
}

// Run blocks until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

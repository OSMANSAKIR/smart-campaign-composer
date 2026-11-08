package delivery

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Class is a provider outcome class (KICKOFF §7.3).
type Class string

const (
	ClassSuccess   Class = "success"
	ClassPermanent Class = "permanent"
	ClassConfig    Class = "config"
	ClassThrottled Class = "throttled"
	ClassTransient Class = "transient"
	ClassAmbiguous Class = "ambiguous"
	// ClassUnreachable: no connection to the provider was ever made (refused, DNS, dial timeout), so nothing
	// was sent. Like a configuration problem it pauses the channel and uses no attempt (need.md N-DEL-007).
	ClassUnreachable Class = "unreachable"
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
	if errors.Is(err, ErrUnreachable) {
		return Decision{Class: ClassUnreachable, Retry: true}
	}
	if err != nil || status == 0 {
		// No answer: the provider may or may not have delivered. Retrying with the same Idempotency-Key is safe.
		return Decision{Class: ClassAmbiguous, Retry: true, CountsAttempt: true}
	}
	switch {
	case status >= 200 && status < 300:
		return Decision{Class: ClassSuccess}
	case status == http.StatusTooManyRequests:
		return Decision{Class: ClassThrottled, Retry: true, RetryAfter: p.retryAfter(header)}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return Decision{Class: ClassConfig, Retry: true}
	case status == http.StatusRequestTimeout || status >= 500:
		return Decision{Class: ClassTransient, Retry: true, CountsAttempt: true}
	default:
		// 400, 404, 422 and any other unexpected 1xx/3xx/4xx: retrying the same request cannot help.
		return Decision{Class: ClassPermanent}
	}
}

// retryAfter parses Retry-After (delta-seconds or HTTP-date); missing, garbage or non-positive values give
// the default, and the result is capped so a hostile header cannot stall the channel.
func (p RetryPolicy) retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	d := p.DefaultRetryAfter
	if v != "" {
		if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 && !math.IsInf(secs, 0) {
			d = time.Duration(min(secs, 1e6) * float64(time.Second))
		} else if t, err := http.ParseTime(v); err == nil {
			if until := time.Until(t); until > 0 {
				d = until
			}
		}
	}
	return min(d, p.MaxRetryAfter)
}

// Backoff returns the full-jitter delay before retry number `attempt` (1-based): uniform in
// [0, min(MaxBackoff, BaseBackoff·2^(attempt-1))). rnd returns a value in [0, 1).
func (p RetryPolicy) Backoff(attempt int, rnd func() float64) time.Duration {
	ceiling := p.BaseBackoff
	for i := 1; i < attempt && ceiling < p.MaxBackoff; i++ {
		ceiling *= 2
	}
	ceiling = min(ceiling, p.MaxBackoff)
	return time.Duration(rnd() * float64(ceiling))
}

// Exhausted reports whether the retry budget is used up. attempts is the number of counted attempts including
// the one that just failed; throttles includes the one that just happened.
func (p RetryPolicy) Exhausted(d Decision, attempts, throttles int) bool {
	switch d.Class {
	case ClassTransient, ClassAmbiguous:
		return attempts >= p.MaxAttempts
	case ClassThrottled:
		return throttles > p.MaxThrottles
	}
	return false
}

// ErrTimeout marks a provider request that hit the client timeout (no answer arrived).
var ErrTimeout = errors.New("provider request timed out")

// ErrUnreachable wraps a send error when no connection to the provider was obtained, i.e. not one byte of the
// request can have reached it. Only the HTTP client can know this (httptrace GotConn), so it sets it.
var ErrUnreachable = errors.New("provider unreachable, nothing sent")

type timeouter interface{ Timeout() bool }

// IsTimeout reports whether err is a request timeout rather than another connection problem.
func IsTimeout(err error) bool {
	var t timeouter
	return errors.Is(err, ErrTimeout) || errors.As(err, &t) && t.Timeout()
}

// ResponseCode is the provider_responses_total `code` label for one outcome.
func ResponseCode(status int, err error) string {
	switch {
	case errors.Is(err, ErrUnreachable):
		return "unreachable"
	case err != nil && IsTimeout(err):
		return "timeout"
	case err != nil || status == 0:
		return "conn_error"
	default:
		return strconv.Itoa(status)
	}
}

// errDescription is the short last_error text. It never contains message content or recipient data.
func errDescription(status int, err error) string {
	switch c := ResponseCode(status, err); c {
	case "timeout", "conn_error", "unreachable":
		return c
	default:
		return "http_" + c
	}
}

package delivery

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestClassify_Table_RSND4(t *testing.T) {
	p := DefaultRetryPolicy
	hdr := func(v string) http.Header {
		h := http.Header{}
		if v != "" {
			h.Set("Retry-After", v)
		}
		return h
	}
	timeoutErr := &net.DNSError{IsTimeout: true}
	tests := []struct {
		name   string
		status int
		header http.Header
		err    error
		want   Decision
	}{
		{"200", 200, nil, nil, Decision{Class: ClassSuccess}},
		{"202", 202, nil, nil, Decision{Class: ClassSuccess}},
		{"400 permanent", 400, nil, nil, Decision{Class: ClassPermanent}},
		{"404 permanent", 404, nil, nil, Decision{Class: ClassPermanent}},
		{"422 permanent", 422, nil, nil, Decision{Class: ClassPermanent}},
		{"409 other 4xx permanent", 409, nil, nil, Decision{Class: ClassPermanent}},
		{"401 config", 401, nil, nil, Decision{Class: ClassConfig, Retry: true}},
		{"403 config", 403, nil, nil, Decision{Class: ClassConfig, Retry: true}},
		{"429 default retry-after", 429, hdr(""), nil, Decision{Class: ClassThrottled, Retry: true, RetryAfter: time.Second}},
		{"429 retry-after 3", 429, hdr("3"), nil, Decision{Class: ClassThrottled, Retry: true, RetryAfter: 3 * time.Second}},
		{"429 retry-after capped at 30s", 429, hdr("3600"), nil, Decision{Class: ClassThrottled, Retry: true, RetryAfter: 30 * time.Second}},
		{"429 retry-after zero uses default", 429, hdr("0"), nil, Decision{Class: ClassThrottled, Retry: true, RetryAfter: time.Second}},
		{"429 retry-after negative uses default", 429, hdr("-5"), nil, Decision{Class: ClassThrottled, Retry: true, RetryAfter: time.Second}},
		{"429 retry-after garbage uses default", 429, hdr("soon"), nil, Decision{Class: ClassThrottled, Retry: true, RetryAfter: time.Second}},
		{"429 retry-after NaN uses default", 429, hdr("NaN"), nil, Decision{Class: ClassThrottled, Retry: true, RetryAfter: time.Second}},
		{"500 transient", 500, nil, nil, Decision{Class: ClassTransient, Retry: true, CountsAttempt: true}},
		{"502 transient", 502, nil, nil, Decision{Class: ClassTransient, Retry: true, CountsAttempt: true}},
		{"503 transient", 503, nil, nil, Decision{Class: ClassTransient, Retry: true, CountsAttempt: true}},
		{"408 transient", 408, nil, nil, Decision{Class: ClassTransient, Retry: true, CountsAttempt: true}},
		{"timeout ambiguous", 0, nil, context.DeadlineExceeded, Decision{Class: ClassAmbiguous, Retry: true, CountsAttempt: true}},
		{"net timeout ambiguous", 0, nil, timeoutErr, Decision{Class: ClassAmbiguous, Retry: true, CountsAttempt: true}},
		{"connection error ambiguous", 0, nil, errors.New("connection refused"), Decision{Class: ClassAmbiguous, Retry: true, CountsAttempt: true}},
		{"no status no error ambiguous", 0, nil, nil, Decision{Class: ClassAmbiguous, Retry: true, CountsAttempt: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Classify(tc.status, tc.header, tc.err); got != tc.want {
				t.Fatalf("Classify(%d) = %+v, want %+v", tc.status, got, tc.want)
			}
		})
	}
}

func TestClassify_RetryAfterHTTPDate_RSND4(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(10*time.Second).UTC().Format(http.TimeFormat))
	d := DefaultRetryPolicy.Classify(429, h, nil)
	if d.RetryAfter < 8*time.Second || d.RetryAfter > 10*time.Second {
		t.Fatalf("RetryAfter = %v, want about 10s", d.RetryAfter)
	}
}

func TestBackoff_FullJitterBounds_RSND4(t *testing.T) {
	p := DefaultRetryPolicy
	// Ceiling per attempt: base 1s, doubling, capped at 60s.
	ceilings := map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second,
		5: 16 * time.Second, 6: 32 * time.Second, 7: 60 * time.Second, 8: 60 * time.Second, 50: 60 * time.Second}
	for attempt, ceiling := range ceilings {
		if got := p.Backoff(attempt, func() float64 { return 0 }); got != 0 {
			t.Errorf("attempt %d, rnd=0: %v, want 0 (full jitter reaches zero)", attempt, got)
		}
		upper := p.Backoff(attempt, func() float64 { return 0.9999999 })
		if upper >= ceiling || upper < ceiling-time.Millisecond {
			t.Errorf("attempt %d, rnd~1: %v, want just below %v", attempt, upper, ceiling)
		}
		if mid := p.Backoff(attempt, func() float64 { return 0.5 }); mid != ceiling/2 {
			t.Errorf("attempt %d, rnd=0.5: %v, want %v", attempt, mid, ceiling/2)
		}
	}
}

func TestExhaustedAttempts_Policy_RSND4(t *testing.T) {
	p := DefaultRetryPolicy
	tr := Decision{Class: ClassTransient}
	th := Decision{Class: ClassThrottled}
	tests := []struct {
		name               string
		d                  Decision
		attempts, throttle int
		want               bool
	}{
		{"transient 4th attempt retries", tr, 4, 0, false},
		{"transient 5th attempt is the last", tr, 5, 0, true},
		{"ambiguous 5th attempt is the last", Decision{Class: ClassAmbiguous}, 5, 0, true},
		{"throttles never use attempts", th, 99, 20, false},
		{"21st throttle fails", th, 1, 21, true},
		{"success never exhausted", Decision{Class: ClassSuccess}, 9, 99, false},
	}
	for _, tc := range tests {
		if got := p.Exhausted(tc.d, tc.attempts, tc.throttle); got != tc.want {
			t.Errorf("%s: Exhausted = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestResponseCode_ROBS2(t *testing.T) {
	if got := ResponseCode(503, nil); got != "503" {
		t.Errorf("got %q", got)
	}
	if got := ResponseCode(0, context.DeadlineExceeded); got != "timeout" {
		t.Errorf("got %q", got)
	}
	if got := ResponseCode(0, errors.New("EOF")); got != "conn_error" {
		t.Errorf("got %q", got)
	}
}

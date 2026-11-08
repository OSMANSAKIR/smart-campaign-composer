package mockprovider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/clock"
	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
)

// The provider is the referee of the rate bound (R-SND-2), so its own measurement must not be disturbed by
// what the provider itself does: arrivals are stamped on arrival, and stats endpoints never hold the lock
// while writing to a client.

var m0 = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func decodeJSON(t *testing.T, r io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestWindow_LateOlderEventCountsInLaterWindows_RRES3(t *testing.T) {
	var w window
	w.add(m0.Add(500 * time.Millisecond))
	if got := w.add(m0.Add(1400 * time.Millisecond)); got != 2 {
		t.Fatalf("max = %d, want 2", got)
	}
	// An event that arrived at 1000 ms but was recorded after the one at 1400 ms: the window ending at 1400
	// now holds 500, 1000 and 1400.
	if got := w.add(m0.Add(1000 * time.Millisecond)); got != 3 {
		t.Fatalf("max after the out-of-order event = %d, want 3", got)
	}
	if got := w.countAt(m0.Add(1400 * time.Millisecond)); got != 3 {
		t.Fatalf("countAt(1400 ms) = %d, want 3", got)
	}
	// Exactly one second apart is outside the half-open window.
	if got := w.countAt(m0.Add(1500 * time.Millisecond)); got != 2 {
		t.Fatalf("countAt(1500 ms) = %d, want 2 (the event at 500 ms has left)", got)
	}
}

// gatedBody blocks its first Read until released and announces that the handler has started reading.
type gatedBody struct {
	r       io.Reader
	reading chan struct{}
	release chan struct{}
	once    bool
}

func (b *gatedBody) Read(p []byte) (int, error) {
	if !b.once {
		b.once = true
		close(b.reading)
		<-b.release
	}
	return b.r.Read(p)
}
func (b *gatedBody) Close() error { return nil }

func TestSend_ArrivalStampedBeforeBodyDecode_RRES3(t *testing.T) {
	clk := clock.NewFake(m0)
	s := New(config.ProviderFaults{Seed: 1}, metrics.New(), WithClock(clk))
	h := s.Handler()

	body := &gatedBody{r: strings.NewReader(smsBody("c", 1)), reading: make(chan struct{}), release: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPost, "/v1/sms", body)
	req.Header.Set("Idempotency-Key", "k1")
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), req) }()

	<-body.reading                       // the handler is decoding: its arrival has been stamped by now
	clk.Advance(1500 * time.Millisecond) // the decode (or the lock) takes a long time
	close(body.release)
	<-done

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats/delivered", nil))
	var list struct {
		Items []Delivery `json:"items"`
	}
	decodeJSON(t, rec.Body, &list)
	if len(list.Items) != 1 || !list.Items[0].At.Equal(m0) {
		t.Fatalf("delivered %+v, want one delivery stamped %v (its arrival), not the later time it was processed", list.Items, m0)
	}
}

// stalledWriter blocks in Write until released, like a client that has stopped reading.
type stalledWriter struct {
	hdr     http.Header
	writing chan struct{}
	release chan struct{}
	once    bool
}

func (w *stalledWriter) Header() http.Header { return w.hdr }
func (w *stalledWriter) WriteHeader(int)     {}
func (w *stalledWriter) Write(p []byte) (int, error) {
	if !w.once {
		w.once = true
		close(w.writing)
		<-w.release
	}
	return len(p), nil
}

// A stats reader that stops reading in the middle of the response must not hold up the send path or make
// evenly spaced arrivals look bunched. 10 requests 100 ms apart are at most 10 in any second.
func TestStats_PollerStalledMidWriteDoesNotRaiseMax_RRES3(t *testing.T) {
	for _, path := range []string{"/stats/delivered", "/stats"} {
		t.Run(path, func(t *testing.T) {
			clk := clock.NewFake(m0)
			s := New(config.ProviderFaults{Seed: 1}, metrics.New(), WithClock(clk))
			h := s.Handler()
			send := func(i int) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/v1/sms", strings.NewReader(smsBody("c", i)))
				req.Header.Set("Idempotency-Key", "k"+string(rune('a'+i)))
				h.ServeHTTP(rec, req)
				if rec.Code != 200 {
					t.Errorf("request %d: status %d", i, rec.Code)
				}
			}
			send(0) // something to list

			sw := &stalledWriter{hdr: http.Header{}, writing: make(chan struct{}), release: make(chan struct{})}
			pollDone := make(chan struct{})
			go func() { defer close(pollDone); h.ServeHTTP(sw, httptest.NewRequest(http.MethodGet, path, nil)) }()
			<-sw.writing // the poller is stuck inside Write

			for i := 1; i < 10; i++ {
				clk.Advance(100 * time.Millisecond)
				done := make(chan struct{})
				go func() { defer close(done); send(i) }()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					close(sw.release)
					t.Fatalf("request %d is blocked behind a stalled %s reader", i, path)
				}
			}
			close(sw.release)
			<-pollDone

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", nil))
			var st struct {
				Channels map[string]ChannelStats `json:"channels"`
			}
			decodeJSON(t, rec.Body, &st)
			if got := st.Channels["sms"].MaxRequestsInAny1sWindow; got != 10 {
				t.Fatalf("max requests in any 1 s window = %d, want 10", got)
			}
		})
	}
}

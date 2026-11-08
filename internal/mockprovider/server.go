// Package mockprovider is the fake SMS/email provider (docs/contracts/provider-api.md). It lives in
// internal/ so tests can run it in-process with httptest. Owned by the delivery builder.
//
// It injects latency, 5xx, 429 and hangs from a seeded RNG, enforces its own per-channel rate limit, stores
// 200/400/422 answers per Idempotency-Key, and measures independently what it received (stats) so the
// delivery invariants I1-I3 can be proven from the receiving side.
package mockprovider

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"smartcampaign/internal/clock"
	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
)

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// maxHang bounds a hung request when neither the client nor the server goes away first.
const maxHang = 5 * time.Minute

// Channels the provider serves.
var channels = []string{"sms", "email"}

// Server holds the in-memory provider state.
type Server struct {
	clk clock.Clock
	m   *metrics.Metrics

	mu      sync.Mutex
	cfg     config.ProviderFaults
	rng     *rand.Rand
	since   time.Time
	replies map[string]stored // Idempotency-Key -> stored 200/400/422 answer
	ch      map[string]*chanState

	done      chan struct{}
	closeOnce sync.Once
}

type stored struct {
	status int
	body   []byte
}

// Delivery is one accepted message, listed by GET /stats/delivered for the consent cross-check (I2).
type Delivery struct {
	Channel    string    `json:"channel"`
	CampaignID string    `json:"campaign_id"`
	UserID     int64     `json:"user_id"`
	MessageID  string    `json:"message_id"`
	At         time.Time `json:"at"`
}

// ChannelStats is one channel's block in GET /stats.
type ChannelStats struct {
	Requests                     int            `json:"requests"`
	Accepted                     int            `json:"accepted"`
	UniqueKeys                   int            `json:"unique_keys"`
	ReplayedKeys                 int            `json:"replayed_keys"`
	DuplicateRecipientDeliveries int            `json:"duplicate_recipient_deliveries"`
	MaxRequestsInAny1sWindow     int            `json:"max_requests_in_any_1s_window"`
	MaxDeliveriesInAny1sWindow   int            `json:"max_deliveries_in_any_1s_window"`
	RejectedByLimiter            int            `json:"rejected_by_limiter"`
	Hangs                        int            `json:"hangs"`
	StatusCounts                 map[string]int `json:"status_counts"`
}

type chanState struct {
	ChannelStats
	arrivals   window         // every request, for max_requests_in_any_1s_window
	admitted   window         // requests the own limiter let through
	deliveries window         // accepted deliveries
	seen       map[string]int // "campaign/user" -> accepted deliveries
	delivered  []Delivery
}

// window is a sliding one-second log of event times, kept sorted. An event at t is inside the window ending at
// `now` while t > now-1s, matching the half-open interval the client-side limiter guarantees.
//
// Events carry the time the request arrived, which is stamped before the body is decoded and before the
// server lock is taken, so goroutines can add them slightly out of order. add therefore inserts in order and
// re-checks every window the new event falls into, and prune keeps one extra second so a late older event
// still finds its neighbours.
type window struct {
	ts   []time.Time
	head int
	max  int
}

const windowSlack = time.Second

func (w *window) prune(newest time.Time) {
	cut := newest.Add(-time.Second - windowSlack)
	for w.head < len(w.ts) && !w.ts[w.head].After(cut) {
		w.head++
	}
	if w.head > 1024 && w.head*2 > len(w.ts) {
		w.ts = append(w.ts[:0], w.ts[w.head:]...)
		w.head = 0
	}
}

// firstAfter returns the index of the first stored event later than t (len when none).
func (w *window) firstAfter(t time.Time) int {
	i := sort.Search(len(w.ts)-w.head, func(i int) bool { return w.ts[w.head+i].After(t) })
	return w.head + i
}

// countAt is the number of events in the window ending at t.
func (w *window) countAt(t time.Time) int {
	return w.firstAfter(t) - w.firstAfter(t.Add(-time.Second))
}

// add records an event and returns the running maximum over all windows ending at an event.
func (w *window) add(t time.Time) int {
	if n := len(w.ts); n > w.head {
		w.prune(maxTime(t, w.ts[n-1]))
	}
	p := len(w.ts)
	for p > w.head && w.ts[p-1].After(t) {
		p--
	}
	w.ts = append(w.ts, time.Time{})
	copy(w.ts[p+1:], w.ts[p:])
	w.ts[p] = t
	// The new event lies in the window ending at each later event within a second of it, and in its own.
	for j := p; j < len(w.ts) && w.ts[j].Before(t.Add(time.Second)); j++ {
		w.max = max(w.max, j+1-w.firstAfter(w.ts[j].Add(-time.Second)))
	}
	return w.max
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func newChanState() *chanState {
	return &chanState{ChannelStats: ChannelStats{StatusCounts: map[string]int{}}, seen: map[string]int{}}
}

// Option customizes a Server.
type Option func(*Server)

// WithClock replaces the wall clock (tests).
func WithClock(c clock.Clock) Option { return func(s *Server) { s.clk = c } }

// New returns a provider with the given fault configuration.
func New(cfg config.ProviderFaults, m *metrics.Metrics, opts ...Option) *Server {
	s := &Server{cfg: cfg, m: m, clk: clock.Real{}, done: make(chan struct{})}
	for _, o := range opts {
		o(s)
	}
	s.reset()
	s.reseed()
	for _, c := range channels { // known label values appear at zero
		for _, code := range []string{"200", "400", "422", "429", "500", "502", "503", "hang"} {
			s.m.ProviderRequests.WithLabelValues(c, code)
		}
		s.m.ProviderDuplicates.WithLabelValues(c)
	}
	return s
}

// Close releases hung requests so test servers can shut down.
func (s *Server) Close() { s.closeOnce.Do(func() { close(s.done) }) }

// reseed builds the fault RNG; seed 0 means a random seed. Caller holds mu or is constructing.
func (s *Server) reseed() {
	seed := s.cfg.Seed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	s.rng = rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

func (s *Server) reset() {
	s.since = s.clk.Now().UTC()
	s.replies = map[string]stored{}
	s.ch = map[string]*chanState{}
	for _, c := range channels {
		s.ch[c] = newChanState()
	}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sms", s.send("sms"))
	mux.HandleFunc("POST /v1/email", s.send("email"))
	mux.HandleFunc("GET /stats", s.getStats)
	mux.HandleFunc("GET /stats/delivered", s.getDelivered)
	mux.HandleFunc("GET /admin/config", s.getConfig)
	mux.HandleFunc("POST /admin/config", s.postConfig)
	mux.HandleFunc("POST /admin/reset", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reset()
		if s.cfg.Seed != 0 { // a fixed seed restarts its sequence, so a test run is repeatable
			s.reseed()
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	})
	ok := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	mux.HandleFunc("GET /healthz", ok)
	mux.HandleFunc("GET /readyz", ok)
	mux.Handle("GET /metrics", s.m.Handler())
	return mux
}

type sendBody struct {
	To         string `json:"to"`
	Text       string `json:"text"`
	Subject    string `json:"subject"`
	HTML       string `json:"html"`
	CampaignID string `json:"campaign_id"`
	UserID     int64  `json:"user_id"`
	MessageID  string `json:"message_id"`
}

// outcome is what one request gets, decided under the lock and executed outside it.
type outcome struct {
	status     int
	body       []byte
	retryAfter int // seconds, for 429
	delay      time.Duration
	hang       bool
}

func (s *Server) send(channel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The arrival time is taken first, before the body is decoded and before the server lock is waited
		// for: the rate-limit measurement must see when the request came in, not when this goroutine got
		// round to it (a stalled lock holder would otherwise bunch up requests that arrived evenly spread).
		arrived := s.clk.Now()
		key := r.Header.Get("Idempotency-Key")
		var b sendBody
		decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b)

		out := s.decide(channel, key, b, decodeErr, arrived)
		if out.hang {
			select {
			case <-r.Context().Done():
			case <-s.done:
			case <-s.clk.After(maxHang):
			}
			return
		}
		if out.delay > 0 {
			select {
			case <-s.clk.After(out.delay):
			case <-r.Context().Done():
				return
			case <-s.done:
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if out.status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", strconv.Itoa(out.retryAfter))
		}
		w.WriteHeader(out.status)
		_, _ = w.Write(out.body)
	}
}

// decide runs the whole provider decision for one request under the lock; now is its arrival time: arrival accounting, own limiter,
// fault roll, idempotency, validation and the delivery itself. The order matters:
//
//  1. arrival is counted before anything can reject it, so a non-compliant client shows up in
//     max_requests_in_any_1s_window;
//  2. the own limiter looks only at requests it admitted, so rejected arrivals do not lock a client out;
//  3. faults precede the idempotency lookup, so even a replay can time out (a retry of a retry);
//  4. half of all hangs happen after the delivery was recorded (provider-api.md §2).
func (s *Server) decide(channel, key string, b sendBody, decodeErr error, now time.Time) outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.ch[channel]
	st.Requests++
	st.MaxRequestsInAny1sWindow = st.arrivals.add(now)

	reply := func(status int, body any) outcome {
		raw, _ := json.Marshal(body)
		return s.finish(channel, outcome{status: status, body: raw})
	}

	if limit := s.cfg.RateLimit; limit > 0 {
		if st.admitted.countAt(now) >= limit {
			st.RejectedByLimiter++
			o := reply(http.StatusTooManyRequests, errBody("rate_limited"))
			o.retryAfter = s.cfg.RetryAfterS
			return o
		}
	}
	st.admitted.add(now)

	if key == "" {
		return reply(http.StatusBadRequest, errBody("missing_idempotency_key"))
	}

	// Fixed number of draws per request keeps a seeded run reproducible whichever branch is taken.
	roll, hangHalf, latU, spikeU := s.rng.Float64(), s.rng.Float64(), s.rng.Float64(), s.rng.Float64()
	serverErr := []int{500, 502, 503}[s.rng.IntN(3)]
	delay := s.latency(latU, spikeU)
	c := s.cfg
	hang := roll < c.HangRate
	hangAfter := hang && hangHalf < 0.5
	switch {
	case hang && !hangAfter:
		st.Hangs++
		s.m.ProviderRequests.WithLabelValues(channel, "hang").Inc()
		return outcome{hang: true}
	case !hang && roll < c.HangRate+c.Rate429:
		o := reply(http.StatusTooManyRequests, errBody("rate_limited"))
		o.retryAfter, o.delay = c.RetryAfterS, delay
		return o
	case !hang && roll < c.HangRate+c.Rate429+c.ErrorRate:
		o := reply(serverErr, errBody("server_error"))
		o.delay = delay
		return o
	}

	var o outcome
	if prev, ok := s.replies[key]; ok {
		st.ReplayedKeys++
		o = s.finish(channel, outcome{status: prev.status, body: prev.body})
	} else {
		st.UniqueKeys++
		o = s.process(channel, key, b, decodeErr, now)
	}
	if hangAfter { // delivery (if any) is recorded; the answer never arrives
		st.Hangs++
		s.m.ProviderRequests.WithLabelValues(channel, "hang").Inc()
		return outcome{hang: true}
	}
	o.delay = delay
	return o
}

// process validates and, when valid, records one new delivery. Caller holds mu.
func (s *Server) process(channel, key string, b sendBody, decodeErr error, now time.Time) outcome {
	st := s.ch[channel]
	store := func(status int, body any) outcome {
		raw, _ := json.Marshal(body)
		s.replies[key] = stored{status, raw}
		return s.finish(channel, outcome{status: status, body: raw})
	}
	switch {
	case decodeErr != nil:
		return store(http.StatusBadRequest, errBody("bad_request"))
	case channel == "sms" && !e164.MatchString(b.To), channel == "email" && !validEmail(b.To):
		return store(http.StatusBadRequest, errBody("invalid_recipient"))
	case channel == "sms" && strings.TrimSpace(b.Text) == "",
		channel == "email" && (strings.TrimSpace(b.Subject) == "" || strings.TrimSpace(b.Text) == "" && strings.TrimSpace(b.HTML) == ""):
		return store(http.StatusUnprocessableEntity, errBody("empty_content"))
	}
	recipient := fmt.Sprintf("%s/%d", b.CampaignID, b.UserID)
	st.seen[recipient]++
	if st.seen[recipient] > 1 {
		st.DuplicateRecipientDeliveries++
		s.m.ProviderDuplicates.WithLabelValues(channel).Inc()
	}
	st.Accepted++
	st.MaxDeliveriesInAny1sWindow = st.deliveries.add(now)
	st.delivered = append(st.delivered, Delivery{Channel: channel, CampaignID: b.CampaignID, UserID: b.UserID, MessageID: b.MessageID, At: now.UTC()})
	return store(http.StatusOK, map[string]string{"provider_message_id": "pm_" + key, "accepted_at": now.UTC().Format(time.RFC3339Nano)})
}

// finish counts a response in the stats and the metric. Caller holds mu.
func (s *Server) finish(channel string, o outcome) outcome {
	code := strconv.Itoa(o.status)
	s.ch[channel].StatusCounts[code]++
	s.m.ProviderRequests.WithLabelValues(channel, code).Inc()
	return o
}

// latency maps two uniform draws to a delay: uniform in [min, max], plus a spike with probability spike_rate.
func (s *Server) latency(u, spikeU float64) time.Duration {
	lo, hi := max(s.cfg.LatencyMinMS, 0), max(s.cfg.LatencyMaxMS, 0)
	hi = max(hi, lo)
	d := time.Duration(float64(lo)+u*float64(hi-lo)) * time.Millisecond
	if spikeU < s.cfg.SpikeRate {
		d += time.Duration(s.cfg.SpikeMS) * time.Millisecond
	}
	return d
}

func validEmail(s string) bool {
	at := strings.LastIndex(s, "@")
	return at > 0 && at < len(s)-1 && strings.Contains(s[at:], ".") && !strings.ContainsAny(s, " \t\r\n")
}

func errBody(code string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code}}
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	// Copy under the lock, encode after releasing it: writing to a slow client while holding the lock would
	// stall every send handler and bunch their arrivals up.
	s.mu.Lock()
	total := ChannelStats{StatusCounts: map[string]int{}}
	perChannel := map[string]ChannelStats{}
	for _, name := range channels {
		st := s.ch[name].ChannelStats
		st.StatusCounts = cloneCounts(st.StatusCounts)
		perChannel[name] = st
		total.Requests += st.Requests
		total.Accepted += st.Accepted
		total.UniqueKeys += st.UniqueKeys
		total.ReplayedKeys += st.ReplayedKeys
		total.DuplicateRecipientDeliveries += st.DuplicateRecipientDeliveries
		total.MaxRequestsInAny1sWindow = max(total.MaxRequestsInAny1sWindow, st.MaxRequestsInAny1sWindow)
		total.MaxDeliveriesInAny1sWindow = max(total.MaxDeliveriesInAny1sWindow, st.MaxDeliveriesInAny1sWindow)
		total.RejectedByLimiter += st.RejectedByLimiter
		total.Hangs += st.Hangs
		for k, v := range st.StatusCounts {
			total.StatusCounts[k] += v
		}
	}
	since := s.since
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"since": since, "channels": perChannel, "total": total})
}

func cloneCounts(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Server) getDelivered(w http.ResponseWriter, r *http.Request) {
	channel, campaign := r.URL.Query().Get("channel"), r.URL.Query().Get("campaign_id")
	if channel != "" && s.ch[channel] == nil {
		writeJSON(w, http.StatusBadRequest, errBody("bad_request"))
		return
	}
	// Copy under the lock, encode after releasing it (see getStats).
	s.mu.Lock()
	items := []Delivery{}
	for _, name := range channels {
		if channel != "" && channel != name {
			continue
		}
		for _, d := range s.ch[name].delivered {
			if campaign == "" || d.CampaignID == campaign {
				items = append(items, d)
			}
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) postConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg // decoding into a copy merges the provided subset
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "bad_request", "message": err.Error()}})
		return
	}
	if err := ValidateConfig(next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_config", "message": err.Error()}})
		return
	}
	reseed := next.Seed != s.cfg.Seed
	s.cfg = next
	if reseed {
		s.reseed()
	}
	writeJSON(w, http.StatusOK, s.cfg)
}

// ValidateConfig rejects fault settings that make no sense (rates outside [0,1], rates summing over 1,
// negative or inverted latency bounds).
func ValidateConfig(c config.ProviderFaults) error {
	var errs []error
	rate := func(name string, v float64) {
		if v < 0 || v > 1 || math.IsNaN(v) {
			errs = append(errs, fmt.Errorf("%s must be between 0 and 1", name))
		}
	}
	rate("spike_rate", c.SpikeRate)
	rate("error_rate", c.ErrorRate)
	rate("rate_429", c.Rate429)
	rate("hang_rate", c.HangRate)
	if c.ErrorRate+c.Rate429+c.HangRate > 1 {
		errs = append(errs, errors.New("error_rate + rate_429 + hang_rate must not exceed 1"))
	}
	if c.LatencyMinMS < 0 || c.LatencyMaxMS < c.LatencyMinMS || c.LatencyMaxMS > 60_000 {
		errs = append(errs, errors.New("latency must satisfy 0 <= latency_min_ms <= latency_max_ms <= 60000"))
	}
	if c.SpikeMS < 0 || c.SpikeMS > 60_000 {
		errs = append(errs, errors.New("spike_ms must be between 0 and 60000"))
	}
	if c.RetryAfterS < 0 || c.RetryAfterS > 3600 {
		errs = append(errs, errors.New("retry_after_s must be between 0 and 3600"))
	}
	if c.RateLimit < 0 {
		errs = append(errs, errors.New("rate_limit must not be negative (0 = off)"))
	}
	return errors.Join(errs...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Package mockprovider is the fake SMS/email provider (docs/contracts/provider-api.md). It lives in
// internal/ so tests can run it in-process with httptest. Owned by the delivery builder.
//
// Phase 0: happy-path sends, idempotency, basic /stats and /admin. Fault injection, the own rate limiter,
// sliding-window maxima and /stats/delivered are the delivery builder's work.
package mockprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
)

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// Server holds the in-memory provider state.
type Server struct {
	mu      sync.Mutex
	cfg     config.ProviderFaults
	m       *metrics.Metrics
	since   time.Time
	replies map[string]stored         // Idempotency-Key -> stored response
	stats   map[string]*ChannelStats  // channel -> stats
	seen    map[string]map[string]int // channel -> "campaign/user" -> deliveries
}

type stored struct {
	status int
	body   []byte
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

// New returns a provider with the given fault configuration.
func New(cfg config.ProviderFaults, m *metrics.Metrics) *Server {
	s := &Server{cfg: cfg, m: m}
	s.reset()
	return s
}

func (s *Server) reset() {
	s.since = time.Now().UTC()
	s.replies = map[string]stored{}
	s.stats = map[string]*ChannelStats{"sms": newStats(), "email": newStats()}
	s.seen = map[string]map[string]int{"sms": {}, "email": {}}
}

func newStats() *ChannelStats { return &ChannelStats{StatusCounts: map[string]int{}} }

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sms", s.send("sms"))
	mux.HandleFunc("POST /v1/email", s.send("email"))
	mux.HandleFunc("GET /stats", s.getStats)
	mux.HandleFunc("GET /admin/config", s.getConfig)
	mux.HandleFunc("POST /admin/config", s.postConfig)
	mux.HandleFunc("POST /admin/reset", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reset()
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

func (s *Server) send(channel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		var b sendBody
		decodeErr := json.NewDecoder(r.Body).Decode(&b)

		s.mu.Lock()
		defer s.mu.Unlock()
		st := s.stats[channel]
		st.Requests++
		reply := func(status int, body any) {
			raw, _ := json.Marshal(body)
			if key != "" && (status == http.StatusOK || status == http.StatusBadRequest || status == http.StatusUnprocessableEntity) {
				s.replies[key] = stored{status, raw}
			}
			st.StatusCounts[fmt.Sprint(status)]++
			s.m.ProviderRequests.WithLabelValues(channel, fmt.Sprint(status)).Inc()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(raw)
		}
		if key == "" {
			reply(http.StatusBadRequest, errBody("missing_idempotency_key"))
			return
		}
		if prev, ok := s.replies[key]; ok {
			st.ReplayedKeys++
			st.StatusCounts[fmt.Sprint(prev.status)]++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(prev.status)
			_, _ = w.Write(prev.body)
			return
		}
		st.UniqueKeys++
		switch {
		case decodeErr != nil:
			reply(http.StatusBadRequest, errBody("bad_request"))
		case channel == "sms" && !e164.MatchString(b.To), channel == "email" && !validEmail(b.To):
			reply(http.StatusBadRequest, errBody("invalid_recipient"))
		case channel == "sms" && b.Text == "", channel == "email" && (b.Subject == "" || b.Text == ""):
			reply(http.StatusUnprocessableEntity, errBody("empty_content"))
		default:
			recipient := fmt.Sprintf("%s/%d", b.CampaignID, b.UserID)
			s.seen[channel][recipient]++
			if s.seen[channel][recipient] > 1 {
				st.DuplicateRecipientDeliveries++
				s.m.ProviderDuplicates.WithLabelValues(channel).Inc()
			}
			st.Accepted++
			reply(http.StatusOK, map[string]string{"provider_message_id": "pm_" + key, "accepted_at": time.Now().UTC().Format(time.RFC3339Nano)})
		}
	}
}

func validEmail(s string) bool {
	at := strings.LastIndex(s, "@")
	return at > 0 && at < len(s)-1 && strings.Contains(s[at:], ".")
}

func errBody(code string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code}}
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := newStats()
	for _, st := range s.stats {
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
	writeJSON(w, http.StatusOK, map[string]any{"since": s.since, "channels": s.stats, "total": total})
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.cfg)
}

func (s *Server) postConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg // decoding into a copy merges the provided subset
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("bad_request"))
		return
	}
	s.cfg = next
	writeJSON(w, http.StatusOK, s.cfg)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

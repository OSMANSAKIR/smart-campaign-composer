// Package api is the HTTP layer: routing, the error model, health endpoints, SSE and the embedded SPA
// (docs/contracts/api.md). Owned by the orchestrator.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/catalog"
	"smartcampaign/internal/clock"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store"
)

// Deps are the server's collaborators.
type Deps struct {
	Pool        *pgxpool.Pool
	Metrics     *metrics.Metrics
	LLM         *llm.Service
	ProviderURL string
	SPA         fs.FS // nil when the binary was built without the spa tag
	Log         *slog.Logger
	Version     string

	SegmentTimeout time.Duration // LLM_SEGMENT_TIMEOUT; 30 s when zero
	ContentTimeout time.Duration // LLM_CONTENT_TIMEOUT; 60 s when zero
}

// Server serves the API.
type Server struct {
	d         Deps
	client    *http.Client
	campaigns *campaign.Service
	hub       *campaign.Hub
	closing   chan struct{}
	closeOnce sync.Once
}

// progressInterval is how often the hub re-reads a watched campaign (need.md N-ORC-006).
const progressInterval = 500 * time.Millisecond

// New returns a server.
func New(d Deps) *Server {
	if d.SegmentTimeout <= 0 {
		d.SegmentTimeout = 30 * time.Second
	}
	if d.ContentTimeout <= 0 {
		d.ContentTimeout = 60 * time.Second
	}
	svc := &campaign.Service{Pool: d.Pool, LLM: d.LLM, Metrics: d.Metrics, Log: d.Log}
	return &Server{d: d, client: &http.Client{Timeout: time.Second}, campaigns: svc,
		hub: campaign.NewHub(svc, clock.Real{}, progressInterval), closing: make(chan struct{})}
}

// Close ends open event streams and stops the progress pollers; register it with http.Server.RegisterOnShutdown,
// because Shutdown alone waits for streams that never go idle.
func (s *Server) Close() {
	s.closeOnce.Do(func() { close(s.closing) })
	s.hub.Close()
}

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", s.d.Metrics.Handler())

	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, catalog.Doc())
	})
	mux.HandleFunc("GET /api/system/status", s.systemStatus)
	mux.HandleFunc("GET /api/system/provider-config", s.getProviderConfig)
	mux.HandleFunc("PUT /api/system/provider-config", s.putProviderConfig)

	mux.HandleFunc("POST /api/campaigns", s.createCampaign)
	mux.HandleFunc("GET /api/campaigns", s.listCampaigns)
	mux.HandleFunc("GET /api/campaigns/{id}", s.getCampaign)
	mux.HandleFunc("POST /api/campaigns/{id}/segment/generate", s.generateSegment)
	mux.HandleFunc("PUT /api/campaigns/{id}/segment", s.putSegment)
	mux.HandleFunc("POST /api/campaigns/{id}/content/generate", s.generateContent)
	mux.HandleFunc("PUT /api/campaigns/{id}/content", s.putContent)
	mux.HandleFunc("POST /api/content/validate", s.validateContent)
	mux.HandleFunc("POST /api/campaigns/{id}/approve", s.approve)
	mux.HandleFunc("GET /api/campaigns/{id}/messages", s.listMessages)
	mux.HandleFunc("GET /api/campaigns/{id}/events", s.events)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "Unknown API route.", nil)
	})
	mux.Handle("/", s.spa())
	return s.instrument(mux)
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	checks := map[string]string{"db": "ok", "migrations": "ok"}
	ready := true
	if err := s.d.Pool.Ping(ctx); err != nil {
		checks["db"], ready = err.Error(), false
	} else if ok, err := store.MigrationsApplied(ctx, s.d.Pool); err != nil || !ok {
		checks["migrations"], ready = "pending", false
	}
	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "checks": checks})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type dispatcher struct {
	WorkerID    string     `json:"worker_id"`
	Channel     string     `json:"channel"`
	HoldsLock   bool       `json:"holds_lock"`
	PausedUntil *time.Time `json:"paused_until"`
	LastSeen    time.Time  `json:"last_seen"`
	Stale       bool       `json:"stale"`
}

// Heartbeat ages are computed by Postgres (one clock for writer and reader). A row older than
// heartbeatFresh marks its dispatcher stale; one older than heartbeatForget belongs to a worker that is gone
// (a crash skips the DELETE on shutdown) and is left out instead of being listed forever.
const (
	heartbeatFresh  = "10 seconds"
	heartbeatForget = "5 minutes"
)

func (s *Server) systemStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	out := map[string]any{"api": map[string]string{"status": "ok", "version": s.d.Version}}

	start := time.Now()
	db := map[string]any{"status": "ok"}
	if err := s.d.Pool.Ping(ctx); err != nil {
		db["status"] = "down"
	}
	db["latency_ms"] = time.Since(start).Milliseconds()
	out["db"] = db

	worker := map[string]any{"status": "down", "dispatchers": []dispatcher{}}
	rows, err := s.d.Pool.Query(ctx, `
		SELECT worker_id, channel, holds_lock, paused_until, at, at < now() - $1::interval
		FROM worker_heartbeats WHERE at >= now() - $2::interval
		ORDER BY channel, at DESC`, heartbeatFresh, heartbeatForget)
	if err == nil {
		var ds []dispatcher
		freshLockHolders := map[string]bool{}
		for rows.Next() {
			var d dispatcher
			if err := rows.Scan(&d.WorkerID, &d.Channel, &d.HoldsLock, &d.PausedUntil, &d.LastSeen, &d.Stale); err != nil {
				break
			}
			if !d.Stale && d.HoldsLock {
				freshLockHolders[d.Channel] = true
			}
			ds = append(ds, d)
		}
		rows.Close()
		switch {
		case len(freshLockHolders) == len(catalog.Channels):
			worker["status"] = "ok"
		case len(ds) > 0:
			worker["status"] = "stale"
		}
		if ds != nil {
			worker["dispatchers"] = ds
		}
	}
	out["worker"] = worker

	provider := map[string]any{"status": "down", "url": s.d.ProviderURL}
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.d.ProviderURL+"/healthz", nil); err == nil {
		if resp, err := s.client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				provider["status"] = "ok"
			}
		}
	}
	out["provider"] = provider

	p := s.d.LLM.Provider()
	out["llm"] = map[string]any{"provider": p.Name(), "model": p.Model(), "mock": p.Name() == "mock"}
	writeJSON(w, http.StatusOK, out)
}

// spa serves the embedded Vue build; unknown paths get index.html so client-side routing works.
func (s *Server) spa() http.Handler {
	if s.d.SPA == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("SPA not embedded in this build. Run `npm run dev` in web/ or build with -tags spa.\n"))
		})
	}
	files := http.FileServerFS(s.d.SPA)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(s.d.SPA, path); errors.Is(err, fs.ErrNotExist) {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}

// instrument counts requests by route pattern and status code.
func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		s.d.Metrics.HTTPRequests.WithLabelValues(route, strconv.Itoa(rec.status)).Inc()
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }

// Unwrap lets http.ResponseController reach Flush on the real writer (needed for SSE).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Issue is one entry of error.details.
type Issue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string, details []Issue) {
	if details == nil {
		details = []Issue{}
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "details": details}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

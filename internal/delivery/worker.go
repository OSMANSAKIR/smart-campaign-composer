package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store"
)

// Known label values, so every series exists at 0 from the first scrape (R-OBS-2) and dashboards and tests
// never have to guess whether a series is missing or just quiet.
var (
	messageStatuses = []string{StatusSent, StatusFailed, StatusSkippedNC, StatusRetry}
	depthStatuses   = []string{StatusQueued, StatusRetry, StatusSending}
	retryClasses    = []Class{ClassThrottled, ClassTransient, ClassAmbiguous, ClassUnreachable}
	responseCodes   = []string{"200", "400", "401", "403", "404", "422", "429", "500", "502", "503", "timeout", "conn_error", "unreachable"}
)

// InitMetrics creates the worker's labeled series at zero.
func InitMetrics(m *metrics.Metrics) {
	for _, ch := range Channels {
		c := string(ch)
		for _, s := range messageStatuses {
			m.Messages.WithLabelValues(c, s)
		}
		for _, s := range depthStatuses {
			m.QueueDepth.WithLabelValues(c, s)
		}
		for _, cl := range retryClasses {
			m.Retries.WithLabelValues(c, string(cl))
		}
		for _, code := range responseCodes {
			m.ProviderReplies.WithLabelValues(c, code)
		}
		m.SendLatency.WithLabelValues(c)
		m.LimiterWait.WithLabelValues(c)
		m.ChannelPaused.WithLabelValues(c)
		m.ContentTooLong.WithLabelValues(c)
	}
}

// Reconciler completes campaigns whose messages are all terminal.
type Reconciler struct {
	Pool     *pgxpool.Pool
	Log      *slog.Logger
	Interval time.Duration // default 1 s
}

// Run blocks until ctx ends. Every worker replica may run it: the update is guarded and idempotent.
func (r *Reconciler) Run(ctx context.Context) {
	interval := defaultDur(r.Interval, time.Second)
	log := r.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		ids, err := Reconcile(ctx, r.Pool)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Warn("reconcile failed", "err", err)
		case len(ids) > 0:
			for _, id := range ids {
				log.Info("campaign finished", "campaign_id", id)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ReadyHandler builds the worker's HTTP surface: /healthz (liveness), /readyz (DB reachable, migrations
// applied, every dispatcher loop beat within 10 s) and /metrics (docs/contracts/observability.md).
func ReadyHandler(pool *pgxpool.Pool, m *metrics.Metrics, ds ...*Dispatcher) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		checks := map[string]string{"db": "ok", "migrations": "ok", "dispatcher": "ok"}
		if err := pool.Ping(ctx); err != nil {
			checks["db"] = "unreachable: " + errorKind(err)
			checks["migrations"] = "unknown"
		} else if ok, err := store.MigrationsApplied(ctx, pool); err != nil || !ok {
			checks["migrations"] = "pending"
		}
		for _, d := range ds {
			if age := time.Since(d.LastLoop()); d.LastLoop().IsZero() || age > 10*time.Second {
				checks["dispatcher"] = fmt.Sprintf("%s loop heartbeat stale", d.Channel)
				break
			}
		}
		for _, v := range checks {
			if v != "ok" {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "checks": checks})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /metrics", m.Handler())
	return mux
}

func errorKind(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

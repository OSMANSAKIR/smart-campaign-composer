// Command worker runs one dispatcher per channel, the campaign reconciler, the scheduled-send scheduler, and serves health, readiness and
// metrics (KICKOFF §7, port 9091). Owned by the delivery builder.
//
// Several replicas may run side by side: the dispatcher of each channel holds a Postgres advisory lock, so
// the others stand by and take over within about a second when the holder dies.
//
//	worker                     run
//	worker healthcheck <url>   Docker HEALTHCHECK helper
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"smartcampaign/internal/config"
	"smartcampaign/internal/delivery"
	"smartcampaign/internal/healthcheck"
	"smartcampaign/internal/logging"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store"
)

func main() { os.Exit(run()) }

func run() int {
	if len(os.Args) > 2 && os.Args[1] == "healthcheck" {
		healthcheck.Run(os.Args[2])
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 2
	}
	log := logging.New("worker", cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	host, _ := os.Hostname()
	workerID := fmt.Sprintf("%s-%d", host, os.Getpid())
	m := metrics.New()
	client := delivery.NewHTTPClient(cfg.ProviderURL, cfg.ProviderTimeout)
	defer client.Close()

	// The pool does not exist until Postgres answers, but /healthz and /metrics should respond right away,
	// so the HTTP server starts first and /readyz reports 503 until the dispatchers are wired.
	var handler struct {
		sync.RWMutex
		h http.Handler
	}
	starting := http.NewServeMux()
	starting.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, `{"status":"ok"}`) })
	starting.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, `{"status":"unavailable","checks":{"db":"connecting"}}`)
	})
	starting.Handle("GET /metrics", m.Handler())
	handler.h = starting
	srv := &http.Server{
		Addr: cfg.WorkerAddr, ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handler.RLock()
			h := handler.h
			handler.RUnlock()
			h.ServeHTTP(w, r)
		}),
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			stop()
		}
	}()
	log.Info("listening", "addr", cfg.WorkerAddr, "worker_id", workerID)

	pool, err := store.OpenWithRetry(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database", "err", err)
		return 1
	}
	defer pool.Close()

	// RATE_LIMIT_WINDOW_GUARD_MS is read here, not in internal/config (not ours); see need.md N-DEL-001.
	guard := 25 * time.Millisecond
	if v, err := strconv.Atoi(os.Getenv("RATE_LIMIT_WINDOW_GUARD_MS")); err == nil && v >= 0 {
		guard = time.Duration(v) * time.Millisecond
		if v == 0 {
			guard = -1 // explicit zero: no guard
		}
	}

	var dispatchers []*delivery.Dispatcher
	for _, ch := range delivery.Channels {
		dispatchers = append(dispatchers, &delivery.Dispatcher{
			Channel: ch, Pool: pool, Client: client, Metrics: m, Log: log, WorkerID: workerID,
			RateLimit: cfg.EffectiveRateLimit(), WindowGuard: guard,
			MaxInFlight: cfg.MaxInFlightPerChannel, Lease: cfg.LeaseDuration,
		})
	}
	handler.Lock()
	handler.h = delivery.ReadyHandler(pool, m, dispatchers...)
	handler.Unlock()
	log.Info("rate limit", "per_channel", cfg.EffectiveRateLimit(), "window_guard", guard, "in_flight", cfg.MaxInFlightPerChannel)

	var wg sync.WaitGroup
	for _, d := range dispatchers {
		wg.Go(func() {
			if err := d.Run(ctx); err != nil {
				log.Error("dispatcher", "channel", d.Channel, "err", err)
			}
		})
	}
	rec := &delivery.Reconciler{Pool: pool, Log: log}
	wg.Go(func() { rec.Run(ctx) })
	sched := &delivery.Scheduler{Pool: pool, Log: log}
	wg.Go(func() { sched.Run(ctx) })

	<-ctx.Done()
	log.Info("shutting down: draining in-flight sends")
	wg.Wait()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	return 0
}

// Command worker runs one dispatcher per channel and serves health and metrics (KICKOFF §7, port 9091).
// Owned by the delivery builder.
//
// Phase 0 skeleton: health/readiness/metrics, a heartbeat row per channel every 2 s, and dispatcher stubs.
//
//	worker                     run
//	worker healthcheck <url>   Docker HEALTHCHECK helper
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/config"
	"smartcampaign/internal/delivery"
	"smartcampaign/internal/healthcheck"
	"smartcampaign/internal/logging"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store"
)

const heartbeatEvery = 2 * time.Second

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
	var (
		pool          atomic.Pointer[pgxpool.Pool]
		lastHeartbeat atomic.Int64 // unix nanos of the last successful heartbeat write
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, `{"status":"ok"}`) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := pool.Load()
		if p == nil || time.Since(time.Unix(0, lastHeartbeat.Load())) > 10*time.Second {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, `{"status":"unavailable","checks":{"dispatcher":"no fresh heartbeat"}}`)
			return
		}
		rctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if ok, err := store.MigrationsApplied(rctx, p); err != nil || !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, `{"status":"unavailable","checks":{"db":"unreachable or migrations pending"}}`)
			return
		}
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.Handle("GET /metrics", m.Handler())
	srv := &http.Server{Addr: cfg.WorkerAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("health server", "err", err)
			stop()
		}
	}()
	log.Info("listening", "addr", cfg.WorkerAddr, "worker_id", workerID)

	p, err := store.OpenWithRetry(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database", "err", err)
		return 1
	}
	defer p.Close()
	pool.Store(p)

	var wg sync.WaitGroup
	for _, ch := range []delivery.Channel{delivery.SMS, delivery.Email} {
		d := &delivery.Dispatcher{Channel: ch}
		wg.Go(func() {
			if err := d.Run(ctx); err != nil {
				log.Error("dispatcher", "channel", ch, "err", err)
			}
		})
	}
	wg.Go(func() {
		t := time.NewTicker(heartbeatEvery)
		defer t.Stop()
		for {
			hctx, cancel := context.WithTimeout(ctx, time.Second)
			_, err := p.Exec(hctx, `INSERT INTO worker_heartbeats (worker_id, channel, holds_lock, at)
				SELECT $1, ch, false, now() FROM unnest($2::text[]) AS ch
				ON CONFLICT (worker_id, channel) DO UPDATE SET at = now()`, workerID, []string{"sms", "email"})
			cancel()
			if err == nil {
				lastHeartbeat.Store(time.Now().UnixNano())
			} else if ctx.Err() == nil {
				log.Warn("heartbeat", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})

	<-ctx.Done()
	log.Info("shutting down")
	wg.Wait()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	return 0
}

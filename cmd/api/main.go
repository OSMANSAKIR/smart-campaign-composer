// Command api serves the HTTP API and the SPA, and runs one-shot maintenance subcommands.
//
//	api serve                 HTTP API + SSE + embedded SPA (default)
//	api migrate               apply embedded migrations
//	api seed                  insert deterministic synthetic users (idempotent)
//	api setup                 migrate, then seed (the compose `migrate` service)
//	api schema [dir]          regenerate the wire schemas into dir (default docs/contracts)
//	api healthcheck <url>     exit 0 when url answers 200 (Docker HEALTHCHECK in distroless)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"smartcampaign/internal/api"
	"smartcampaign/internal/catalog"
	"smartcampaign/internal/config"
	"smartcampaign/internal/healthcheck"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/factory"
	"smartcampaign/internal/logging"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/seed"
	"smartcampaign/internal/store"
	"smartcampaign/web"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if cmd == "healthcheck" {
		healthcheck.Run(arg(2, "http://localhost:8080/readyz"))
	}
	if cmd == "schema" {
		dir := arg(2, "docs/contracts")
		if err := catalog.WriteWireSchemas(dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote wire schemas to", dir)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	log := logging.New("api", cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		err = serve(ctx, cfg, log)
	case "migrate":
		err = migrate(ctx, cfg, log)
	case "seed":
		err = seedUsers(ctx, cfg, log)
	case "setup":
		if err = migrate(ctx, cfg, log); err == nil {
			err = seedUsers(ctx, cfg, log)
		}
	default:
		err = fmt.Errorf("unknown command %q (serve|migrate|seed|setup|schema|healthcheck)", cmd)
	}
	if err != nil {
		log.Error("exit", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}

func arg(i int, def string) string {
	if len(os.Args) > i {
		return os.Args[i]
	}
	return def
}

func migrate(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	openCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	pool, err := store.OpenWithRetry(openCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := store.Migrate(ctx, pool)
	log.Info("migrate", "applied", applied)
	return err
}

func seedUsers(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	openCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	pool, err := store.OpenWithRetry(openCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	n, err := seed.Run(ctx, pool, seed.Generate(cfg.SeedUsers, cfg.SeedRandom, time.Now()))
	log.Info("seed", "requested", cfg.SeedUsers, "inserted", n)
	return err
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	provider, err := factory.New(cfg)
	if err != nil {
		return err
	}
	openCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	pool, err := store.OpenWithRetry(openCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	m := metrics.New()
	srv := &http.Server{
		Addr: cfg.APIAddr,
		Handler: api.New(api.Deps{
			Pool: pool, Metrics: m, LLM: llm.NewService(provider, nil), ProviderURL: cfg.ProviderURL,
			SPA: web.Dist(), Log: log, Version: version,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("listening", "addr", cfg.APIAddr, "llm_provider", provider.Name(), "llm_model", provider.Model())

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}

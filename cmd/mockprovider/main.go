// Command mockprovider runs the fake SMS/email provider (docs/contracts/provider-api.md).
//
//	mockprovider                     serve on MOCKPROVIDER_ADDR
//	mockprovider healthcheck <url>   Docker HEALTHCHECK helper
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"smartcampaign/internal/config"
	"smartcampaign/internal/healthcheck"
	"smartcampaign/internal/logging"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "healthcheck" {
		healthcheck.Run(os.Args[2])
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	log := logging.New("mockprovider", cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: cfg.MockProviderAddr, Handler: mockprovider.New(cfg.Provider, metrics.New()).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Info("listening", "addr", cfg.MockProviderAddr, "faults", cfg.Provider)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

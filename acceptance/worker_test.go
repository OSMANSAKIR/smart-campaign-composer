//go:build acceptance

package acceptance

import (
	"context"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/delivery"
)

// startBackground runs the worker's background loops next to the dispatchers, as cmd/worker does.
func startBackground(ctx context.Context, wg *sync.WaitGroup, pool *pgxpool.Pool, log *slog.Logger) {
	rec := &delivery.Reconciler{Pool: pool, Log: log}
	wg.Go(func() { rec.Run(ctx) })
}

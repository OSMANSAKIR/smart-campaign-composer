package delivery

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Activate moves every scheduled campaign whose send time has come to `sending` and returns their ids
// (F5, docs/contracts/api.md §9.6). The comparison runs on the database clock (now()), like leases and
// retry times, so replicas and tests that move scheduled_at in SQL agree. The guarded UPDATE is idempotent:
// every worker replica may run it, a campaign is activated once, and a campaign cancelled in the meantime
// stays cancelled (its status is no longer `scheduled`).
func Activate(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		UPDATE campaigns SET status = 'sending', updated_at = now()
		WHERE status = 'scheduled' AND scheduled_at <= now()
		RETURNING id::text`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Scheduler activates due scheduled campaigns, once per Interval.
type Scheduler struct {
	Pool     *pgxpool.Pool
	Log      *slog.Logger
	Interval time.Duration // default 1 s
}

// Run blocks until ctx ends. Consent is not looked at here: the claim re-checks it per message, so a user
// who withdrew consent while the campaign was scheduled is skipped at send time (R-AUD-2).
func (s *Scheduler) Run(ctx context.Context) {
	log := s.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	t := time.NewTicker(defaultDur(s.Interval, time.Second))
	defer t.Stop()
	for {
		ids, err := Activate(ctx, s.Pool)
		if err != nil && ctx.Err() == nil {
			log.Warn("scheduler failed", "err", err)
		}
		for _, id := range ids {
			log.Info("scheduled campaign started", "campaign_id", id)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

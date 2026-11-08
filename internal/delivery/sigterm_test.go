package delivery

import (
	"context"
	"testing"
	"time"

	"smartcampaign/internal/store/storetest"
)

// SIGTERM that arrives while a claim statement is running must not abort it: a cancelled statement may
// already have been committed by the server, which would strand its rows in `sending` until the lease runs
// out. The claim finishes, and its rows go straight back unsent (nothing is sent after SIGTERM).
func TestShutdown_ClaimInProgressCompletesAndRowsAreReleased_RSND3(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 6))

	// Hold the users table so the claim (which joins it) blocks inside the server.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, `LOCK TABLE users IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	d := fastDispatcher(pool, SMS, client)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(runCtx) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, 10*time.Second, "the claim to block on the table lock", func() bool {
		var n int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%UPDATE messages m SET%'`).Scan(&n)
		return err == nil && n == 1
	})
	cancel() // SIGTERM, with the claim waiting inside the server
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "Run to return", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})

	if got := statusCounts(t, pool, camp, SMS); got[StatusQueued] != 6 {
		t.Fatalf("after shutdown: %v, want all 6 back in queued, none stranded in sending", got)
	}
	var touched, attempts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE updated_at > created_at), sum(attempts) FROM messages`).Scan(&touched, &attempts); err != nil {
		t.Fatal(err)
	}
	if touched != 6 {
		t.Errorf("%d of 6 rows were touched: the claim was aborted instead of completed and released", touched)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, a message that was never sent must not count a try", attempts)
	}
	if n := len(client.snapshot()); n != 0 {
		t.Fatalf("%d sends happened after SIGTERM", n)
	}
}

package delivery

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"smartcampaign/internal/store/storetest"
)

// TestScheduler_StartsOnlyWhenDue_B1: a scheduled campaign is never claimed while its time is in the future;
// once the database clock passes scheduled_at (moved in SQL, no sleeping) the scheduler activates it, the
// dispatcher drains it and the reconciler completes it. Consent is re-checked at claim: users who withdrew
// while it was scheduled are skipped.
func TestScheduler_StartsOnlyWhenDue_B1(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	users := newUsers(t, pool, 40)
	enqueue(t, pool, camp, SMS, users)
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'scheduled', scheduled_at = now() + interval '1 hour' WHERE id = $1`, camp); err != nil {
		t.Fatal(err)
	}
	revoked := users[:5]
	if _, err := pool.Exec(ctx, `UPDATE users SET sms_consent = false WHERE id = ANY($1)`, revoked); err != nil {
		t.Fatal(err)
	}

	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	startDispatcher(t, fastDispatcher(pool, SMS, client))
	runCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { (&Reconciler{Pool: pool, Interval: 20 * time.Millisecond}).Run(runCtx) })
	wg.Go(func() { (&Scheduler{Pool: pool, Interval: 20 * time.Millisecond}).Run(runCtx) })
	t.Cleanup(func() { stop(); wg.Wait() })

	q := &Queue{Pool: pool, Channel: SMS, Lease: time.Minute}
	for range 30 { // many scheduler ticks and claim rounds while the time is in the future
		if got, err := q.Claim(ctx, 100); err != nil || len(got) != 0 {
			t.Fatalf("claimed %d messages of a campaign scheduled for the future (%v)", len(got), err)
		}
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, camp).Scan(&status); err != nil || status != "scheduled" {
		t.Fatalf("status %q before the time (%v)", status, err)
	}
	if n := len(client.snapshot()); n != 0 {
		t.Fatalf("%d sends before the scheduled time", n)
	}

	// The database clock reaches the send time.
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET scheduled_at = now() - interval '1 second' WHERE id = $1`, camp); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "scheduled campaign completed", func() bool {
		_ = pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, camp).Scan(&status)
		return status == "completed"
	})
	counts := statusCounts(t, pool, camp, SMS)
	if counts[StatusSent] != 35 || counts[StatusSkippedNC] != 5 || len(client.snapshot()) != 35 {
		t.Fatalf("counts %v, provider calls %d; want 35 sent and 5 skipped (consent revoked while scheduled)", counts, len(client.snapshot()))
	}
	for _, c := range client.snapshot() {
		if slices.Contains(revoked, c.Req.UserID) {
			t.Fatalf("user %d withdrew consent while scheduled and was still sent a message", c.Req.UserID)
		}
	}
}

// TestActivate_OnceAndNotCancelled_B1: concurrent replicas activate a due campaign exactly once, and a
// campaign cancelled while scheduled is never revived.
func TestActivate_OnceAndNotCancelled_B1(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	due, cancelled, future := newCampaign(t, pool), newCampaign(t, pool), newCampaign(t, pool)
	for camp, interval := range map[string]string{due: "-1 second", cancelled: "-1 second", future: "1 minute"} {
		if _, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'scheduled', scheduled_at = now() + $2::interval WHERE id = $1`, camp, interval); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, cancelled); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var activated []string
	var wg sync.WaitGroup
	for range 6 { // six replicas ticking at once
		wg.Go(func() {
			ids, err := Activate(ctx, pool)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			activated = append(activated, ids...)
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(activated) != 1 || activated[0] != due {
		t.Fatalf("activated %v, want exactly [%s]", activated, due)
	}
	for camp, want := range map[string]string{due: "sending", cancelled: "cancelled", future: "scheduled"} {
		var st string
		if err := pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, camp).Scan(&st); err != nil || st != want {
			t.Fatalf("campaign %s: status %q, want %q (%v)", camp, st, want, err)
		}
	}
}

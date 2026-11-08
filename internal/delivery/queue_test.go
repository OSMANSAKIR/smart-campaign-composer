package delivery

import (
	"context"
	"sync"
	"testing"
	"time"

	"smartcampaign/internal/store/storetest"
)

func TestClaim_SkipLockedNoDoubleClaim_RSND3(t *testing.T) {
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	const total = 400
	enqueue(t, pool, camp, SMS, newUsers(t, pool, total))

	// Two claimers racing for the same queue, in small batches so they interleave on the same rows.
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for range 2 {
		q := &Queue{Pool: pool, Channel: SMS, Lease: 30 * time.Second}
		wg.Go(func() {
			for {
				rows, err := q.Claim(context.Background(), 7)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if len(rows) == 0 {
					return
				}
				mu.Lock()
				for _, r := range rows {
					seen[r.ID]++
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if len(seen) != total {
		t.Fatalf("claimed %d distinct messages, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("message %s claimed %d times", id, n)
		}
	}
	counts := statusCounts(t, pool, camp, SMS)
	if counts[StatusSending] != total {
		t.Fatalf("status counts %v, want all %d sending", counts, total)
	}
	var maxAttempts int
	if err := pool.QueryRow(context.Background(), `SELECT max(attempts) FROM messages`).Scan(&maxAttempts); err != nil || maxAttempts != 1 {
		t.Fatalf("max attempts = %d (%v), want 1", maxAttempts, err)
	}
}

func TestClaim_ConsentRevoked_Skipped_RAUD2(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	users := newUsers(t, pool, 10)
	enqueue(t, pool, camp, SMS, users)
	enqueue(t, pool, camp, Email, users)

	// After enqueue: three users withdraw SMS consent; one of them also email consent, one only email.
	if _, err := pool.Exec(ctx, `UPDATE users SET sms_consent = false WHERE id = ANY($1)`, users[:3]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email_consent = false WHERE id = ANY($1)`, []int64{users[0], users[9]}); err != nil {
		t.Fatal(err)
	}

	sms := &Queue{Pool: pool, Channel: SMS, Lease: time.Minute}
	rows, err := sms.Claim(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	skipped := 0
	for _, r := range rows {
		if r.Skipped {
			skipped++
		}
	}
	if len(rows) != 10 || skipped != 3 {
		t.Fatalf("claimed %d rows with %d skipped, want 10 and 3", len(rows), skipped)
	}
	if got := statusCounts(t, pool, camp, SMS); got[StatusSkippedNC] != 3 || got[StatusSending] != 7 {
		t.Fatalf("sms status counts %v", got)
	}
	var skippedAttempts int
	if err := pool.QueryRow(ctx, `SELECT sum(attempts) FROM messages WHERE status = 'skipped_no_consent'`).Scan(&skippedAttempts); err != nil || skippedAttempts != 0 {
		t.Fatalf("a skipped message must not count an attempt: %d (%v)", skippedAttempts, err)
	}

	// Consent is per channel: the email queue judges email consent only.
	email := &Queue{Pool: pool, Channel: Email, Lease: time.Minute}
	if _, err := email.Claim(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if got := statusCounts(t, pool, camp, Email); got[StatusSkippedNC] != 2 || got[StatusSending] != 8 {
		t.Fatalf("email status counts %v", got)
	}
}

func TestClaim_OnlySendingCampaignsAndDueMessages_RSND1(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	other := newCampaign(t, pool)
	users := newUsers(t, pool, 4)
	enqueue(t, pool, camp, SMS, users[:2])
	enqueue(t, pool, other, SMS, users[2:])
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'cancelled' WHERE id = $1`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET status = 'retry', next_attempt_at = now() + interval '1 hour'
		WHERE campaign_id = $1 AND user_id = $2`, camp, users[1]); err != nil {
		t.Fatal(err)
	}
	q := &Queue{Pool: pool, Channel: SMS, Lease: time.Minute}
	rows, err := q.Claim(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UserID != users[0] {
		t.Fatalf("claimed %+v, want only the due message of the sending campaign", rows)
	}
}

func TestLeaseExpiry_Reclaimed_RSND3(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 2))
	q := &Queue{Pool: pool, Channel: SMS, Lease: time.Minute}

	rows, err := q.Claim(ctx, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("claim: %v %d", err, len(rows))
	}
	// A live lease is left alone.
	if requeued, failed, err := q.ReclaimExpired(ctx, 5); err != nil || requeued+failed != 0 {
		t.Fatalf("reclaimed a live lease: %d %d %v", requeued, failed, err)
	}
	// The worker "dies": the lease runs out. One message already used its last attempt.
	if _, err := pool.Exec(ctx, `UPDATE messages SET lease_until = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET attempts = 5 WHERE id = $1`, rows[1].ID); err != nil {
		t.Fatal(err)
	}
	requeued, failed, err := q.ReclaimExpired(ctx, 5)
	if err != nil || requeued != 1 || failed != 1 {
		t.Fatalf("reclaim = %d requeued, %d failed (%v), want 1 and 1", requeued, failed, err)
	}
	again, err := q.Claim(ctx, 10)
	if err != nil || len(again) != 1 || again[0].ID != rows[0].ID || again[0].Attempts != 2 {
		t.Fatalf("second claim = %+v (%v), want the first message with attempt 2", again, err)
	}
	var lastErr string
	if err := pool.QueryRow(ctx, `SELECT last_error FROM messages WHERE id = $1`, rows[1].ID).Scan(&lastErr); err != nil || lastErr != "lease_expired" {
		t.Fatalf("last_error = %q (%v)", lastErr, err)
	}
}

func TestFinish_StaleWriterIsNoOp_RSND3(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 1))
	q := &Queue{Pool: pool, Channel: SMS, Lease: time.Minute}

	first, _ := q.Claim(ctx, 1)
	if _, err := pool.Exec(ctx, `UPDATE messages SET lease_until = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.ReclaimExpired(ctx, 5); err != nil {
		t.Fatal(err)
	}
	second, _ := q.Claim(ctx, 1) // another dispatcher took over: attempt 2
	if len(first) != 1 || len(second) != 1 {
		t.Fatal("setup")
	}
	if changed, err := q.Finish(ctx, first[0], Outcome{Status: StatusSent, Class: ClassSuccess}); err != nil || changed {
		t.Fatalf("the stale holder's late result must be dropped: changed=%v err=%v", changed, err)
	}
	if changed, err := q.Finish(ctx, second[0], Outcome{Status: StatusSent, Class: ClassSuccess, ProviderMessageID: "pm_1"}); err != nil || !changed {
		t.Fatalf("current holder: changed=%v err=%v", changed, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_attempts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("delivery_attempts rows = %d (%v), want 1 (only the accepted write)", n, err)
	}
}

func TestReconcile_CompletesWhenAllTerminal_RFLOW5(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	users := newUsers(t, pool, 3)

	clean, withFailure, busy := newCampaign(t, pool), newCampaign(t, pool), newCampaign(t, pool)
	enqueue(t, pool, clean, SMS, users)
	enqueue(t, pool, withFailure, SMS, users)
	enqueue(t, pool, busy, SMS, users)
	set := func(camp, status string, uid int64) {
		if _, err := pool.Exec(ctx, `UPDATE messages SET status = $3 WHERE campaign_id = $1 AND user_id = $2`, camp, uid, status); err != nil {
			t.Fatal(err)
		}
	}
	for i, u := range users {
		set(clean, []string{"sent", "sent", "skipped_no_consent"}[i], u)
		set(withFailure, []string{"sent", "failed", "skipped_no_consent"}[i], u)
		set(busy, []string{"sent", "retry", "sent"}[i], u)
	}

	ids, err := Reconcile(ctx, pool)
	if err != nil || len(ids) != 2 {
		t.Fatalf("reconcile = %v (%v), want two campaigns", ids, err)
	}
	status := func(id string) (s string, completed bool) {
		if err := pool.QueryRow(ctx, `SELECT status, completed_at IS NOT NULL FROM campaigns WHERE id = $1`, id).Scan(&s, &completed); err != nil {
			t.Fatal(err)
		}
		return
	}
	for camp, want := range map[string]string{clean: "completed", withFailure: "completed_with_failures", busy: "sending"} {
		if got, done := status(camp); got != want || done != (want != "sending") {
			t.Errorf("campaign %s: status %q completed_at set=%v, want %q", camp, got, done, want)
		}
	}
	if ids, err := Reconcile(ctx, pool); err != nil || len(ids) != 0 {
		t.Fatalf("second reconcile must be a no-op: %v %v", ids, err)
	}
}

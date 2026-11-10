package delivery

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/store/storetest"
)

// startSweeper runs the reconciler (which also sweeps cancelled campaigns) until the test ends. A Finish that
// began just before a cancel committed can still write `retry` after it; the sweep ends such a message.
func startSweeper(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Reconciler{Pool: pool, Interval: 20 * time.Millisecond}).Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
}

// cancelNow cancels through the service, exactly as POST /api/campaigns/{id}/cancel does.
func cancelNow(t testing.TB, pool *pgxpool.Pool, id string) campaign.CancelResult {
	t.Helper()
	res, err := (&campaign.Service{Pool: pool}).Cancel(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// requireAllTerminal fails when a message is non-terminal or sent + failed + cancelled + skipped differs from total.
func requireAllTerminal(t testing.TB, pool *pgxpool.Pool, camp string, ch Channel, total int) map[string]int {
	t.Helper()
	counts := statusCounts(t, pool, camp, ch)
	if n := nonTerminal(counts); n != 0 {
		t.Fatalf("%s: %d messages not terminal: %v", ch, n, counts)
	}
	if sum := counts[StatusSent] + counts[StatusFailed] + counts["cancelled"] + counts[StatusSkippedNC]; sum != total {
		t.Fatalf("%s: sent+failed+cancelled+skipped = %d, want %d (%v)", ch, sum, total, counts)
	}
	return counts
}

// TestCancel_InFlightBoundIsTight_F4 holds every send at the provider, so a full batch is claimed and on the
// wire, then cancels. Exactly MaxInFlight messages (the claimed batch) are sent after the cancel; every other
// message is cancelled and nothing is left in flight. This is the documented bound (DECISIONS.md): per channel,
// messages sent after the cancel commits <= MaxInFlight.
func TestCancel_InFlightBoundIsTight_F4(t *testing.T) {
	const users, inflight = 200, 8
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, users))

	gate := make(chan struct{})
	var started atomic.Int64
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) {
		started.Add(1)
		<-gate
		return ok200(r)
	}}
	d := fastDispatcher(pool, SMS, client)
	d.MaxInFlight = inflight
	startDispatcher(t, d)
	eventually(t, 10*time.Second, "a full batch is on the wire", func() bool { return started.Load() == inflight })

	res := cancelNow(t, pool, camp)
	if res.Cancelled != users-inflight || res.Campaign.Status != campaign.StatusCancelled || res.Campaign.CancelledAt == nil {
		t.Fatalf("cancel: %d moved (want %d), campaign %s", res.Cancelled, users-inflight, res.Campaign.Status)
	}
	close(gate)
	eventually(t, 10*time.Second, "in-flight sends recorded", func() bool { return statusCounts(t, pool, camp, SMS)[StatusSending] == 0 })

	counts := requireAllTerminal(t, pool, camp, SMS, users)
	if got := len(client.snapshot()); got != inflight || counts[StatusSent] != inflight || counts["cancelled"] != users-inflight {
		t.Fatalf("provider calls %d, counts %v; want exactly %d sent after cancel", got, counts, inflight)
	}
}

// TestCancel_RaceMidBatch_F4 cancels while the dispatcher is busy sending, with the provider failing every
// third request so messages are also in `retry` at the cancel. After the dispatcher drains: at most
// MaxInFlight sends started after the cancel returned, every message is terminal, retry results became
// `cancelled` (not another try) and sent + failed + cancelled + skipped = total.
func TestCancel_RaceMidBatch_F4(t *testing.T) {
	const users, inflight = 400, 8
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	ids := newUsers(t, pool, users)
	enqueue(t, pool, camp, SMS, ids)

	client := &scriptedClient{}
	client.fn = func(n int, r SendRequest) (SendResult, error) {
		time.Sleep(3 * time.Millisecond) // a provider that takes a moment, so claims overlap the cancel
		if n%3 == 0 {
			return SendResult{Status: 500, Latency: time.Millisecond}, nil
		}
		return ok200(r)
	}
	d := fastDispatcher(pool, SMS, client)
	d.MaxInFlight = inflight
	startDispatcher(t, d)
	startSweeper(t, pool)
	eventually(t, 10*time.Second, "sending is under way", func() bool { return len(client.snapshot()) >= 40 })

	res := cancelNow(t, pool, camp)
	atCancel := len(client.snapshot())
	if res.Cancelled == 0 {
		t.Fatal("the cancel found nothing to cancel; the test must cancel mid-run")
	}
	eventually(t, 15*time.Second, "dispatcher drained", func() bool { return nonTerminal(statusCounts(t, pool, camp, SMS)) == 0 })

	after := len(client.snapshot()) - atCancel
	if after > inflight {
		t.Fatalf("%d sends started after the cancel returned; the bound is MaxInFlight = %d", after, inflight)
	}
	counts := requireAllTerminal(t, pool, camp, SMS, users)
	if counts["cancelled"] < res.Cancelled {
		t.Fatalf("cancelled %d, but the cancel call alone moved %d", counts["cancelled"], res.Cancelled)
	}
	// What the provider accepted is exactly what the database calls sent: no message vanished or doubled.
	var accepted int
	for _, c := range client.snapshot() {
		if c.N%3 != 0 {
			accepted++
		}
	}
	if accepted != counts[StatusSent] {
		t.Fatalf("provider accepted %d, database sent %d", accepted, counts[StatusSent])
	}
	var dups int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM (SELECT message_id FROM delivery_attempts WHERE error_class = 'success'
		GROUP BY message_id HAVING count(*) > 1) x`).Scan(&dups); err != nil || dups != 0 {
		t.Fatalf("messages with more than one success: %d (%v)", dups, err)
	}
}

// TestCancel_NeverClaimsCancelledCampaign_F4: queued and retry messages of a cancelled campaign are never
// claimed, even if some write path left them behind; the sweep ends them as cancelled.
func TestCancel_NeverClaimsCancelledCampaign_F4(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 30))
	// Simulate stragglers: the campaign is cancelled without touching its messages.
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, camp); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET status = 'retry' WHERE campaign_id = $1 AND user_id % 2 = 0`, camp); err != nil {
		t.Fatal(err)
	}
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	d := fastDispatcher(pool, SMS, client)
	startDispatcher(t, d)

	q := &Queue{Pool: pool, Channel: SMS, Lease: time.Minute}
	for range 20 { // several claim rounds: nothing is handed out
		got, err := q.Claim(ctx, 100)
		if err != nil || len(got) != 0 {
			t.Fatalf("claimed %d messages of a cancelled campaign (%v)", len(got), err)
		}
	}
	if n := len(client.snapshot()); n != 0 {
		t.Fatalf("%d sends for a cancelled campaign", n)
	}
	n, err := SweepCancelled(ctx, pool)
	if err != nil || n != 30 {
		t.Fatalf("sweep moved %d (%v), want 30", n, err)
	}
	if c := statusCounts(t, pool, camp, SMS); c["cancelled"] != 30 {
		t.Fatalf("counts %v", c)
	}
	// And a cancelled campaign is never completed by the reconciler.
	ids, err := Reconcile(ctx, pool)
	if err != nil || len(ids) != 0 {
		t.Fatalf("reconcile touched %v (%v)", ids, err)
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, camp).Scan(&st); err != nil || st != "cancelled" {
		t.Fatalf("status %s (%v)", st, err)
	}
}

// TestCancel_ExpiredLeaseAndReleaseBecomeCancelled_F4: a claimed message of a cancelled campaign whose lease
// expires, or that is handed back unsent, ends as cancelled instead of going back to the queue.
func TestCancel_ExpiredLeaseAndReleaseBecomeCancelled_F4(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 4))
	q := &Queue{Pool: pool, Channel: SMS, Lease: time.Minute}
	claimed, err := q.Claim(ctx, 4)
	if err != nil || len(claimed) != 4 {
		t.Fatalf("claim: %d %v", len(claimed), err)
	}
	cancelNow(t, pool, camp) // all four are `sending`: the cancel leaves them to finish
	if c := statusCounts(t, pool, camp, SMS); c[StatusSending] != 4 {
		t.Fatalf("a claimed message must stay sending at cancel: %v", c)
	}
	// Two leases expire, one message is released unsent, one reports a retryable failure.
	if _, err := pool.Exec(ctx, `UPDATE messages SET lease_until = now() - interval '1 s' WHERE id = ANY($1)`,
		[]string{claimed[0].ID, claimed[1].ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.ReclaimExpired(ctx, 5, 3); err != nil {
		t.Fatal(err)
	}
	if err := q.Release(ctx, claimed[2], 0); err != nil {
		t.Fatal(err)
	}
	status500 := 500
	changed, err := q.Finish(ctx, claimed[3], Outcome{Status: StatusRetry, HTTPStatus: &status500, Class: ClassTransient, Delay: time.Second, LastError: "http 500"})
	if err != nil || !changed {
		t.Fatalf("finish: %v %v", changed, err)
	}
	if c := statusCounts(t, pool, camp, SMS); c["cancelled"] != 4 {
		t.Fatalf("want all four cancelled, got %v", c)
	}
	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_attempts WHERE http_status = 500`).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatalf("the failed try must still be recorded: %d %v", recorded, err)
	}
}

// TestCancel_ParallelCancelAndClaim_F4 hammers cancel against a running dispatcher on several campaigns at
// once: every campaign ends with all messages terminal and consistent counts, with no deadlock.
func TestCancel_ParallelCancelAndClaim_F4(t *testing.T) {
	pool := storetest.NewDB(t)
	users := newUsers(t, pool, 120)
	var camps []string
	for range 4 {
		c := newCampaign(t, pool)
		enqueue(t, pool, c, SMS, users)
		camps = append(camps, c)
	}
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) {
		time.Sleep(time.Millisecond)
		if r.UserID%7 == 0 {
			return SendResult{Status: http.StatusServiceUnavailable}, nil
		}
		return ok200(r)
	}}
	d := fastDispatcher(pool, SMS, client)
	d.MaxInFlight = 6
	startDispatcher(t, d)
	startSweeper(t, pool)
	eventually(t, 10*time.Second, "sending is under way", func() bool { return len(client.snapshot()) >= 20 })
	var wg sync.WaitGroup
	for i, c := range camps {
		wg.Go(func() {
			if i%2 == 0 { // two of the four are cancelled, twice, concurrently
				for range 2 {
					if _, err := (&campaign.Service{Pool: pool}).Cancel(context.Background(), c); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	wg.Wait()
	for i, c := range camps {
		if i%2 == 0 {
			eventually(t, 15*time.Second, "cancelled campaign drained", func() bool { return nonTerminal(statusCounts(t, pool, c, SMS)) == 0 })
			requireAllTerminal(t, pool, c, SMS, len(users))
		}
	}
}

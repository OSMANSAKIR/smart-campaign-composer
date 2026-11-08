package delivery

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/store/storetest"
)

// TestTakeover_ProviderNeverSeesMoreThanN_RSND2 hands the channel from one replica to another at full rate.
// Two ways the old holder goes away: a graceful stop (it drains, then releases the lock) and a terminated
// lock session (it keeps sending until it notices). In both the new holder's first request must come a full
// window after the old holder's last, or the provider sees both at once (V-013: 129 and 141 at N=100).
func TestTakeover_ProviderNeverSeesMoreThanN_RSND2(t *testing.T) {
	const n, users = 100, 650
	for _, tc := range []struct {
		name string
		kill func(t *testing.T, pool *pgxpool.Pool, stopA func())
	}{
		{"graceful stop", func(_ *testing.T, _ *pgxpool.Pool, stopA func()) { stopA() }},
		{"lock session terminated", func(t *testing.T, pool *pgxpool.Pool, _ func()) {
			if killed := terminateLockSession(t, pool); killed != 1 {
				t.Fatalf("terminated %d lock sessions", killed)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := storetest.NewDB(t)
			camp := newCampaign(t, pool)
			enqueue(t, pool, camp, SMS, newUsers(t, pool, users))

			prov := mockprovider.New(config.ProviderFaults{LatencyMinMS: 20, LatencyMaxMS: 300, Seed: 3}, metrics.New())
			srv := httptest.NewServer(prov.Handler())
			t.Cleanup(func() { prov.Close(); srv.Close() })
			client := NewHTTPClient(srv.URL, 5*time.Second)
			t.Cleanup(client.Close)

			mk := func(id string) *Dispatcher {
				d := fastDispatcher(pool, SMS, client)
				d.WorkerID, d.RateLimit, d.Metrics, d.MaxInFlight = id, n, metrics.New(), 32
				d.TakeoverWait = 0 // the default quiet time is the thing under test
				d.WindowGuard = testJitterGuard
				return d
			}
			a, b := mk("a"), mk("b")
			stopA := startDispatcher(t, a)
			eventually(t, 5*time.Second, "a holds the lock", a.HoldsLock)
			startDispatcher(t, b)

			// A is silent for the quiet time, then sends at full rate; hand over once it has done so for a while.
			eventually(t, 30*time.Second, "a at full rate", func() bool { return statusCounts(t, pool, camp, SMS)[StatusSent] >= 250 })
			tc.kill(t, pool, stopA)
			eventually(t, 10*time.Second, "b holds the lock", b.HoldsLock)
			eventually(t, 90*time.Second, "every message terminal", func() bool { return nonTerminal(statusCounts(t, pool, camp, SMS)) == 0 })

			s := providerStats(t, srv.URL)["sms"]
			t.Logf("provider: requests=%d accepted=%d max_requests_1s=%d duplicates=%d", s.Requests, s.Accepted, s.MaxRequestsInAny1sWindow, s.DuplicateRecipientDeliveries)
			if s.MaxRequestsInAny1sWindow > n {
				t.Errorf("provider saw %d requests in one second across the takeover, limit %d", s.MaxRequestsInAny1sWindow, n)
			}
			if s.DuplicateRecipientDeliveries != 0 {
				t.Errorf("%d duplicate deliveries", s.DuplicateRecipientDeliveries)
			}
			if c := statusCounts(t, pool, camp, SMS); c[StatusSent] != users {
				t.Errorf("outcome %v, want all %d sent", c, users)
			}
		})
	}
}

// cancelCountingLimiter blocks every Acquire until its context ends and counts how many ended that way.
type cancelCountingLimiter struct{ cancelled atomic.Int64 }

func (l *cancelCountingLimiter) Acquire(ctx context.Context) (Ticket, error) {
	<-ctx.Done()
	l.cancelled.Add(1)
	return nil, ctx.Err()
}

// terminateLockSession kills the Postgres session that holds the channel's advisory lock.
func terminateLockSession(t testing.TB, pool *pgxpool.Pool) int {
	t.Helper()
	var killed int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(pg_terminate_backend(pid)) FROM pg_locks
		WHERE locktype = 'advisory' AND granted AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		  AND ((classid::bigint << 32) | objid::bigint) = $1`, LockKey(SMS)).Scan(&killed); err != nil {
		t.Fatal(err)
	}
	return killed
}

// A replica that lost the lock must not keep exporting the queue depth it last saw (R-OBS-2): the gauge is
// shared by every replica's scrape.
func TestLockLoss_QueueDepthGaugesAreZeroed_ROBS2(t *testing.T) {
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 8))
	m := metrics.New()
	d := fastDispatcher(pool, SMS, &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }})
	d.Limiter, d.MaxInFlight, d.Metrics = &cancelCountingLimiter{}, 3, m
	startDispatcher(t, d)
	depth := func(status string) float64 { return valueOf(m.QueueDepth.WithLabelValues("sms", status)) }
	eventually(t, 5*time.Second, "depth reported while holding the lock", func() bool {
		return depth(StatusQueued) == 5 && depth(StatusSending) == 3
	})

	// Keep the lock away from d after its session dies, so the only thing that can zero the gauge is the loss.
	terminateLockSession(t, pool)
	thief, err := pgx.ConnectConfig(context.Background(), pool.Config().ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = thief.Close(context.Background()) })
	eventually(t, 5*time.Second, "another session takes the lock", func() bool {
		var got bool
		return thief.QueryRow(context.Background(), `SELECT pg_try_advisory_lock($1)`, LockKey(SMS)).Scan(&got) == nil && got
	})
	eventually(t, 5*time.Second, "queue_depth zeroed", func() bool {
		return depth(StatusQueued) == 0 && depth(StatusRetry) == 0 && depth(StatusSending) == 0
	})
	if d.HoldsLock() {
		t.Fatal("dispatcher still claims to hold the lock")
	}
}

// TestLockLoss_QueuedSendsAreReleasedUnsent_RSND2: when the lock session dies, sends queued behind the
// limiter must be handed back, not sent after another replica may have taken over.
func TestLockLoss_QueuedSendsAreReleasedUnsent_RSND2(t *testing.T) {
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 8))
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	lim := &cancelCountingLimiter{}
	d := fastDispatcher(pool, SMS, client)
	d.Limiter, d.MaxInFlight = lim, 4
	startDispatcher(t, d) // runs until the test ends: only the lock loss can cancel an Acquire

	eventually(t, 5*time.Second, "4 messages claimed and queued behind the limiter", func() bool {
		return statusCounts(t, pool, camp, SMS)[StatusSending] == 4
	})
	if killed := terminateLockSession(t, pool); killed != 1 {
		t.Fatalf("terminated %d lock sessions", killed)
	}
	eventually(t, 10*time.Second, "queued sends released by the lost lock", func() bool { return lim.cancelled.Load() >= 4 })
	if n := len(client.snapshot()); n != 0 {
		t.Fatalf("%d sends happened after the lock was lost", n)
	}
}

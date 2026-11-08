package delivery

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fastDispatcher returns a dispatcher tuned for tests: short polls and heartbeats, tiny backoffs.
func fastDispatcher(pool *pgxpool.Pool, ch Channel, client ProviderClient) *Dispatcher {
	return &Dispatcher{
		Channel: ch, Pool: pool, Client: client,
		PollInterval: 10 * time.Millisecond, HeartbeatEvery: 40 * time.Millisecond, StandbyPoll: 20 * time.Millisecond,
		Policy: RetryPolicy{BaseBackoff: 5 * time.Millisecond, MaxBackoff: 40 * time.Millisecond, MaxAttempts: 5,
			MaxThrottles: 20, DefaultRetryAfter: 50 * time.Millisecond, MaxRetryAfter: 100 * time.Millisecond},
		RateLimit: 1000, // out of the way unless a test is about the limiter
	}
}

// fixtures

const (
	testSMS     = "Merhaba {{first_name}}, {{city}} için %20 indirim!"
	testSubject = "{{first_name}}, indirim sizi bekliyor"
	testBody    = "Merhaba {{first_name}},\n\n{{city}} mağazasında indirim var.\nGörüşmek üzere."
)

// newCampaign inserts a campaign in `sending` with approved content version 1.
func newCampaign(t testing.TB, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, status, language, channels, approved_content_version, approved_at)
		VALUES ('test brief', 'sending', 'tr', '{sms,email}', 1, now()) RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO campaign_content (campaign_id, version, sms_text, email_subject, email_body, source, validation)
		VALUES ($1, 1, $2, $3, $4, 'llm', '{}')`, id, testSMS, testSubject, testBody); err != nil {
		t.Fatal(err)
	}
	return id
}

// newUsers inserts n consented synthetic users (valid phone and email) and returns their ids.
func newUsers(t testing.TB, pool *pgxpool.Pool, n int) []int64 {
	t.Helper()
	var offset int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&offset)
	rows, err := pool.Query(context.Background(), `
		INSERT INTO users (seed_key, first_name, last_name, email, phone, city, locale, birth_date, signup_at,
		                   loyalty_tier, sms_consent, email_consent)
		SELECT 'u' || i, 'Ayşe' || i, 'Yılmaz', 'ayse' || i || '@example.com', '+90532' || lpad(i::text, 7, '0'),
		       'İstanbul', 'tr', date '1990-01-01', now(), 'bronze', true, true
		FROM generate_series($1::int, $2::int) AS i RETURNING id`, offset+1, offset+n)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// enqueue creates queued messages for the users on one channel, as the approve transaction does.
func enqueue(t testing.TB, pool *pgxpool.Pool, campaign string, ch Channel, users []int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO messages (campaign_id, user_id, channel, content_version)
		SELECT $1, u, $2, 1 FROM unnest($3::bigint[]) AS u`, campaign, string(ch), users); err != nil {
		t.Fatal(err)
	}
}

func statusCounts(t testing.TB, pool *pgxpool.Pool, campaign string, ch Channel) map[string]int {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT status, count(*) FROM messages WHERE campaign_id = $1 AND channel = $2 GROUP BY status`, campaign, string(ch))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			t.Fatal(err)
		}
		out[s] = n
	}
	return out
}

func nonTerminal(counts map[string]int) int {
	return counts[StatusQueued] + counts[StatusRetry] + counts[StatusSending]
}

// eventually polls cond until it holds or the deadline passes. Polling an external system (the database)
// has no clock to inject; the interval is short and the failure message names what never happened.
func eventually(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for: %s", timeout, what)
		}
		<-tick.C
	}
}

// startDispatcher runs d until the returned stop function is called, which also waits for Run to return.
func startDispatcher(t testing.TB, d *Dispatcher) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.Run(ctx)
	}()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

// scriptedClient is a ProviderClient whose answers a test controls. It records every call.
type scriptedClient struct {
	mu    sync.Mutex
	fn    func(n int, r SendRequest) (SendResult, error)
	calls []sentCall
}

type sentCall struct {
	Req SendRequest
	At  time.Time
	N   int
}

func (c *scriptedClient) Send(ctx context.Context, r SendRequest) (SendResult, error) {
	c.mu.Lock()
	n := len(c.calls) + 1
	c.calls = append(c.calls, sentCall{Req: r, At: time.Now(), N: n})
	fn := c.fn
	c.mu.Unlock()
	return fn(n, r)
}

func (c *scriptedClient) snapshot() []sentCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sentCall(nil), c.calls...)
}

func ok200(r SendRequest) (SendResult, error) {
	return SendResult{Status: 200, ProviderMessageID: "pm_" + r.IdempotencyKey, Latency: time.Millisecond}, nil
}

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/store/storetest"
)

func TestThrottle_PausesChannelHonorsRetryAfter_RSND4(t *testing.T) {
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 40))

	var mu sync.Mutex
	var throttledAt time.Time
	client := &scriptedClient{}
	client.fn = func(n int, r SendRequest) (SendResult, error) {
		if n == 3 { // the provider throttles one request and asks for 30 s; the policy caps it at 100 ms here
			mu.Lock()
			throttledAt = time.Now()
			mu.Unlock()
			return SendResult{Status: 429, Header: http.Header{"Retry-After": {"30"}}}, nil
		}
		return ok200(r)
	}
	d := fastDispatcher(pool, SMS, client)
	d.MaxInFlight = 8
	d.Metrics = metrics.New()
	startDispatcher(t, d)

	eventually(t, 10*time.Second, "all messages sent", func() bool {
		return statusCounts(t, pool, camp, SMS)[StatusSent] == 40
	})

	mu.Lock()
	t429 := throttledAt
	mu.Unlock()
	const pauseCap, slack = 100 * time.Millisecond, 25 * time.Millisecond
	var after, duringPause int
	for _, c := range client.snapshot() {
		if c.At.After(t429.Add(slack)) {
			after++
			if c.At.Before(t429.Add(pauseCap - slack)) {
				duringPause++
			}
		}
	}
	if duringPause != 0 || after == 0 {
		t.Fatalf("%d provider calls started during the pause (and %d after); the channel must stay quiet for Retry-After", duringPause, after)
	}

	if got := valueOf(d.Metrics.Retries.WithLabelValues("sms", "throttled")); got != 1 {
		t.Errorf("retries_total{throttled} = %v, want 1", got)
	}
	// The throttled try did not use up an attempt: every message was sent on its first counted attempt.
	var maxAttempts, throttled int
	if err := pool.QueryRow(context.Background(), `SELECT max(attempts), sum(throttled_count) FROM messages`).Scan(&maxAttempts, &throttled); err != nil {
		t.Fatal(err)
	}
	if maxAttempts != 1 || throttled != 1 {
		t.Errorf("max attempts = %d, throttled_count total = %d, want 1 and 1", maxAttempts, throttled)
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM delivery_attempts WHERE error_class = 'throttled' AND http_status = 429`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("throttled delivery_attempts rows = %d (%v), want 1", rows, err)
	}
}

func TestThrottle_TooManyThrottlesFails_RSND4(t *testing.T) {
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 1))
	client := &scriptedClient{fn: func(int, SendRequest) (SendResult, error) {
		return SendResult{Status: 429, Header: http.Header{"Retry-After": {"1"}}}, nil
	}}
	d := fastDispatcher(pool, SMS, client)
	d.Policy.MaxRetryAfter = time.Millisecond
	d.Policy.MaxThrottles = 3
	startDispatcher(t, d)

	eventually(t, 10*time.Second, "message failed after the throttle cap", func() bool {
		return statusCounts(t, pool, camp, SMS)[StatusFailed] == 1
	})
	var attempts, throttled int
	var lastErr string
	if err := pool.QueryRow(context.Background(), `SELECT attempts, throttled_count, last_error FROM messages`).Scan(&attempts, &throttled, &lastErr); err != nil {
		t.Fatal(err)
	}
	if throttled != 4 || attempts != 0 || lastErr != "too_many_throttles" {
		t.Fatalf("attempts=%d throttled=%d last_error=%q, want 0, 4 (cap 3 exceeded) and too_many_throttles", attempts, throttled, lastErr)
	}
}

func TestExhaustedAttempts_Failed_RSND4(t *testing.T) {
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 3))
	client := &scriptedClient{fn: func(int, SendRequest) (SendResult, error) { return SendResult{Status: 503}, nil }}
	d := fastDispatcher(pool, SMS, client)
	d.Metrics = metrics.New()
	startDispatcher(t, d)

	eventually(t, 10*time.Second, "all messages failed", func() bool {
		return statusCounts(t, pool, camp, SMS)[StatusFailed] == 3
	})
	rows, err := pool.Query(context.Background(), `SELECT m.attempts, m.last_error, count(a.id)
		FROM messages m JOIN delivery_attempts a ON a.message_id = m.id GROUP BY m.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var attempts, recorded int
		var lastErr string
		if err := rows.Scan(&attempts, &lastErr, &recorded); err != nil {
			t.Fatal(err)
		}
		if attempts != 5 || recorded != 5 || !strings.HasPrefix(lastErr, "http_503") {
			t.Errorf("attempts=%d recorded=%d last_error=%q, want 5, 5 and http_503...", attempts, recorded, lastErr)
		}
		n++
	}
	if n != 3 {
		t.Fatalf("%d messages inspected", n)
	}
	if got := len(client.snapshot()); got != 15 {
		t.Errorf("provider calls = %d, want exactly 3 x 5", got)
	}
	if got := valueOf(d.Metrics.Messages.WithLabelValues("sms", "failed")); got != 3 {
		t.Errorf("messages_total{failed} = %v, want 3", got)
	}
	if got := valueOf(d.Metrics.Retries.WithLabelValues("sms", "transient")); got != 12 {
		t.Errorf("retries_total{transient} = %v, want 12 (4 retries x 3)", got)
	}
	// With every message terminal and one failed, the reconciler closes the campaign with failures.
	if ids, err := Reconcile(context.Background(), pool); err != nil || len(ids) != 1 {
		t.Fatalf("reconcile %v %v", ids, err)
	}
	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM campaigns WHERE id = $1`, camp).Scan(&status)
	if status != "completed_with_failures" {
		t.Errorf("campaign status = %q", status)
	}
}

func TestDispatcher_RetryClasses_RSND4(t *testing.T) {
	tests := []struct {
		name       string
		answer     func(n int) (SendResult, error)
		wantStatus string
		wantTries  int // provider calls for the single message
		wantAtt    int // messages.attempts at the end
	}{
		{"permanent 400 fails at once", func(int) (SendResult, error) { return SendResult{Status: 400}, nil }, StatusFailed, 1, 1},
		{"permanent 422 fails at once", func(int) (SendResult, error) { return SendResult{Status: 422}, nil }, StatusFailed, 1, 1},
		{"timeouts retry with the same key then succeed", func(n int) (SendResult, error) {
			if n < 3 {
				return SendResult{}, context.DeadlineExceeded
			}
			return SendResult{Status: 200, ProviderMessageID: "pm"}, nil
		}, StatusSent, 3, 3},
		{"connection errors are ambiguous too", func(n int) (SendResult, error) {
			if n == 1 {
				return SendResult{}, errorf("connection reset")
			}
			return SendResult{Status: 200}, nil
		}, StatusSent, 2, 2},
		{"5xx then success", func(n int) (SendResult, error) {
			if n <= 2 {
				return SendResult{Status: 502}, nil
			}
			return SendResult{Status: 200}, nil
		}, StatusSent, 3, 3},
		{"401 pauses the channel without using attempts, then recovers", func(n int) (SendResult, error) {
			if n <= 2 {
				return SendResult{Status: 401}, nil
			}
			return SendResult{Status: 200}, nil
		}, StatusSent, 3, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool := storetest.NewDB(t)
			camp := newCampaign(t, pool)
			enqueue(t, pool, camp, SMS, newUsers(t, pool, 1))
			client := &scriptedClient{fn: func(n int, _ SendRequest) (SendResult, error) { return tc.answer(n) }}
			d := fastDispatcher(pool, SMS, client)
			d.ConfigPause = 30 * time.Millisecond
			startDispatcher(t, d)
			eventually(t, 10*time.Second, "message terminal", func() bool {
				return nonTerminal(statusCounts(t, pool, camp, SMS)) == 0
			})
			if got := statusCounts(t, pool, camp, SMS); got[tc.wantStatus] != 1 {
				t.Fatalf("status counts %v, want one %s", got, tc.wantStatus)
			}
			calls := client.snapshot()
			if len(calls) != tc.wantTries {
				t.Fatalf("provider calls = %d, want %d", len(calls), tc.wantTries)
			}
			for _, c := range calls { // R-SND-3: every retry reuses the same Idempotency-Key = the message id
				if c.Req.IdempotencyKey != calls[0].Req.IdempotencyKey || c.Req.IdempotencyKey != c.Req.MessageID {
					t.Fatalf("idempotency key changed between tries: %+v", c.Req)
				}
			}
			var att int
			if err := pool.QueryRow(context.Background(), `SELECT attempts FROM messages`).Scan(&att); err != nil || att != tc.wantAtt {
				t.Fatalf("attempts = %d (%v), want %d", att, err, tc.wantAtt)
			}
		})
	}
}

func TestDispatcher_RendersPerRecipient_RCNT3(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	users := newUsers(t, pool, 3)
	// Hostile and empty names: stay literal/escaped, single pass, fallback for empty.
	if _, err := pool.Exec(ctx, `UPDATE users SET first_name = '{{city}}' WHERE id = $1`, users[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET first_name = '<b>x</b>&"' WHERE id = $1`, users[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET first_name = '' WHERE id = $1`, users[2]); err != nil {
		t.Fatal(err)
	}
	enqueue(t, pool, camp, SMS, users)
	enqueue(t, pool, camp, Email, users)
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	startDispatcher(t, fastDispatcher(pool, SMS, client))
	startDispatcher(t, fastDispatcher(pool, Email, client))
	eventually(t, 10*time.Second, "all sent", func() bool {
		return statusCounts(t, pool, camp, SMS)[StatusSent] == 3 && statusCounts(t, pool, camp, Email)[StatusSent] == 3
	})

	byUser := map[int64]map[Channel]SendRequest{}
	for _, c := range client.snapshot() {
		if byUser[c.Req.UserID] == nil {
			byUser[c.Req.UserID] = map[Channel]SendRequest{}
		}
		byUser[c.Req.UserID][c.Req.Channel] = c.Req
	}
	if got := byUser[users[0]][SMS].Text; got != "Merhaba {{city}}, İstanbul için %20 indirim!" {
		t.Errorf("sms for {{city}} user = %q: a substituted value must not be expanded again", got)
	}
	if got := byUser[users[2]][SMS].Text; !strings.HasPrefix(got, "Merhaba Değerli müşterimiz,") {
		t.Errorf("sms for empty-name user = %q, want the locale fallback", got)
	}
	em := byUser[users[1]][Email]
	if strings.Contains(em.HTML, "<b>x") || !strings.Contains(em.HTML, "&lt;b&gt;x&lt;/b&gt;&amp;&#34;") {
		t.Errorf("email html for hostile name is not escaped: %s", em.HTML)
	}
	if em.To == "" || em.Subject == "" || !strings.Contains(em.Text, `<b>x</b>&"`) {
		t.Errorf("plain-text part must keep the raw value, subject/to must be set: %+v", em)
	}
	if byUser[users[0]][SMS].To == "" || byUser[users[0]][SMS].CampaignID != camp {
		t.Errorf("sms request fields: %+v", byUser[users[0]][SMS])
	}
}

func TestDispatcher_SMSTooLong_PermanentFailure_RSND3(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE campaign_content SET sms_text = repeat('a', 161)`); err != nil {
		t.Fatal(err)
	}
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 2))
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	d := fastDispatcher(pool, SMS, client)
	d.Metrics = metrics.New()
	startDispatcher(t, d)
	eventually(t, 10*time.Second, "failed", func() bool { return statusCounts(t, pool, camp, SMS)[StatusFailed] == 2 })
	if n := len(client.snapshot()); n != 0 {
		t.Errorf("%d over-long messages reached the provider", n)
	}
	if got := valueOf(d.Metrics.ContentTooLong.WithLabelValues("sms")); got != 2 {
		t.Errorf("content_too_long_total = %v, want 2", got)
	}
	var e string
	_ = pool.QueryRow(ctx, `SELECT last_error FROM messages LIMIT 1`).Scan(&e)
	if e != "content_too_long" {
		t.Errorf("last_error = %q", e)
	}
}

func TestDispatcher_EndToEnd_RFLOW4(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()

	prov := mockprovider.New(config.ProviderFaults{
		LatencyMinMS: 1, LatencyMaxMS: 15, ErrorRate: 0.05, Rate429: 0.02, RetryAfterS: 1, HangRate: 0.01,
		RateLimit: 100, Seed: 99,
	}, metrics.New())
	srv := httptest.NewServer(prov.Handler())
	t.Cleanup(func() { prov.Close(); srv.Close() })
	client := NewHTTPClient(srv.URL, 250*time.Millisecond)
	t.Cleanup(client.Close)

	const nUsers = 250
	camp := newCampaign(t, pool)
	users := newUsers(t, pool, nUsers)
	enqueue(t, pool, camp, SMS, users)
	enqueue(t, pool, camp, Email, users)
	invalidPhone, noSMS, noEmail := users[:6], users[10:19], users[20:27]
	for _, q := range []struct {
		sql string
		ids []int64
	}{
		{`UPDATE users SET phone = '12' WHERE id = ANY($1)`, invalidPhone},
		{`UPDATE users SET sms_consent = false WHERE id = ANY($1)`, noSMS}, // revoked after enqueue
		{`UPDATE users SET email_consent = false WHERE id = ANY($1)`, noEmail},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.ids); err != nil {
			t.Fatal(err)
		}
	}

	// Two worker replicas: every channel has one active dispatcher and one standby.
	var ds []*Dispatcher
	for _, id := range []string{"worker-a", "worker-b"} {
		for _, ch := range Channels {
			d := fastDispatcher(pool, ch, client)
			d.WorkerID, d.Metrics, d.RateLimit = id, metrics.New(), 100
			ds = append(ds, d)
			startDispatcher(t, d)
		}
	}
	eventually(t, 60*time.Second, "every message terminal", func() bool {
		return nonTerminal(statusCounts(t, pool, camp, SMS))+nonTerminal(statusCounts(t, pool, camp, Email)) == 0
	})

	sms, email := statusCounts(t, pool, camp, SMS), statusCounts(t, pool, camp, Email)
	if sms[StatusSent] != nUsers-9-6 || sms[StatusSkippedNC] != 9 || sms[StatusFailed] != 6 {
		t.Errorf("sms outcome %v, want %d sent, 9 skipped, 6 failed (invalid number)", sms, nUsers-15)
	}
	if email[StatusSent] != nUsers-7 || email[StatusSkippedNC] != 7 || email[StatusFailed] != 0 {
		t.Errorf("email outcome %v, want %d sent and 7 skipped", email, nUsers-7)
	}

	resp, err := http.Get(srv.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stats struct {
		Channels map[string]mockprovider.ChannelStats `json:"channels"`
		Total    mockprovider.ChannelStats            `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	t.Logf("provider stats: sms=%+v email=%+v", stats.Channels["sms"], stats.Channels["email"])
	for ch, s := range stats.Channels {
		if s.DuplicateRecipientDeliveries != 0 { // I1
			t.Errorf("%s: duplicate_recipient_deliveries = %d", ch, s.DuplicateRecipientDeliveries)
		}
		if s.MaxRequestsInAny1sWindow > 100 { // I3, measured on arrivals including retries
			t.Errorf("%s: max requests in any 1 s window = %d, limit 100", ch, s.MaxRequestsInAny1sWindow)
		}
		if s.RejectedByLimiter != 0 {
			t.Errorf("%s: provider limiter rejected %d requests; the client must stay under it", ch, s.RejectedByLimiter)
		}
	}
	// I5: DB, provider and metrics agree.
	sent := map[string]float64{}
	for _, d := range ds {
		for _, ch := range Channels {
			sent[string(ch)] += valueOf(d.Metrics.Messages.WithLabelValues(string(ch), StatusSent))
		}
	}
	if stats.Channels["sms"].Accepted != sms[StatusSent] || stats.Channels["email"].Accepted != email[StatusSent] {
		t.Errorf("provider accepted sms=%d email=%d, DB sent sms=%d email=%d", stats.Channels["sms"].Accepted, stats.Channels["email"].Accepted, sms[StatusSent], email[StatusSent])
	}
	if int(sent["sms"]) != sms[StatusSent] || int(sent["email"]) != email[StatusSent] {
		t.Errorf("messages_total{sent} sms=%v email=%v, DB sms=%d email=%d", sent["sms"], sent["email"], sms[StatusSent], email[StatusSent])
	}

	// I2: nobody without consent was delivered.
	for ch, col := range map[string]string{"sms": "sms_consent", "email": "email_consent"} {
		resp, err := http.Get(srv.URL + "/stats/delivered?channel=" + ch + "&campaign_id=" + camp)
		if err != nil {
			t.Fatal(err)
		}
		var list struct {
			Items []struct {
				UserID int64 `json:"user_id"`
			} `json:"items"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, len(list.Items))
		for i, it := range list.Items {
			ids[i] = it.UserID
		}
		var bad int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = ANY($1) AND NOT `+col, ids).Scan(&bad); err != nil || bad != 0 {
			t.Errorf("%s: %d deliveries to users without consent (%v)", ch, bad, err)
		}
	}
	// I6: attempts within policy.
	var maxAtt int
	if err := pool.QueryRow(ctx, `SELECT max(attempts) FROM messages`).Scan(&maxAtt); err != nil || maxAtt > 5 {
		t.Errorf("max attempts = %d (%v)", maxAtt, err)
	}

	if ids, err := Reconcile(ctx, pool); err != nil || len(ids) != 1 {
		t.Fatalf("reconcile %v %v", ids, err)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, camp).Scan(&status)
	if status != "completed_with_failures" {
		t.Errorf("campaign status = %q, want completed_with_failures (6 invalid numbers)", status)
	}
}

func TestAdvisoryLock_SingleActiveDispatcher_RSND2(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	a, b := fastDispatcher(pool, SMS, client), fastDispatcher(pool, SMS, client)
	a.WorkerID, b.WorkerID = "worker-a", "worker-b"
	other := fastDispatcher(pool, Email, client) // the email channel has its own lock
	other.WorkerID = "worker-c"

	stopA := startDispatcher(t, a)
	eventually(t, 5*time.Second, "a holds the sms lock", a.HoldsLock)
	startDispatcher(t, b)
	startDispatcher(t, other)
	eventually(t, 5*time.Second, "email dispatcher holds its own lock", other.HoldsLock)

	// While a holds the lock, b's heartbeat row exists and says it does not.
	eventually(t, 5*time.Second, "heartbeat rows: a holds, b does not", func() bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM worker_heartbeats WHERE channel = 'sms'
			AND ((worker_id = 'worker-a' AND holds_lock) OR (worker_id = 'worker-b' AND NOT holds_lock))`).Scan(&n)
		return n == 2
	})
	rows, err := pool.Query(ctx, `SELECT worker_id, holds_lock FROM worker_heartbeats WHERE channel = 'sms'`)
	if err != nil {
		t.Fatal(err)
	}
	holders := map[string]bool{}
	for rows.Next() {
		var id string
		var holds bool
		if err := rows.Scan(&id, &holds); err != nil {
			t.Fatal(err)
		}
		holders[id] = holds
	}
	rows.Close()
	if !holders["worker-a"] || holders["worker-b"] {
		t.Fatalf("heartbeat holds_lock = %v, want only worker-a", holders)
	}
	if b.HoldsLock() {
		t.Fatal("standby holds the lock too")
	}
	var free bool
	if err := pool.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, LockKey(SMS)).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free {
		t.Fatal("the sms advisory lock was free while the dispatcher was active")
	}

	// The holder stops: the standby takes over and its heartbeat flips.
	stopA()
	if a.HoldsLock() {
		t.Fatal("stopped dispatcher still reports the lock")
	}
	eventually(t, 5*time.Second, "b takes over", b.HoldsLock)
	eventually(t, 5*time.Second, "b heartbeat says holds_lock", func() bool {
		var holds bool
		err := pool.QueryRow(ctx, `SELECT holds_lock FROM worker_heartbeats WHERE worker_id = 'worker-b' AND channel = 'sms'`).Scan(&holds)
		return err == nil && holds
	})
	var ghost int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM worker_heartbeats WHERE worker_id = 'worker-a'`).Scan(&ghost)
	if ghost != 0 {
		t.Errorf("a clean shutdown must remove its heartbeat row, found %d", ghost)
	}
}

func TestAdvisoryLock_LostConnectionIsReacquired_RSND2(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	d := fastDispatcher(pool, SMS, client)
	startDispatcher(t, d)
	eventually(t, 5*time.Second, "lock held", d.HoldsLock)

	// Kill the session that holds the lock, as a database restart or network drop would.
	if _, err := pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND granted AND ((classid::bigint << 32) | objid::bigint) = $1`, LockKey(SMS)); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "dispatcher noticed the loss", func() bool { return !d.HoldsLock() })
	eventually(t, 10*time.Second, "dispatcher re-acquired the lock", d.HoldsLock)

	// And it still delivers.
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 5))
	eventually(t, 10*time.Second, "messages sent after recovery", func() bool {
		return statusCounts(t, pool, camp, SMS)[StatusSent] == 5
	})
}

// blockingLimiter never admits a send until its context ends, like a limiter with a long queue.
type blockingLimiter struct{}

func (blockingLimiter) Wait(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

func TestShutdown_ReleasesLeases(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 12))
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	d := fastDispatcher(pool, SMS, client)
	d.Limiter, d.MaxInFlight = blockingLimiter{}, 5
	stop := startDispatcher(t, d)

	eventually(t, 5*time.Second, "5 messages claimed and waiting for the limiter", func() bool {
		return statusCounts(t, pool, camp, SMS)[StatusSending] == 5
	})
	stop()

	counts := statusCounts(t, pool, camp, SMS)
	if counts[StatusQueued] != 12 || counts[StatusSending] != 0 {
		t.Fatalf("after shutdown: %v, want all 12 back in queued", counts)
	}
	var attempts, leases int
	if err := pool.QueryRow(ctx, `SELECT sum(attempts), count(lease_until) FROM messages`).Scan(&attempts, &leases); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || leases != 0 {
		t.Fatalf("released messages keep attempts=%d leases=%d; an unsent message must not count a try", attempts, leases)
	}
	if n := len(client.snapshot()); n != 0 {
		t.Fatalf("%d sends happened", n)
	}
}

func TestShutdown_DrainsInFlightSend(t *testing.T) {
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 1))
	entered, release := make(chan struct{}), make(chan struct{})
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) {
		close(entered)
		<-release
		return ok200(r)
	}}
	d := fastDispatcher(pool, SMS, client)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx) }()
	<-entered
	cancel() // SIGTERM while the request is on the wire
	select {
	case <-done:
		t.Fatal("Run returned while a send was still in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-done
	if got := statusCounts(t, pool, camp, SMS); got[StatusSent] != 1 {
		t.Fatalf("after drain: %v, want the in-flight message recorded as sent", got)
	}
}

func TestWorkerMetrics_Exposed_ROBS2(t *testing.T) {
	pool := storetest.NewDB(t)
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	m := metrics.New()
	var ds []*Dispatcher
	for _, ch := range Channels {
		d := fastDispatcher(pool, ch, client)
		d.Metrics = m
		ds = append(ds, d)
	}
	h := httptest.NewServer(ReadyHandler(pool, m, ds...))
	t.Cleanup(h.Close)

	get := func(path string) (int, string) {
		resp, err := http.Get(h.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, body := get("/healthz"); code != 200 {
		t.Fatalf("healthz %d %s", code, body)
	}
	// R-OBS-3: not ready before any dispatcher loop has beaten.
	if code, body := get("/readyz"); code != 503 || !strings.Contains(body, "dispatcher") {
		t.Fatalf("readyz before start: %d %s", code, body)
	}
	for _, d := range ds {
		startDispatcher(t, d)
	}
	eventually(t, 5*time.Second, "readyz 200", func() bool { c, _ := get("/readyz"); return c == 200 })

	// R-OBS-2: every series of the contract exists at zero before any traffic.
	_, text := get("/metrics")
	for _, ch := range []string{"sms", "email"} {
		for _, want := range []string{
			`messages_total{channel="` + ch + `",status="sent"} 0`,
			`messages_total{channel="` + ch + `",status="failed"} 0`,
			`messages_total{channel="` + ch + `",status="skipped_no_consent"} 0`,
			`messages_total{channel="` + ch + `",status="retry"} 0`,
			`queue_depth{channel="` + ch + `",status="queued"} 0`,
			`queue_depth{channel="` + ch + `",status="retry"} 0`,
			`queue_depth{channel="` + ch + `",status="sending"} 0`,
			`retries_total{channel="` + ch + `",class="throttled"} 0`,
			`retries_total{channel="` + ch + `",class="transient"} 0`,
			`retries_total{channel="` + ch + `",class="ambiguous"} 0`,
			`provider_responses_total{channel="` + ch + `",code="200"} 0`,
			`provider_responses_total{channel="` + ch + `",code="timeout"} 0`,
			`provider_responses_total{channel="` + ch + `",code="conn_error"} 0`,
			`send_latency_seconds_count{channel="` + ch + `"} 0`,
			`rate_limiter_wait_seconds_count{channel="` + ch + `"} 0`,
			`channel_paused{channel="` + ch + `"} 0`,
			`content_too_long_total{channel="` + ch + `"} 0`,
		} {
			if !strings.Contains(text, want+"\n") {
				t.Errorf("/metrics lacks %q", want)
			}
		}
	}
	for _, want := range []string{"go_goroutines", "process_cpu_seconds_total"} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics lacks runtime collector %s", want)
		}
	}

	// Traffic moves the counters.
	camp := newCampaign(t, pool)
	enqueue(t, pool, camp, SMS, newUsers(t, pool, 4))
	eventually(t, 10*time.Second, "4 sent", func() bool { return statusCounts(t, pool, camp, SMS)[StatusSent] == 4 })
	_, text = get("/metrics")
	for _, want := range []string{`messages_total{channel="sms",status="sent"} 4`, `provider_responses_total{channel="sms",code="200"} 4`, `send_latency_seconds_count{channel="sms"} 4`, `rate_limiter_wait_seconds_count{channel="sms"} 4`} {
		if !strings.Contains(text, want+"\n") {
			t.Errorf("after traffic /metrics lacks %q", want)
		}
	}
	// queue_depth is sampled by the heartbeat loop and drops back to zero once the queue is empty.
	eventually(t, 5*time.Second, "queue depth sampled", func() bool {
		return valueOf(m.QueueDepth.WithLabelValues("sms", "sending")) == 0 &&
			valueOf(m.QueueDepth.WithLabelValues("sms", "queued")) == 0
	})

	// R-OBS-3: with the database gone, readiness turns red while liveness stays green.
	pool.Close()
	if code, body := get("/readyz"); code != 503 || !strings.Contains(body, `"db"`) {
		t.Fatalf("readyz with closed pool: %d %s", code, body)
	}
	if code, _ := get("/healthz"); code != 200 {
		t.Fatalf("healthz with closed pool: %d", code)
	}
}

// valueOf reads a counter or gauge.
func valueOf(m prometheus.Metric) float64 {
	var out dto.Metric
	if err := m.Write(&out); err != nil {
		panic(err)
	}
	if out.Counter != nil {
		return out.Counter.GetValue()
	}
	return out.Gauge.GetValue()
}

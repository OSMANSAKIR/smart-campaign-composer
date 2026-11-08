package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/goleak"

	"smartcampaign/internal/clock"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/seed"
	"smartcampaign/internal/segment"
	"smartcampaign/internal/store/storetest"
)

// goldenRule is the official brief's rule (docs/contracts/segment-dsl.md §7).
const goldenRule = `{"groups":[
	{"conditions":[{"field":"city","op":"eq","value":"İstanbul"}]},
	{"conditions":[{"field":"last_cart_add_at","op":"within_last_days","value":30}]},
	{"conditions":[{"field":"cart_abandoned","op":"is_true","value":null}]}]}`

func newService(t *testing.T, users int) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := storetest.NewDB(t)
	if users > 0 {
		if _, err := seed.Run(context.Background(), pool, seed.Generate(users, 20261108, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	return &Service{Pool: pool, LLM: llm.NewService(mock.NewDemo(), nil), Metrics: metrics.New()}, pool
}

// needValidators skips tests that depend on the llm builder's segment and content validators until they are
// merged (W1). Remove at merge.
func needValidators(t *testing.T) {
	t.Helper()
	var r segment.Rule
	if err := json.Unmarshal([]byte(goldenRule), &r); err != nil {
		t.Fatal(err)
	}
	_, errs, _ := segment.Validate(r)
	rep := content.Validate(content.Content{SMSText: "x", EmailSubject: "x", EmailBody: "x"}, content.Constraints{Locale: "tr"})
	for _, e := range append(errs, contentIssues(rep.Errors)...) {
		if e.Code == "not_implemented" {
			t.Skip("segment/content validators arrive with the llm builder's branch")
		}
	}
}

func codeOf(err error) string {
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	if err == nil {
		return ""
	}
	return "unexpected: " + err.Error()
}

func TestCreate_BriefLimits_RFLOW1(t *testing.T) {
	svc, _ := newService(t, 0)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		brief, code string
	}{
		"empty":     {"   ", "validation_failed"},
		"too long":  {strings.Repeat("ş", 4001), "validation_failed"},
		"max":       {strings.Repeat("ş", 4000), ""},
		"plain":     {"İstanbul'daki kullanıcılara indirim", ""},
		"multiline": {"line one\nline two", ""},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := svc.Create(ctx, tc.brief)
			if got := codeOf(err); got != tc.code {
				t.Fatalf("code %q, want %q", got, tc.code)
			}
			if err == nil && (c.Status != StatusDraft || c.Segment != nil || c.Content != nil || c.Channels == nil) {
				t.Fatalf("unexpected campaign %+v", c)
			}
		})
	}
	if _, err := svc.Get(ctx, "not-a-uuid"); codeOf(err) != "not_found" {
		t.Fatalf("malformed id: %v", err)
	}
	if _, err := svc.Get(ctx, "00000000-0000-0000-0000-000000000000"); codeOf(err) != "not_found" {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestList_CursorPagination(t *testing.T) {
	svc, _ := newService(t, 0)
	ctx := context.Background()
	for range 5 {
		if _, err := svc.Create(ctx, "brief"); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor, pages := "", 0
	var prev time.Time
	for {
		items, next, err := svc.List(ctx, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("duplicate %s", it.ID)
			}
			if !prev.IsZero() && it.CreatedAt.After(prev) {
				t.Fatal("not newest first")
			}
			seen[it.ID], prev = true, it.CreatedAt
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("seen %d in %d pages", len(seen), pages)
	}
	if _, _, err := svc.List(ctx, 2, "garbage"); codeOf(err) != "bad_request" {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestApprove_Preconditions_RFLOW3(t *testing.T) {
	svc, _ := newService(t, 0)
	ctx := context.Background()
	c, err := svc.Create(ctx, "brief")
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		key      string
		channels []string
		code     string
	}{
		"no key":        {"", []string{"sms"}, "idempotency_key_required"},
		"long key":      {strings.Repeat("k", 201), []string{"sms"}, "bad_request"},
		"no channels":   {"k1", nil, "bad_request"},
		"bad channel":   {"k1", []string{"fax"}, "bad_request"},
		"draft":         {"k1", []string{"sms"}, "invalid_state"},
		"draft deduped": {"k1", []string{"email", "sms", "sms"}, "invalid_state"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Approve(ctx, c.ID, tc.key, tc.channels, 1); codeOf(err) != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
		})
	}
	if _, err := svc.Approve(ctx, "00000000-0000-0000-0000-000000000000", "k", []string{"sms"}, 1); codeOf(err) != "not_found" {
		t.Fatalf("unknown campaign: %v", err)
	}
}

// readyCampaign creates a campaign with the golden rule and one saved content version.
func readyCampaign(t *testing.T, svc *Service, c content.Content) Campaign {
	t.Helper()
	ctx := context.Background()
	camp, err := svc.Create(ctx, "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim")
	if err != nil {
		t.Fatal(err)
	}
	var r segment.Rule
	if err := json.Unmarshal([]byte(goldenRule), &r); err != nil {
		t.Fatal(err)
	}
	if camp, err = svc.PutSegment(ctx, camp.ID, r); err != nil {
		t.Fatal(err)
	}
	if camp.Status != StatusSegmented || camp.Segment == nil || camp.Segment.Audience.Total == 0 {
		t.Fatalf("segment not stored: %+v", camp.Segment)
	}
	if _, err := svc.SaveContent(ctx, camp.ID, c, 0); err != nil {
		t.Fatal(err)
	}
	camp, err = svc.Get(ctx, camp.ID)
	if err != nil {
		t.Fatal(err)
	}
	return camp
}

var validContent = content.Content{
	SMSText:      "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor: bugüne özel %30 indirim!",
	EmailSubject: "{{first_name}}, sepetin seni bekliyor",
	EmailBody:    "Merhaba {{first_name}},\n\nSepetine eklediğin ürünlerde bugüne özel %30 indirim var.",
}

func TestApprove_EnqueuesConsentedOnly_RAUD2(t *testing.T) {
	needValidators(t)
	svc, pool := newService(t, 2000)
	ctx := context.Background()
	camp := readyCampaign(t, svc, validContent)
	res, err := svc.Approve(ctx, camp.ID, "key-1", []string{"sms", "email"}, camp.Content.Version)
	if err != nil {
		t.Fatal(err)
	}
	where, args, err := segment.Compile(mustValid(t, goldenRule), 1)
	if err != nil {
		t.Fatal(err)
	}
	for ch, col := range consentColumn {
		var want, got int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM users u WHERE (`+where+`) AND `+col, args...).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages m JOIN users u ON u.id = m.user_id
			WHERE m.campaign_id = $1 AND m.channel = $2 AND NOT `+col, camp.ID, ch).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != 0 {
			t.Fatalf("%s: %d messages for users without consent", ch, got)
		}
		if res.Enqueued[ch] != want || want == 0 {
			t.Fatalf("%s: enqueued %d, direct SQL %d", ch, res.Enqueued[ch], want)
		}
	}
	if res.Campaign.Status != StatusSending || res.Campaign.ApprovedContentVersion == nil {
		t.Fatalf("campaign after approve: %+v", res.Campaign)
	}
}

func TestApprove_IdempotentReplay_RSND3(t *testing.T) {
	needValidators(t)
	svc, pool := newService(t, 2000)
	ctx := context.Background()
	camp := readyCampaign(t, svc, validContent)
	first, err := svc.Approve(ctx, camp.ID, "key-1", []string{"sms"}, camp.Content.Version)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.Approve(ctx, camp.ID, "key-1", []string{"sms"}, camp.Content.Version)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := mustJSON(first), mustJSON(again); string(a) != string(b) {
		t.Fatalf("replay differs:\n%s\n%s", a, b)
	}
	if _, err := svc.Approve(ctx, camp.ID, "key-2", []string{"sms"}, camp.Content.Version); codeOf(err) != "already_approved" {
		t.Fatalf("different key: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE campaign_id = $1`, camp.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != first.Enqueued["sms"] {
		t.Fatalf("%d messages, enqueued %d", n, first.Enqueued["sms"])
	}
}

func TestApprove_ConcurrentOneWins_RFLOW3(t *testing.T) {
	needValidators(t)
	svc, pool := newService(t, 2000)
	ctx := context.Background()
	camp := readyCampaign(t, svc, validContent)
	const n = 12
	var wg sync.WaitGroup
	codes := make([]string, n)
	for i := range n {
		wg.Go(func() {
			_, err := svc.Approve(ctx, camp.ID, "key-"+string(rune('a'+i)), []string{"sms", "email"}, camp.Content.Version)
			codes[i] = codeOf(err)
		})
	}
	wg.Wait()
	wins := 0
	for _, c := range codes {
		switch c {
		case "":
			wins++
		case "approve_in_progress", "already_approved":
		default:
			t.Fatalf("unexpected outcome %q", c)
		}
	}
	if wins != 1 {
		t.Fatalf("%d winners: %v", wins, codes)
	}
	var dup int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM messages WHERE campaign_id = $1
		GROUP BY user_id, channel HAVING count(*) > 1) d`, camp.ID).Scan(&dup); err != nil {
		t.Fatal(err)
	}
	if dup != 0 {
		t.Fatalf("%d duplicate (user, channel) pairs", dup)
	}
}

func TestApprove_Gating_RFLOW3(t *testing.T) {
	needValidators(t)
	svc, _ := newService(t, 2000)
	ctx := context.Background()

	t.Run("stale version", func(t *testing.T) {
		camp := readyCampaign(t, svc, validContent)
		if _, err := svc.Approve(ctx, camp.ID, "k-stale", []string{"sms"}, camp.Content.Version+1); codeOf(err) != "stale_content_version" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("sms too long", func(t *testing.T) {
		long := validContent
		long.SMSText = strings.Repeat("ş", 161)
		camp := readyCampaign(t, svc, long)
		if _, err := svc.Approve(ctx, camp.ID, "k-long", []string{"sms"}, camp.Content.Version); codeOf(err) != "content_invalid" {
			t.Fatalf("got %v", err)
		}
		if _, err := svc.Approve(ctx, camp.ID, "k-email", []string{"email"}, camp.Content.Version); err != nil {
			t.Fatalf("email-only approve with invalid sms: %v", err)
		}
	})
	t.Run("save with old base", func(t *testing.T) {
		camp := readyCampaign(t, svc, validContent)
		if _, err := svc.SaveContent(ctx, camp.ID, validContent, 0); codeOf(err) != "stale_content_version" {
			t.Fatalf("got %v", err)
		}
		cv, err := svc.SaveContent(ctx, camp.ID, validContent, camp.Content.Version)
		if err != nil || cv.Version != camp.Content.Version+1 || cv.Source != "edited" {
			t.Fatalf("save: %+v %v", cv, err)
		}
	})
	t.Run("empty audience", func(t *testing.T) {
		camp := readyCampaign(t, svc, validContent)
		var r segment.Rule
		if err := json.Unmarshal([]byte(`{"groups":[{"conditions":[{"field":"purchase_count","op":"gt","value":100000}]}]}`), &r); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.PutSegment(ctx, camp.ID, r); err != nil {
			t.Fatal(err)
		}
		cv, err := svc.SaveContent(ctx, camp.ID, validContent, camp.Content.Version)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Approve(ctx, camp.ID, "k-empty", []string{"sms"}, cv.Version); codeOf(err) != "empty_audience" {
			t.Fatalf("got %v", err)
		}
	})
}

func mustValid(t *testing.T, raw string) segment.ValidRule {
	t.Helper()
	var r segment.Rule
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	v, errs, _ := segment.Validate(r)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return v
}

// sendingCampaign inserts an approved campaign and messages in the given statuses directly.
func sendingCampaign(t *testing.T, pool *pgxpool.Pool, statuses map[string][]string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, status, channels, approved_at) VALUES ('b', 'sending', '{sms,email}', now())
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	user := 0
	for ch, list := range statuses {
		for _, st := range list {
			user++
			sentAt := "NULL"
			if st == "sent" {
				sentAt = "now()"
			}
			if _, err := pool.Exec(ctx, `INSERT INTO messages (campaign_id, user_id, channel, content_version, status, sent_at)
				SELECT $1, id, $2, 1, $3, `+sentAt+` FROM users ORDER BY id OFFSET $4 LIMIT 1`, id, ch, st, user); err != nil {
				t.Fatal(err)
			}
		}
	}
	return id
}

func TestProgress_Snapshot_RFLOW5(t *testing.T) {
	svc, pool := newService(t, 50)
	ctx := context.Background()
	id := sendingCampaign(t, pool, map[string][]string{
		"sms":   {"queued", "sending", "sent", "sent", "retry", "failed", "skipped_no_consent"},
		"email": {"sent", "queued"},
	})
	if _, err := pool.Exec(ctx, `INSERT INTO delivery_attempts (message_id, attempt, http_status, error_class, latency_ms)
		SELECT id, 1, 429, 'throttled', 5 FROM messages WHERE campaign_id = $1 AND status = 'retry'`, id); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Progress(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sms := p.Channels["sms"]
	if sms.Total != 7 || sms.Queued != 1 || sms.Sending != 1 || sms.Sent != 2 || sms.Retry != 1 || sms.Failed != 1 || sms.SkippedNoConsent != 1 {
		t.Fatalf("sms %+v", sms)
	}
	if p.Channels["email"].Total != 2 || p.Errors["rate_limited"] != 1 || p.Status != StatusSending || p.StartedAt == nil {
		t.Fatalf("progress %+v", p)
	}
	if sms.ThroughputPerS <= 0 || p.ETASeconds == nil {
		t.Fatalf("throughput %v eta %v", sms.ThroughputPerS, p.ETASeconds)
	}
	if _, err := svc.Progress(ctx, "00000000-0000-0000-0000-000000000000"); codeOf(err) != "not_found" {
		t.Fatalf("unknown: %v", err)
	}
}

func TestHub_SharedPollAndDone_RFLOW5(t *testing.T) {
	svc, pool := newService(t, 20)
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent()) // the pool's own goroutines exist already
	ctx := context.Background()
	id := sendingCampaign(t, pool, map[string][]string{"sms": {"queued", "sent"}})
	fc := clock.NewFake(time.Now())
	hub := NewHub(svc, fc, 500*time.Millisecond)
	defer hub.Close()

	a, unsubA := hub.Subscribe(id)
	b, unsubB := hub.Subscribe(id)
	defer unsubB()
	waitFor(t, func() bool { return fc.Waiters() == 1 }) // one poller for two subscribers
	fc.Advance(500 * time.Millisecond)
	for _, s := range []*Sub{a, b} {
		u := next(t, s)
		if u.Snapshot == nil || u.Snapshot.Channels["sms"].Total != 2 || u.Done {
			t.Fatalf("first update %+v", u)
		}
	}
	unsubA()

	if _, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'completed' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return fc.Waiters() == 1 })
	fc.Advance(500 * time.Millisecond)
	if u := next(t, b); !u.Done || u.Snapshot == nil || u.Snapshot.Status != StatusCompleted {
		t.Fatalf("done update %+v", u)
	}
}

func next(t *testing.T, s *Sub) Update {
	t.Helper()
	select {
	case <-s.C():
		u, _ := s.Take()
		return u
	case <-time.After(5 * time.Second):
		t.Fatal("no update")
		return Update{}
	}
}

// waitFor spins until a condition owned by another goroutine holds (the fake clock's waiter count).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		runtime.Gosched()
	}
}

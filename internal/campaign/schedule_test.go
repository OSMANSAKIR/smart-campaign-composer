package campaign

import (
	"context"
	"testing"
	"time"

	"smartcampaign/internal/delivery"
)

func TestApprove_SendAtWindow_B1(t *testing.T) {
	svc, _ := newService(t, 300)
	ctx := context.Background()
	c, v := everyoneCampaign(t, svc, validContent)
	// The window is checked against the database clock, which is this machine's clock in tests; the
	// margins below are far larger than any skew.
	for name, tc := range map[string]struct {
		at   time.Duration
		code string
	}{
		"past":              {-time.Hour, "invalid_send_at"},
		"now":               {0, "invalid_send_at"},
		"under the minimum": {30 * time.Second, "invalid_send_at"},
		"just under 50 s":   {45 * time.Second, "invalid_send_at"},
		"beyond seven days": {7*24*time.Hour + time.Minute, "invalid_send_at"},
		"far in the future": {400 * 24 * time.Hour, "invalid_send_at"},
	} {
		t.Run(name, func(t *testing.T) {
			at := time.Now().Add(tc.at)
			_, err := svc.ApproveWith(ctx, c.ID, "k-"+name, []string{"sms"}, v, ApproveOptions{SendAt: &at})
			if codeOf(err) != tc.code {
				t.Fatalf("send_at %v from now: %v, want %s", tc.at, err, tc.code)
			}
		})
	}
	// The failed attempts changed nothing: the campaign is still approvable, now and later.
	var n int
	if err := svc.Pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE campaign_id = $1`, c.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d messages after rejected approves (%v)", n, err)
	}
	for name, d := range map[string]time.Duration{"one minute": time.Minute, "seven days minus an hour": 7*24*time.Hour - time.Hour} {
		t.Run("accepts "+name, func(t *testing.T) {
			c, v := everyoneCampaign(t, svc, validContent)
			at := time.Now().Add(d)
			res, err := svc.ApproveWith(ctx, c.ID, "k-"+name, []string{"sms"}, v, ApproveOptions{SendAt: &at})
			if err != nil || res.Campaign.Status != StatusScheduled {
				t.Fatalf("send_at in %v: %v status %s", d, err, res.Campaign.Status)
			}
		})
	}
}

// TestApprove_ScheduledEnqueuesNowAndWaits_B1: approve with send_at enqueues the whole audience at once
// (frozen at approve), stores scheduled_at in UTC whatever offset the client used, parks the campaign in
// `scheduled`, and nothing is claimable until the scheduler moves it; cancel works on it.
func TestApprove_ScheduledEnqueuesNowAndWaits_B1(t *testing.T) {
	svc, pool := newService(t, 300)
	ctx := context.Background()
	c, v := everyoneCampaign(t, svc, validContent)

	istanbul := time.FixedZone("+03:00", 3*3600)
	at := time.Now().Add(10 * time.Minute).In(istanbul).Truncate(time.Millisecond)
	res, err := svc.ApproveWith(ctx, c.ID, "sched-1", []string{"sms", "email"}, v, ApproveOptions{SendAt: &at})
	if err != nil {
		t.Fatal(err)
	}
	if res.Campaign.Status != StatusScheduled || res.Campaign.ScheduledAt == nil || !res.Campaign.ScheduledAt.Equal(at) {
		t.Fatalf("campaign %s scheduled_at %v, want scheduled at %v", res.Campaign.Status, res.Campaign.ScheduledAt, at)
	}
	if res.Campaign.ScheduledAt.Location() != time.UTC {
		t.Fatalf("scheduled_at is not UTC: %v", res.Campaign.ScheduledAt.Location())
	}
	if res.Enqueued["sms"] == 0 || res.Enqueued["email"] == 0 {
		t.Fatalf("nothing enqueued: %v", res.Enqueued)
	}
	var queued, other int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'queued'), count(*) FILTER (WHERE status <> 'queued')
		FROM messages WHERE campaign_id = $1`, c.ID).Scan(&queued, &other); err != nil || other != 0 || queued != res.Enqueued["sms"]+res.Enqueued["email"] {
		t.Fatalf("queued %d other %d (%v)", queued, other, err)
	}
	for _, ch := range []delivery.Channel{delivery.SMS, delivery.Email} {
		got, err := (&delivery.Queue{Pool: pool, Channel: ch, Lease: time.Minute}).Claim(ctx, 100)
		if err != nil || len(got) != 0 {
			t.Fatalf("%s: claimed %d messages of a scheduled campaign (%v)", ch, len(got), err)
		}
	}
	// The same key replays the stored answer, scheduled status included.
	again, err := svc.ApproveWith(ctx, c.ID, "sched-1", []string{"sms", "email"}, v, ApproveOptions{SendAt: &at})
	if err != nil || again.Campaign.Status != StatusScheduled {
		t.Fatalf("replay: %v %+v", err, again.Campaign.Status)
	}

	// Cancel while scheduled: every message is still queued, so all of them are cancelled.
	cr, err := svc.Cancel(ctx, c.ID)
	if err != nil || cr.Cancelled != queued || cr.Campaign.Status != StatusCancelled {
		t.Fatalf("cancel of a scheduled campaign: %+v %v (want %d)", cr.Cancelled, err, queued)
	}
	if ids, err := delivery.Activate(ctx, pool); err != nil || len(ids) != 0 {
		t.Fatalf("scheduler revived a cancelled campaign: %v %v", ids, err)
	}
}

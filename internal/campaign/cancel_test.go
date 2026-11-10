package campaign

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"smartcampaign/internal/clock"
)

func TestCancel_IdempotentAndCountsMoved_F4(t *testing.T) {
	svc, pool := newService(t, 60)
	ctx := context.Background()
	id := sendingCampaign(t, pool, map[string][]string{
		"sms":   {"queued", "queued", "retry", "sending", "sent", "failed", "skipped_no_consent"},
		"email": {"queued", "sent"},
	})
	res, err := svc.Cancel(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// queued + retry move; a message already claimed (sending) and the terminal ones stay as they are.
	if res.Cancelled != 4 || res.Campaign.Status != StatusCancelled || res.Campaign.CancelledAt == nil {
		t.Fatalf("first cancel: moved %d, campaign %s, cancelled_at %v", res.Cancelled, res.Campaign.Status, res.Campaign.CancelledAt)
	}
	p := res.Campaign.Progress
	if p == nil || p.Channels["sms"].Cancelled != 3 || p.Channels["sms"].Sending != 1 || p.Channels["sms"].Sent != 1 || p.Channels["email"].Cancelled != 1 {
		t.Fatalf("progress after cancel: %+v", p.Channels["sms"])
	}
	again, err := svc.Cancel(ctx, id)
	if err != nil || again.Cancelled != 0 || again.Campaign.Status != StatusCancelled {
		t.Fatalf("second cancel: %+v %v", again, err)
	}
	if !again.Campaign.CancelledAt.Equal(*res.Campaign.CancelledAt) {
		t.Fatal("a repeated cancel must not move cancelled_at")
	}
}

func TestCancel_ConcurrentCallsMoveEachMessageOnce_F4(t *testing.T) {
	svc, pool := newService(t, 80)
	id := sendingCampaign(t, pool, map[string][]string{"sms": {"queued", "queued", "queued", "retry", "sent"}})
	const n = 8
	moved := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			r, err := svc.Cancel(context.Background(), id)
			moved[i], errs[i] = r.Cancelled, err
		})
	}
	wg.Wait()
	total := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		total += moved[i]
	}
	if total != 4 {
		t.Fatalf("calls moved %v messages in total, want 4", moved)
	}
}

func TestCancel_StatusGate_F4(t *testing.T) {
	svc, pool := newService(t, 10)
	ctx := context.Background()
	insert := func(status string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, status) VALUES ('b', $1) RETURNING id::text`, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for status, code := range map[string]string{
		"draft": "invalid_state", "segmented": "invalid_state", "content_ready": "invalid_state",
		"completed": "invalid_state", "completed_with_failures": "invalid_state",
		"scheduled": "", "sending": "", "cancelled": "",
	} {
		t.Run(status, func(t *testing.T) {
			if _, err := svc.Cancel(ctx, insert(status)); codeOf(err) != code {
				t.Fatalf("cancel from %s: %v, want %q", status, err, code)
			}
		})
	}
	if _, err := svc.Cancel(ctx, "00000000-0000-0000-0000-000000000000"); codeOf(err) != "not_found" {
		t.Fatalf("unknown campaign: %v", err)
	}
	if _, err := svc.Cancel(ctx, "nope"); codeOf(err) != "not_found" {
		t.Fatalf("malformed id: %v", err)
	}
}

// TestProgress_CancelledIsDoneOnlyWhenQuiet_F4: the stream ends for a cancelled campaign once no message is
// sending (or waiting), never earlier, so the final counts add up (docs/contracts/api.md §9.5).
func TestProgress_CancelledIsDoneOnlyWhenQuiet_F4(t *testing.T) {
	svc, pool := newService(t, 20)
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx := context.Background()
	id := sendingCampaign(t, pool, map[string][]string{"sms": {"sending", "sent", "queued"}})
	if _, err := svc.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Progress(ctx, id)
	if err != nil || p.Done() {
		t.Fatalf("done with a message still sending: %v %+v", err, p.Channels["sms"])
	}

	fc := clock.NewFake(time.Now())
	hub := NewHub(svc, fc, 500*time.Millisecond)
	defer hub.Close()
	sub, unsub := hub.Subscribe(id)
	defer unsub()
	waitFor(t, func() bool { return fc.Waiters() == 1 })
	fc.Advance(500 * time.Millisecond)
	if u := next(t, sub); u.Done {
		t.Fatalf("hub reported done while a message is sending: %+v", u)
	}

	if _, err := pool.Exec(ctx, `UPDATE messages SET status = 'sent', sent_at = now() WHERE campaign_id = $1 AND status = 'sending'`, id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return fc.Waiters() == 1 })
	fc.Advance(500 * time.Millisecond)
	u := next(t, sub)
	if !u.Done || u.Snapshot == nil || u.Snapshot.Status != StatusCancelled {
		t.Fatalf("want done for the quiet cancelled campaign: %+v", u)
	}
	sms := u.Snapshot.Channels["sms"]
	if sms.Sent+sms.Failed+sms.Cancelled+sms.SkippedNoConsent != sms.Total || sms.Total != 3 || sms.Cancelled != 1 {
		t.Fatalf("final counts do not add up: %+v", sms)
	}
}

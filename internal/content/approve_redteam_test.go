package content_test

// Red-team test (llm-red-team, CP1): approve must re-validate the copy against the audience as it is at
// approve time, not trust the report stored with the content version (R-FLOW-3, R-CNT-1).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/segment"
	"smartcampaign/internal/store/storetest"
)

func addUser(t *testing.T, pool *pgxpool.Pool, key, first string, sms bool) {
	t.Helper()
	now := time.Now()
	_, err := pool.Exec(context.Background(), `INSERT INTO users (seed_key, first_name, email, phone, city, locale, birth_date,
		signup_at, loyalty_tier, sms_consent, email_consent) VALUES ($1, $2, $3, '+905550000000', 'İstanbul', 'tr', $4, $5, 'bronze', $6, true)`,
		key, first, key+"@example.com", now.AddDate(-30, 0, 0), now.AddDate(-1, 0, 0), sms)
	if err != nil {
		t.Fatal(err)
	}
}

func codeOf(err error) string {
	var ce *campaign.Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	if err == nil {
		return ""
	}
	return "unexpected: " + err.Error()
}

func TestRedTeam_ApproveRevalidatesAgainstCurrentAudience_RFLOW3(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	svc := &campaign.Service{Pool: pool, LLM: llm.NewService(mock.NewScripted(), nil)}
	addUser(t, pool, "u1", "Ali", true)
	addUser(t, pool, "u2", "Ayşe", true)

	c, err := svc.Create(ctx, "İstanbul'a kısa bir SMS")
	if err != nil {
		t.Fatal(err)
	}
	rule, issues := segment.ParseRule([]byte(`{"groups":[{"conditions":[{"field":"city","op":"eq","value":"İstanbul"}]}]}`))
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	if _, err := svc.PutSegment(ctx, c.ID, rule); err != nil {
		t.Fatal(err)
	}
	// Exactly 160 runes with the longest current name ("Ayşe", 4 runes).
	sms := strings.Repeat("a", 156) + "{{first_name}}"
	cv, err := svc.SaveContent(ctx, c.ID, content.Content{SMSText: sms, EmailSubject: "s", EmailBody: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !cv.Validation.ValidFor["sms"] {
		t.Fatalf("setup: content must be valid now: %+v", cv.Validation.Errors)
	}
	// The audience changes after validation: a consented user with a 20-rune name joins the segment.
	addUser(t, pool, "u3", "Abdurrahman Kerimcan", true)

	_, err = svc.Approve(ctx, c.ID, "rt-key-1", []string{"sms"}, cv.Version)
	if got := codeOf(err); got != "content_invalid" {
		t.Fatalf("approve with a stale valid report: code %q, want content_invalid", got)
	}
	var queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued != 0 || got.Status != campaign.StatusContentReady {
		t.Fatalf("a refused approve must not enqueue or move the campaign: queued %d, status %s", queued, got.Status)
	}
	// Email is unaffected by the SMS budget and may still be approved.
	if _, err := svc.Approve(ctx, c.ID, "rt-key-2", []string{"email"}, cv.Version); err != nil {
		t.Fatalf("email approve: %v", err)
	}
}

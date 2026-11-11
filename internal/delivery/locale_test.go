package delivery

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/store/storetest"
)

// localeCampaign is a campaign in `sending` whose language is tr, with a primary (tr) copy and an English
// adaptation, 20 users (even ids tr, odd en) whose first_name is empty for every fourth one, and one SMS and
// one email message per user with the locale frozen from the user, as approve does.
func localeCampaign(t *testing.T, pool *pgxpool.Pool, mode string, withEnglish bool) (camp string, users []int64) {
	t.Helper()
	ctx := context.Background()
	camp = newCampaign(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET language_mode = $2 WHERE id = $1`, camp, mode); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE campaign_content SET sms_text = 'TR {{first_name}}', email_subject = 'TR konu {{first_name}}',
		email_body = 'TR gövde {{first_name}}' WHERE campaign_id = $1`, camp); err != nil {
		t.Fatal(err)
	}
	if withEnglish {
		if _, err := pool.Exec(ctx, `INSERT INTO campaign_content (campaign_id, version, variant, locale, adapted_from, sms_text,
				email_subject, email_body, source, validation)
			VALUES ($1, 1, 'a', 'en', 'hash', 'EN {{first_name}}', 'EN subject {{first_name}}', 'EN body {{first_name}}', 'llm', '{}')`, camp); err != nil {
			t.Fatal(err)
		}
	}
	users = newUsers(t, pool, 20)
	if _, err := pool.Exec(ctx, `UPDATE users SET locale = CASE WHEN id % 2 = 0 THEN 'tr' ELSE 'en' END,
		first_name = CASE WHEN id % 4 = 0 THEN '' ELSE first_name END WHERE id = ANY($1)`, users); err != nil {
		t.Fatal(err)
	}
	for _, ch := range Channels {
		if _, err := pool.Exec(ctx, `INSERT INTO messages (campaign_id, user_id, channel, content_version, locale)
			SELECT $1, u.id, $2, 1, u.locale FROM users u WHERE u.id = ANY($3)`, camp, string(ch), users); err != nil {
			t.Fatal(err)
		}
	}
	return camp, users
}

func userLocales(t *testing.T, pool *pgxpool.Pool, users []int64) map[int64]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id, locale, first_name FROM users WHERE id = ANY($1)`, users)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var loc, name string
		if err := rows.Scan(&id, &loc, &name); err != nil {
			t.Fatal(err)
		}
		out[id] = loc
		if name == "" {
			out[id] += "/no-name"
		}
	}
	return out
}

func runAll(t *testing.T, pool *pgxpool.Pool, camp string) *scriptedClient {
	t.Helper()
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	for _, ch := range Channels {
		startDispatcher(t, fastDispatcher(pool, ch, client))
	}
	eventually(t, 30*time.Second, "every message sent", func() bool {
		return statusCounts(t, pool, camp, SMS)[StatusSent] == 20 && statusCounts(t, pool, camp, Email)[StatusSent] == 20
	})
	return client
}

// B-3: language_mode 'recipient' renders the copy for the message's locale: an en user gets the English copy
// and the English fallback ("Valued customer") for an empty name, a tr user the primary copy and the Turkish
// fallback, on both channels.
func TestDispatcher_RecipientModeRendersTheMessagesLocale_B3(t *testing.T) {
	pool := storetest.NewDB(t)
	camp, users := localeCampaign(t, pool, "recipient", true)
	loc := userLocales(t, pool, users)
	client := runAll(t, pool, camp)
	nEN, nTR := 0, 0
	for _, c := range client.snapshot() {
		l := loc[c.Req.UserID]
		text := c.Req.Text
		if c.Req.Channel == Email {
			text = c.Req.Subject + " | " + c.Req.Text
		}
		switch {
		case strings.HasPrefix(l, "en"):
			nEN++
			want := "EN"
			if strings.HasSuffix(l, "/no-name") {
				want = "Valued customer"
			}
			if !strings.Contains(text, "EN") || !strings.Contains(text, want) || strings.Contains(text, "TR") {
				t.Errorf("en user %d (%s) got %q", c.Req.UserID, l, text)
			}
		default:
			nTR++
			want := "Ayşe"
			if strings.HasSuffix(l, "/no-name") {
				want = "Değerli müşterimiz"
			}
			if !strings.Contains(text, "TR") || !strings.Contains(text, want) || strings.Contains(text, "EN") {
				t.Errorf("tr user %d (%s) got %q", c.Req.UserID, l, text)
			}
		}
	}
	if nEN != 20 || nTR != 20 {
		t.Fatalf("rendered %d en and %d tr messages, want 20 each (10 users x 2 channels)", nEN, nTR)
	}
}

// B-3: language_mode 'campaign' is the behaviour before B-3: everyone gets the campaign-language copy and
// fallback, whatever their locale, even when an adaptation exists.
func TestDispatcher_CampaignModeIgnoresLocale_B3(t *testing.T) {
	pool := storetest.NewDB(t)
	camp, users := localeCampaign(t, pool, "campaign", true)
	loc := userLocales(t, pool, users)
	client := runAll(t, pool, camp)
	for _, c := range client.snapshot() {
		text := c.Req.Subject + c.Req.Text
		if strings.Contains(text, "EN") || strings.Contains(text, "Valued") || !strings.Contains(text, "TR") {
			t.Errorf("user %d (%s) got %q in campaign mode", c.Req.UserID, loc[c.Req.UserID], text)
		}
		if strings.HasSuffix(loc[c.Req.UserID], "/no-name") && !strings.Contains(text, "Değerli müşterimiz") {
			t.Errorf("user %d got %q, want the Turkish fallback", c.Req.UserID, text)
		}
	}
}

// B-3: a recipient-mode message whose locale has no copy fails permanently as content_missing; it never falls
// back to the other language.
func TestDispatcher_RecipientModeMissingLocaleFails_B3(t *testing.T) {
	pool := storetest.NewDB(t)
	camp, _ := localeCampaign(t, pool, "recipient", false)
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	startDispatcher(t, fastDispatcher(pool, SMS, client))
	eventually(t, 15*time.Second, "english messages failed, turkish ones sent", func() bool {
		c := statusCounts(t, pool, camp, SMS)
		return c[StatusFailed] == 10 && c[StatusSent] == 10
	})
	for _, c := range client.snapshot() {
		if strings.Contains(c.Req.Text, "EN") {
			t.Fatalf("an English message went out without an English copy: %q", c.Req.Text)
		}
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'failed'
		AND locale = 'en' AND last_error = 'content_missing'`, camp).Scan(&n); err != nil || n != 10 {
		t.Fatalf("content_missing en messages = %d, %v; want 10", n, err)
	}
}

package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/store/storetest"
)

// TestDispatcher_RendersTheMessagesVariant_B2: the worker renders the variant recorded on each message, so a
// campaign sent as A/B reaches the provider with A's text for A messages and B's text for B messages, on both
// channels. The provider here is the in-process mock behind a recorder that keeps what each request carried.
func TestDispatcher_RendersTheMessagesVariant_B2(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	camp := newCampaign(t, pool) // variant A of content version 1 is testSMS / testSubject / testBody
	if _, err := pool.Exec(ctx, `INSERT INTO campaign_content
			(campaign_id, version, variant, label, sms_text, email_subject, email_body, source, validation)
		VALUES ($1, 1, 'b', 'B', 'BBB {{first_name}}, sadece bugün', 'BBB {{first_name}} konu', 'BBB gövde {{first_name}}', 'llm', '{}')`, camp); err != nil {
		t.Fatal(err)
	}
	users := newUsers(t, pool, 60)
	for _, ch := range Channels {
		// Even-indexed users get B, odd-indexed A; the same user has the same variant on both channels.
		if _, err := pool.Exec(ctx, `INSERT INTO messages (campaign_id, user_id, channel, content_version, variant)
			SELECT $1, u.id, $2, 1, CASE WHEN o % 2 = 0 THEN 'b' ELSE 'a' END
			FROM unnest($3::bigint[]) WITH ORDINALITY AS u(id, o)`, camp, string(ch), users); err != nil {
			t.Fatal(err)
		}
	}
	wantB := map[int64]bool{}
	rows, err := pool.Query(ctx, `SELECT user_id FROM messages WHERE campaign_id = $1 AND channel = 'sms' AND variant = 'b'`, camp)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		wantB[id] = true
	}
	rows.Close()
	if len(wantB) != 30 {
		t.Fatalf("%d B users, want 30", len(wantB))
	}

	prov := mockprovider.New(config.ProviderFaults{LatencyMinMS: 1, LatencyMaxMS: 5, RateLimit: 1000, Seed: 4}, metrics.New())
	type seen struct {
		ch            string
		userID        int64
		text, subject string
	}
	var mu sync.Mutex
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			var b struct {
				Text, Subject string
				UserID        int64 `json:"user_id"`
			}
			if json.Unmarshal(body, &b) == nil {
				mu.Lock()
				got = append(got, seen{ch: strings.TrimPrefix(r.URL.Path, "/"), userID: b.UserID, text: b.Text, subject: b.Subject})
				mu.Unlock()
			}
		}
		prov.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(func() { prov.Close(); srv.Close() })
	client := NewHTTPClient(srv.URL, 2*time.Second)
	t.Cleanup(client.Close)

	for _, ch := range Channels {
		startDispatcher(t, fastDispatcher(pool, ch, client))
	}
	eventually(t, 30*time.Second, "every message sent", func() bool {
		return statusCounts(t, pool, camp, SMS)[StatusSent] == 60 && statusCounts(t, pool, camp, Email)[StatusSent] == 60
	})

	mu.Lock()
	defer mu.Unlock()
	nB, nA := 0, 0
	for _, s := range got {
		isB := strings.Contains(s.text, "BBB") || strings.Contains(s.subject, "BBB")
		if isB != wantB[s.userID] {
			t.Fatalf("%s user %d: rendered B=%v, message variant B=%v (text %q subject %q)", s.ch, s.userID, isB, wantB[s.userID], s.text, s.subject)
		}
		if isB {
			nB++
		} else {
			nA++
		}
	}
	if nB != 60 || nA != 60 {
		t.Fatalf("provider received %d B and %d A requests, want 60 each", nB, nA)
	}
}

// TestDispatcher_MissingVariantRowFails_B2: a message whose variant has no content row cannot be rendered;
// it fails permanently as content_missing instead of falling back to another variant.
func TestDispatcher_MissingVariantRowFails_B2(t *testing.T) {
	pool := storetest.NewDB(t)
	camp := newCampaign(t, pool) // only variant A exists
	users := newUsers(t, pool, 1)
	enqueue(t, pool, camp, SMS, users)
	if _, err := pool.Exec(context.Background(), `UPDATE messages SET variant = 'b' WHERE campaign_id = $1`, camp); err != nil {
		t.Fatal(err)
	}
	client := &scriptedClient{fn: func(_ int, r SendRequest) (SendResult, error) { return ok200(r) }}
	startDispatcher(t, fastDispatcher(pool, SMS, client))
	eventually(t, 10*time.Second, "message failed", func() bool { return statusCounts(t, pool, camp, SMS)[StatusFailed] == 1 })
	if n := len(client.snapshot()); n != 0 {
		t.Fatalf("%d sends for a message without content", n)
	}
}

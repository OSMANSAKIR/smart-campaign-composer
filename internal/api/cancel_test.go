package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store/storetest"
)

func TestCancelEndpoint_F4(t *testing.T) {
	pool := storetest.NewDB(t)
	srv := httptest.NewServer(New(Deps{Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(mock.NewDemo(), nil),
		ProviderURL: "http://127.0.0.1:1", Version: "test"}).Handler())
	t.Cleanup(srv.Close)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (seed_key, first_name, last_name, email, phone, city, locale, birth_date, signup_at, loyalty_tier, sms_consent, email_consent)
		SELECT 'u' || i, 'A', 'B', 'a' || i || '@example.com', '+90532' || lpad(i::text, 7, '0'), 'İstanbul', 'tr', date '1990-01-01', now(), 'bronze', true, true
		FROM generate_series(1, 5) i`); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, status, channels, approved_at) VALUES ('b', 'sending', '{sms}', now())
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO messages (campaign_id, user_id, channel, content_version, status)
		SELECT $1, id, 'sms', 1, CASE WHEN id % 2 = 0 THEN 'queued' ELSE 'sent' END FROM users`, id); err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/api/campaigns/" + id + "/cancel"

	type cancelBody struct {
		Campaign struct {
			Status      string  `json:"status"`
			CancelledAt *string `json:"cancelled_at"`
		} `json:"campaign"`
		Cancelled int `json:"cancelled"`
	}
	call := func() cancelBody {
		t.Helper()
		code, _, b := do(t, "POST", url, `{}`, nil)
		if code != http.StatusOK {
			t.Fatalf("cancel: %d %s", code, b)
		}
		var out cancelBody
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if first := call(); first.Cancelled != 2 || first.Campaign.Status != "cancelled" || first.Campaign.CancelledAt == nil {
		t.Fatalf("first cancel %+v", first)
	}
	if again := call(); again.Cancelled != 0 || again.Campaign.Status != "cancelled" {
		t.Fatalf("second cancel %+v", again)
	}

	// The stream ends at once for a cancelled campaign with nothing in flight.
	code, h, b := do(t, "GET", srv.URL+"/api/campaigns/"+id+"/events", "", nil)
	frames := strings.Split(strings.TrimSpace(string(b)), "\n\n")
	if code != 200 || h.Get("Content-Type") != "text/event-stream" || len(frames) != 2 ||
		frames[1] != "event: done\ndata: {\"status\":\"cancelled\"}" {
		t.Fatalf("events of a cancelled campaign: %d\n%s", code, b)
	}
	var snap struct {
		Channels map[string]struct {
			Total, Sent, Cancelled int
			Variants               map[string]struct{ Total, Sent, Cancelled int }
		} `json:"channels"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[0], "event: snapshot\ndata: ")), &snap); err != nil {
		t.Fatal(err)
	}
	if sms := snap.Channels["sms"]; sms.Total != 5 || sms.Sent != 3 || sms.Cancelled != 2 || sms.Variants["a"].Total != 5 {
		t.Fatalf("snapshot %s", frames[0])
	}

	// Status gate and unknown ids.
	var ready string
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, status) VALUES ('b', 'content_ready') RETURNING id::text`).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		path string
		code int
		err  string
	}{
		"content_ready": {ready, 409, "invalid_state"},
		"unknown":       {"00000000-0000-0000-0000-000000000000", 404, "not_found"},
		"malformed":     {"nope", 404, "not_found"},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, b := do(t, "POST", srv.URL+"/api/campaigns/"+tc.path+"/cancel", `{}`, nil)
			var e apiError
			if err := json.Unmarshal(b, &e); err != nil || code != tc.code || e.Error.Code != tc.err {
				t.Fatalf("%d %s", code, b)
			}
		})
	}
}

// TestApprove_VariantAndSendAtValidation_B1B2: malformed phase-2 inputs are rejected with the contract's codes.
func TestApprove_VariantAndSendAtValidation_B1B2(t *testing.T) {
	srv := newTestServer(t)
	_, _, b := do(t, "POST", srv.URL+"/api/campaigns", `{"brief":"indirim"}`, nil)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &created); err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/api/campaigns/" + created.ID + "/approve"
	key := map[string]string{"Idempotency-Key": "k1"}
	for name, tc := range map[string]struct {
		body string
		code int
		err  string
	}{
		"unknown variant":   {`{"channels":["sms"],"content_version":1,"variant":"c"}`, 400, "bad_request"},
		"send_at garbage":   {`{"channels":["sms"],"content_version":1,"send_at":"tomorrow"}`, 422, "invalid_send_at"},
		"send_at no zone":   {`{"channels":["sms"],"content_version":1,"send_at":"2026-11-11T09:30:00"}`, 422, "invalid_send_at"},
		"valid but a draft": {`{"channels":["sms"],"content_version":1,"variant":"ab","send_at":"2099-01-01T00:00:00+03:00"}`, 409, "invalid_state"},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, b := do(t, "POST", url, tc.body, key)
			var e apiError
			if err := json.Unmarshal(b, &e); err != nil || code != tc.code || e.Error.Code != tc.err {
				t.Fatalf("%d %s", code, b)
			}
		})
	}
}

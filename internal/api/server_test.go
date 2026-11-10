package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store/storetest"
)

func newTestServer(t *testing.T) *httptest.Server {
	pool := storetest.NewDB(t)
	srv := httptest.NewServer(New(Deps{
		Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(mock.NewDemo(), nil),
		ProviderURL: "http://127.0.0.1:1", Version: "test",
	}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthAndReadiness_ROBS3(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/api/catalog", "/api/system/status"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
	}
}

type apiError struct {
	Error struct {
		Code    string  `json:"code"`
		Message string  `json:"message"`
		Details []Issue `json:"details"`
	} `json:"error"`
}

// do sends one request and returns the status, headers and the whole body (the body is closed here).
func do(t *testing.T, method, url, body string, header map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, b
}

func TestErrorModel_ROBS1(t *testing.T) {
	srv := newTestServer(t)
	status, _, b := do(t, "POST", srv.URL+"/api/campaigns", `{"brief":"İstanbul'daki kullanıcılara indirim"}`, nil)
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(b, &created); err != nil || status != http.StatusCreated || created.Status != "draft" {
		t.Fatalf("create: %d %s", status, b)
	}
	base := srv.URL + "/api/campaigns/"
	for _, tc := range []struct {
		name, method, url, body string
		header                  map[string]string
		status                  int
		code                    string
	}{
		{"missing brief", "POST", srv.URL + "/api/campaigns", `{}`, nil, 400, "bad_request"},
		{"bad json", "POST", srv.URL + "/api/campaigns", `{`, nil, 400, "bad_request"},
		{"empty brief", "POST", srv.URL + "/api/campaigns", `{"brief":" "}`, nil, 422, "validation_failed"},
		{"unknown id", "GET", base + "00000000-0000-0000-0000-000000000000", "", nil, 404, "not_found"},
		{"malformed id", "GET", base + "nope", "", nil, 404, "not_found"},
		{"approve without key", "POST", base + created.ID + "/approve", `{"channels":["sms"],"content_version":1}`, nil, 400, "idempotency_key_required"},
		{"approve draft", "POST", base + created.ID + "/approve", `{"channels":["sms"],"content_version":1}`,
			map[string]string{"Idempotency-Key": "k"}, 409, "invalid_state"},
		{"content in draft", "POST", base + created.ID + "/content/generate", `{}`, nil, 409, "invalid_state"},
		{"bad limit", "GET", srv.URL + "/api/campaigns?limit=0", "", nil, 400, "bad_request"},
		{"bad message filter", "GET", base + created.ID + "/messages?status=lost", "", nil, 400, "bad_request"},
		{"segment misspelled groups", "PUT", base + created.ID + "/segment", `{"rule":{"group":[]}}`, nil, 422, "validation_failed"},
		{"segment case-folded", "PUT", base + created.ID + "/segment", `{"rule":{"GROUPS":[]}}`, nil, 422, "validation_failed"},
		{"segment missing rule", "PUT", base + created.ID + "/segment", `{}`, nil, 400, "bad_request"},
		{"unknown route", "GET", srv.URL + "/api/nope", "", nil, 404, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, _, b := do(t, tc.method, tc.url, tc.body, tc.header)
			var e apiError
			if err := json.Unmarshal(b, &e); err != nil {
				t.Fatalf("not the error model: %s", b)
			}
			if status != tc.status || e.Error.Code != tc.code || e.Error.Message == "" || e.Error.Details == nil {
				t.Fatalf("got %d %s", status, b)
			}
		})
	}
	status, _, b = do(t, "GET", srv.URL+"/api/campaigns?limit=5", "", nil)
	if status != 200 || !strings.Contains(string(b), created.ID) {
		t.Fatalf("list: %d %s", status, b)
	}
}

func TestEvents_SnapshotThenDone_RFLOW5(t *testing.T) {
	pool := storetest.NewDB(t)
	srv := httptest.NewServer(New(Deps{Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(mock.NewDemo(), nil),
		ProviderURL: "http://127.0.0.1:1", Version: "test"}).Handler())
	t.Cleanup(srv.Close)
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO campaigns (brief, status, approved_at, completed_at)
		VALUES ('b', 'completed', now(), now()) RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	status, h, b := do(t, "GET", srv.URL+"/api/campaigns/"+id+"/events", "", nil)
	if ct := h.Get("Content-Type"); status != 200 || ct != "text/event-stream" {
		t.Fatalf("%d %s", status, ct)
	}
	frames := strings.Split(strings.TrimSpace(string(b)), "\n\n")
	if len(frames) != 2 || !strings.HasPrefix(frames[0], "event: snapshot\ndata: {") ||
		frames[1] != `event: done`+"\n"+`data: {"status":"completed"}` {
		t.Fatalf("stream:\n%s", b)
	}
	var snap struct {
		CampaignID string                    `json:"campaign_id"`
		Channels   map[string]map[string]any `json:"channels"`
		Errors     map[string]int            `json:"errors"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[0], "event: snapshot\ndata: ")), &snap); err != nil ||
		snap.CampaignID != id || len(snap.Channels) != 2 || len(snap.Errors) != 5 {
		t.Fatalf("snapshot %s (%v)", frames[0], err)
	}
}

// A crashed worker never deletes its heartbeat row: the status page must not list it forever, and a late
// heartbeat must be flagged rather than shown as a live dispatcher.
func TestSystemStatus_StaleAndDeadWorkers_ROBS3(t *testing.T) {
	pool := storetest.NewDB(t)
	srv := httptest.NewServer(New(Deps{
		Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(mock.NewDemo(), nil),
		ProviderURL: "http://127.0.0.1:1", Version: "test",
	}).Handler())
	t.Cleanup(srv.Close)
	ctx := context.Background()
	insert := func(worker, ch string, lock bool, age string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO worker_heartbeats (worker_id, channel, holds_lock, at)
			VALUES ($1, $2, $3, now() - $4::interval)`, worker, ch, lock, age); err != nil {
			t.Fatal(err)
		}
	}
	type status struct {
		Worker struct {
			Status      string `json:"status"`
			Dispatchers []struct {
				WorkerID string `json:"worker_id"`
				Channel  string `json:"channel"`
				Stale    bool   `json:"stale"`
			} `json:"dispatchers"`
		} `json:"worker"`
	}
	get := func() status {
		t.Helper()
		code, _, body := do(t, http.MethodGet, srv.URL+"/api/system/status", "", nil)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		var s status
		if err := json.Unmarshal(body, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	insert("dead", "sms", false, "10 hours")
	insert("dead", "email", true, "10 hours")
	if s := get(); s.Worker.Status != "down" || len(s.Worker.Dispatchers) != 0 {
		t.Fatalf("only long-dead rows: got %+v, want down with no dispatchers", s.Worker)
	}

	insert("live", "sms", true, "1 second")
	insert("live", "email", true, "1 second")
	insert("late", "sms", false, "1 minute")
	s := get()
	if s.Worker.Status != "ok" {
		t.Errorf("both channels have a fresh lock holder: status %q, want ok", s.Worker.Status)
	}
	got := map[string]bool{}
	for _, d := range s.Worker.Dispatchers {
		got[d.WorkerID+"/"+d.Channel] = d.Stale
	}
	want := map[string]bool{"live/sms": false, "live/email": false, "late/sms": true}
	if len(got) != len(want) {
		t.Fatalf("dispatchers %v, want %v", got, want)
	}
	for k, stale := range want {
		if g, ok := got[k]; !ok || g != stale {
			t.Errorf("%s: stale=%v present=%v, want stale=%v", k, g, ok, stale)
		}
	}
}

// TestList_CarriesScheduledAt_B1: a scheduled campaign's list item has its send time (UTC), the others null.
func TestList_CarriesScheduledAt_B1(t *testing.T) {
	pool := storetest.NewDB(t)
	srv := httptest.NewServer(New(Deps{Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(mock.NewDemo(), nil),
		ProviderURL: "http://127.0.0.1:1", Version: "test"}).Handler())
	t.Cleanup(srv.Close)
	var sched, draft string
	if err := pool.QueryRow(context.Background(), `INSERT INTO campaigns (brief, status, approved_at, scheduled_at)
		VALUES ('scheduled', 'scheduled', now(), '2099-01-01T09:30:00Z') RETURNING id::text`).Scan(&sched); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `INSERT INTO campaigns (brief) VALUES ('draft') RETURNING id::text`).Scan(&draft); err != nil {
		t.Fatal(err)
	}
	status, _, b := do(t, "GET", srv.URL+"/api/campaigns", "", nil)
	var page struct {
		Items []struct {
			ID          string  `json:"id"`
			ScheduledAt *string `json:"scheduled_at"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &page); err != nil || status != 200 || len(page.Items) != 2 {
		t.Fatalf("list: %d %s", status, b)
	}
	for _, it := range page.Items {
		switch it.ID {
		case sched:
			if it.ScheduledAt == nil || *it.ScheduledAt != "2099-01-01T09:30:00Z" {
				t.Fatalf("scheduled item: %s", b)
			}
		case draft:
			if !strings.Contains(string(b), `"scheduled_at":null`) || it.ScheduledAt != nil {
				t.Fatalf("draft item: %s", b)
			}
		}
	}
}

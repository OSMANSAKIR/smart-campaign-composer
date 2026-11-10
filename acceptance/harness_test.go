//go:build acceptance

// Package acceptance drives the product end to end through its HTTP API, the way a marketer's browser does:
// brief → segment → content → approve → delivery, with the real API handler, real delivery dispatchers and
// the mock provider, all in this process, against a throwaway database (docs/contracts/api.md §9.9).
//
//	make acceptance                                  # mock LLM (CI)
//	make acceptance PROVIDER=anthropic <spend flag>  # owner only: real Claude, writes a report
//
// Assertions are semantic (fields used, unsupported list non-empty, consent), never exact model text, so the
// same scenarios run against the mock and the real model.
package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/api"
	"smartcampaign/internal/config"
	"smartcampaign/internal/delivery"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/factory"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/seed"
	"smartcampaign/internal/store/storetest"
)

// env is one running system: API, worker (both channels + reconciler) and mock provider on a fresh database.
type env struct {
	t        *testing.T
	api      *httptest.Server
	prov     *httptest.Server
	pool     *pgxpool.Pool
	llmName  string
	llmModel string
	maxCost  float64

	mu      sync.Mutex
	cost    float64
	results []result
}

type result struct {
	ID, Name string
	Pass     bool
	Notes    []string
	CostUSD  float64
	Duration time.Duration
}

const seedUsers = 5000

func newEnv(t *testing.T) *env {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	pool := storetest.NewDB(t)
	ctx := context.Background()
	if _, err := seed.Run(ctx, pool, seed.Generate(seedUsers, 20261108, time.Now())); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if os.Getenv("ACCEPTANCE_VERBOSE") != "" {
		log = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}

	// Mock provider with the compose defaults: some 5xx, 429 and hangs, its own 100/s limiter.
	faults := cfg.Provider
	faults.Seed = 20261110
	prov := mockprovider.New(faults, metrics.New())
	provSrv := httptest.NewServer(prov.Handler())
	t.Cleanup(func() { provSrv.Close(); prov.Close() })

	provider, err := factory.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Second money guard after the Makefile's: anything but the mock needs the owner's explicit flag.
	if provider.Name() != "mock" && os.Getenv("CONFIRM_SPEND") != "yes" {
		t.Fatalf("LLM provider %q costs money; owner only: make acceptance PROVIDER=anthropic CONFIRM_SPEND=yes", provider.Name())
	}
	m := metrics.New()
	apiSrv := httptest.NewServer(api.New(api.Deps{
		Pool: pool, Metrics: m, ProviderURL: provSrv.URL, Log: log, Version: "acceptance",
		LLM: llm.NewService(provider, llm.NewPGRecorder(pool), llm.WithMetrics(m),
			llm.WithTimeouts(cfg.LLMSegmentTimeout, cfg.LLMContentTimeout), llm.WithLogger(log)),
		SegmentTimeout: cfg.LLMSegmentTimeout, ContentTimeout: cfg.LLMContentTimeout,
	}).Handler())
	t.Cleanup(apiSrv.Close)

	// The worker, wired like cmd/worker.
	wctx, stop := context.WithCancel(context.Background())
	client := delivery.NewHTTPClient(provSrv.URL, cfg.ProviderTimeout)
	wm := metrics.New()
	var wg sync.WaitGroup
	for _, ch := range delivery.Channels {
		d := &delivery.Dispatcher{Channel: ch, Pool: pool, Client: client, Metrics: wm, Log: log, WorkerID: "acceptance",
			RateLimit: cfg.EffectiveRateLimit(), WindowGuard: 25 * time.Millisecond,
			MaxInFlight: cfg.MaxInFlightPerChannel, Lease: cfg.LeaseDuration}
		wg.Go(func() { _ = d.Run(wctx) })
	}
	startBackground(wctx, &wg, pool, log)
	t.Cleanup(func() { stop(); wg.Wait(); client.Close() })

	maxCost := 1.0
	if v, err := strconv.ParseFloat(os.Getenv("ACCEPTANCE_MAX_USD"), 64); err == nil && v > 0 {
		maxCost = v
	}
	e := &env{t: t, api: apiSrv, prov: provSrv, pool: pool, llmName: provider.Name(), llmModel: provider.Model(), maxCost: maxCost}
	t.Cleanup(e.writeReport)
	return e
}

// ---- HTTP ----

func (e *env) do(method, path string, body any, header map[string]string) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.api.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (e *env) must(method, path string, body any, want int, into any) []byte {
	e.t.Helper()
	code, out := e.do(method, path, body, nil)
	if code != want {
		e.t.Fatalf("%s %s: %d, want %d: %s", method, path, code, want, out)
	}
	if into != nil {
		if err := json.Unmarshal(out, into); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, out)
		}
	}
	return out
}

// ---- API objects (the subset the scenarios read; docs/contracts/api.md) ----

type condition struct {
	Field string          `json:"field"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
}

type rule struct {
	Groups []struct {
		Conditions []condition `json:"conditions"`
	} `json:"groups"`
}

func (r rule) fields() []string {
	var out []string
	for _, g := range r.Groups {
		for _, c := range g.Conditions {
			out = append(out, c.Field)
		}
	}
	return out
}

type audience struct {
	Total, SMSReachable, EmailReachable int
}

func (a *audience) UnmarshalJSON(b []byte) error {
	var x struct {
		Total          int `json:"total"`
		SMSReachable   int `json:"sms_reachable"`
		EmailReachable int `json:"email_reachable"`
	}
	err := json.Unmarshal(b, &x)
	*a = audience{x.Total, x.SMSReachable, x.EmailReachable}
	return err
}

type variant struct {
	Label        string `json:"label"`
	Source       string `json:"source"`
	SMSText      string `json:"sms_text"`
	EmailSubject string `json:"email_subject"`
	EmailBody    string `json:"email_body"`
	Validation   struct {
		ValidFor map[string]bool `json:"valid_for"`
		Fields   map[string]struct {
			WorstCaseRunes int `json:"worst_case_runes"`
		} `json:"fields"`
	} `json:"validation"`
}

type contentVersion struct {
	Version  int                 `json:"version"`
	Variants map[string]*variant `json:"variants"`
}

type campaignObj struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Segment *struct {
		Source              string    `json:"source"`
		Rule                rule      `json:"rule"`
		OriginalRule        *rule     `json:"original_rule"`
		UnsupportedCriteria []string  `json:"unsupported_criteria"`
		Assumptions         []string  `json:"assumptions"`
		ChannelsRequested   string    `json:"channels_requested"`
		Audience            *audience `json:"audience"`
	} `json:"segment"`
	Content     *contentVersion `json:"content"`
	SendVariant *string         `json:"send_variant"`
	ScheduledAt *time.Time      `json:"scheduled_at"`
	Progress    *progress       `json:"progress"`
}

type counts struct {
	Total            int `json:"total"`
	Sent             int `json:"sent"`
	Failed           int `json:"failed"`
	Cancelled        int `json:"cancelled"`
	SkippedNoConsent int `json:"skipped_no_consent"`
	Queued           int `json:"queued"`
	Sending          int `json:"sending"`
	Retry            int `json:"retry"`
}

type progress struct {
	Status   string `json:"status"`
	Channels map[string]*struct {
		counts
		Variants map[string]counts `json:"variants"`
	} `json:"channels"`
}

type llmMeta struct {
	CostUSD      float64 `json:"cost_usd"`
	Attempts     int     `json:"attempts"`
	CostRecorded *bool   `json:"cost_recorded"`
}

// ---- steps ----

func (e *env) addCost(usd float64) {
	e.mu.Lock()
	e.cost += usd
	over := e.cost > e.maxCost
	total := e.cost
	e.mu.Unlock()
	if over {
		e.t.Fatalf("cumulative LLM cost $%.4f passed the cap $%.2f (ACCEPTANCE_MAX_USD); stopping", total, e.maxCost)
	}
}

func (e *env) create(brief string) string {
	var c campaignObj
	e.must("POST", "/api/campaigns", map[string]string{"brief": brief}, 201, &c)
	return c.ID
}

func (e *env) segment(id string) (campaignObj, llmMeta) {
	var out struct {
		Campaign campaignObj `json:"campaign"`
		LLM      llmMeta     `json:"llm"`
	}
	e.must("POST", "/api/campaigns/"+id+"/segment/generate", nil, 200, &out)
	e.addCost(out.LLM.CostUSD)
	return out.Campaign, out.LLM
}

// stream posts to a generation endpoint and reads the SSE stream up to its last event.
func (e *env) stream(path string, body any) (event string, data []byte) {
	e.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(e.api.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		out, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("POST %s: %d %s", path, resp.StatusCode, out)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var cur string
	var buf strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			buf.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			if cur == "result" || cur == "error" {
				return cur, []byte(buf.String())
			}
			cur = ""
			buf.Reset()
		}
	}
	e.t.Fatalf("POST %s: stream ended without result or error", path)
	return "", nil
}

func (e *env) content(id string) contentVersion {
	ev, data := e.stream("/api/campaigns/"+id+"/content/generate", map[string]any{})
	if ev != "result" {
		e.t.Fatalf("content generation: %s %s", ev, data)
	}
	var out struct {
		Content contentVersion `json:"content"`
		LLM     llmMeta        `json:"llm"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		e.t.Fatal(err)
	}
	e.addCost(out.LLM.CostUSD)
	return out.Content
}

type approveReq struct {
	Channels       []string `json:"channels"`
	ContentVersion int      `json:"content_version"`
	Variant        string   `json:"variant,omitempty"`
	SendAt         string   `json:"send_at,omitempty"`
}

func (e *env) approve(id, key string, req approveReq) (int, []byte) {
	return e.do("POST", "/api/campaigns/"+id+"/approve", req, map[string]string{"Idempotency-Key": key})
}

func (e *env) get(id string) campaignObj {
	var c campaignObj
	e.must("GET", "/api/campaigns/"+id, nil, 200, &c)
	return c
}

// waitDone polls until the campaign is terminal and none of its messages is still in flight.
func (e *env) waitDone(id string, timeout time.Duration) campaignObj {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		c := e.get(id)
		if c.Status == "completed" || c.Status == "completed_with_failures" || c.Status == "cancelled" {
			if inFlight(c) == 0 {
				return c
			}
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("campaign %s still %s after %s: %+v", id, c.Status, timeout, c.Progress)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func inFlight(c campaignObj) int {
	if c.Progress == nil {
		return 0
	}
	n := 0
	for _, ch := range c.Progress.Channels {
		n += ch.Queued + ch.Sending + ch.Retry
	}
	return n
}

type providerStats struct {
	Channels map[string]struct {
		DuplicateRecipientDeliveries int `json:"duplicate_recipient_deliveries"`
		MaxRequestsInAny1sWindow     int `json:"max_requests_in_any_1s_window"`
		RejectedByLimiter            int `json:"rejected_by_limiter"`
	} `json:"channels"`
}

func (e *env) providerStats() providerStats {
	var s providerStats
	getJSON(e.t, e.prov.URL+"/stats", &s)
	return s
}

type delivered struct {
	Channel   string    `json:"channel"`
	UserID    int64     `json:"user_id"`
	MessageID string    `json:"message_id"`
	At        time.Time `json:"at"`
}

func (e *env) delivered(campaignID string) []delivered {
	var out struct {
		Items []delivered `json:"items"`
	}
	getJSON(e.t, e.prov.URL+"/stats/delivered?campaign_id="+campaignID, &out)
	return out.Items
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatal(err)
	}
}

// ---- invariants shared by every scenario that delivers ----

// deliveryInvariants checks a finished campaign: every message terminal, no message to a user without
// consent, counts add up per channel (and per variant), the provider saw no duplicate delivery and never
// more than 100 requests in any 1 s window on a channel.
func (e *env) deliveryInvariants(r *result, c campaignObj) {
	ctx := context.Background()
	var open, noConsent int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE m.status IN ('queued','sending','retry')),
			count(*) FILTER (WHERE m.status = 'sent' AND ((m.channel = 'sms' AND NOT u.sms_consent) OR (m.channel = 'email' AND NOT u.email_consent)))
		FROM messages m JOIN users u ON u.id = m.user_id WHERE m.campaign_id = $1`, c.ID).Scan(&open, &noConsent); err != nil {
		e.t.Fatal(err)
	}
	r.check(open == 0, "every message terminal (%d open)", open)
	r.check(noConsent == 0, "no delivery without consent (%d)", noConsent)
	for ch, p := range c.Progress.Channels {
		if p.Total == 0 {
			continue
		}
		sum := p.Sent + p.Failed + p.Cancelled + p.SkippedNoConsent
		r.check(sum == p.Total, "%s: sent %d + failed %d + cancelled %d + skipped %d = total %d", ch, p.Sent, p.Failed, p.Cancelled, p.SkippedNoConsent, p.Total)
		vt := 0
		for _, v := range p.Variants {
			vt += v.Total
		}
		r.check(len(p.Variants) == 0 || vt == p.Total, "%s: per-variant totals add up (%d of %d)", ch, vt, p.Total)
	}
	st := e.providerStats()
	for ch, s := range st.Channels {
		r.check(s.DuplicateRecipientDeliveries == 0, "%s: 0 duplicate deliveries (%d)", ch, s.DuplicateRecipientDeliveries)
		r.check(s.MaxRequestsInAny1sWindow <= 100, "%s: max %d requests in any 1 s window (≤ 100)", ch, s.MaxRequestsInAny1sWindow)
	}
}

func (r *result) check(ok bool, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if ok {
		r.Notes = append(r.Notes, "✓ "+msg)
	} else {
		r.Notes = append(r.Notes, "✗ "+msg)
		r.Pass = false
	}
}

// scenario runs one numbered scenario as a subtest and records it for the report.
func (e *env) scenario(id, name string, fn func(t *testing.T, r *result)) {
	e.t.Run(id+"_"+strings.ReplaceAll(name, " ", "_"), func(t *testing.T) {
		r := &result{ID: id, Name: name, Pass: true}
		e.mu.Lock()
		before := e.cost
		e.mu.Unlock()
		start := time.Now()
		defer func() {
			r.Duration = time.Since(start)
			e.mu.Lock()
			r.CostUSD = e.cost - before
			if t.Failed() {
				r.Pass = false
			}
			e.results = append(e.results, *r)
			e.mu.Unlock()
			for _, n := range r.Notes {
				t.Log(n)
			}
			if !r.Pass {
				t.Fail()
			}
		}()
		fn(t, r)
	})
}

// writeReport writes docs/acceptance/REPORT-<provider>-<date>.md when ACCEPTANCE_REPORT names the directory.
func (e *env) writeReport() {
	dir := os.Getenv("ACCEPTANCE_REPORT")
	if dir == "" {
		return
	}
	var b strings.Builder
	date := time.Now().Format("2006-01-02")
	fmt.Fprintf(&b, "# Acceptance report: %s / %s / %s\n\n", e.llmName, e.llmModel, date)
	b.WriteString("Generated by `make acceptance` (`acceptance/`): the HTTP API, the delivery worker and the mock provider in one process on a throwaway database seeded with 5000 synthetic users. Assertions are semantic, not exact model text.\n\n")
	pass := 0
	for _, r := range e.results {
		if r.Pass {
			pass++
		}
	}
	fmt.Fprintf(&b, "**%d/%d scenarios pass.** LLM cost: $%.4f (cap $%.2f).\n\n", pass, len(e.results), e.cost, e.maxCost)
	b.WriteString("| # | Scenario | Result | Time | LLM cost |\n|---|---|---|---|---|\n")
	for _, r := range e.results {
		res := "PASS"
		if !r.Pass {
			res = "FAIL"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | $%.4f |\n", r.ID, r.Name, res, r.Duration.Round(100*time.Millisecond), r.CostUSD)
	}
	b.WriteString("\n## Checks\n")
	for _, r := range e.results {
		fmt.Fprintf(&b, "\n### %s %s\n\n", r.ID, r.Name)
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}
	path := fmt.Sprintf("%s/REPORT-%s-%s.md", dir, e.llmName, date)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		e.t.Errorf("write report: %v", err)
		return
	}
	e.t.Logf("report written to %s", path)
}

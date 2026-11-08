//go:build stress

package main

// Stress scenarios for the delivery pipeline (KICKOFF §10.1, S1/S2/S7) against real worker processes, the
// in-process mock provider and a throwaway database. It writes docs/stress/REPORT.md with the §10.2
// invariants of every run.
//
//	STRESS=1 go test -tags stress -run TestStress -timeout 60m ./cmd/worker/
//
// Environment: TEST_DATABASE_URL (as for all integration tests), STRESS_REPORT (output path),
// STRESS_S1_USERS (default 5000), STRESS_S2_USERS (default 2000), STRESS_S7_USERS (default 1500),
// STRESS_ONLY (comma-separated subset, e.g. "S1,S7").

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/store/storetest"
)

// ---- report model ----

type invariant struct {
	ID, Text, Detail string
	OK               bool
}

type result struct {
	Name, Desc string
	Params     [][2]string
	Duration   time.Duration
	Inv        []invariant
	Notes      []string
}

var (
	resultsMu sync.Mutex
	results   []*result
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func wanted(name string) bool {
	only := os.Getenv("STRESS_ONLY")
	if only == "" {
		return true
	}
	for _, s := range strings.Split(only, ",") {
		if strings.EqualFold(strings.TrimSpace(s), name) {
			return true
		}
	}
	return false
}

// ---- infrastructure ----

var (
	buildOnce sync.Once
	workerBin string
	buildErr  error
)

func buildWorker(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "worker-stress")
		if err != nil {
			buildErr = err
			return
		}
		workerBin = filepath.Join(dir, "worker")
		if out, err := exec.Command("go", "build", "-o", workerBin, ".").CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("%w\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("build worker: %v", buildErr)
	}
	return workerBin
}

// provider is the mock provider on a fixed address so it can be taken down and brought back (S7).
type provider struct {
	t    *testing.T
	addr string
	mock *mockprovider.Server
	mu   sync.Mutex
	srv  *http.Server
}

func startProvider(t *testing.T, cfg config.ProviderFaults) *provider {
	p := &provider{t: t, addr: freeAddr(t), mock: mockprovider.New(cfg, metrics.New())}
	p.up()
	t.Cleanup(func() { p.mock.Close(); p.down() })
	return p
}

func (p *provider) url() string { return "http://" + p.addr }

func (p *provider) up() {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, err := net.Listen("tcp", p.addr)
	if err != nil {
		p.t.Fatalf("provider listen: %v", err)
	}
	p.srv = &http.Server{Handler: p.mock.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = p.srv.Serve(l) }()
}

func (p *provider) down() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv != nil {
		_ = p.srv.Close() // closes the listener and every open connection: the clients see refused/reset
		p.srv = nil
	}
}

func (p *provider) stats() (map[string]mockprovider.ChannelStats, error) {
	resp, err := http.Get(p.url() + "/stats")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var d struct {
		Channels map[string]mockprovider.ChannelStats `json:"channels"`
	}
	return d.Channels, json.NewDecoder(resp.Body).Decode(&d)
}

func (p *provider) delivered(channel, campaign string) ([]int64, error) {
	resp, err := http.Get(p.url() + "/stats/delivered?channel=" + channel + "&campaign_id=" + campaign)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var d struct {
		Items []struct {
			UserID int64 `json:"user_id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	ids := make([]int64, len(d.Items))
	for i, it := range d.Items {
		ids[i] = it.UserID
	}
	return ids, nil
}

type worker struct {
	id      string
	cmd     *exec.Cmd
	addr    string
	logPath string
	exited  chan error
}

type env struct {
	t      *testing.T
	pool   *pgxpool.Pool
	dbURL  string
	prov   *provider
	logDir string
	nextID int
}

func newEnv(t *testing.T, faults config.ProviderFaults) *env {
	pool := storetest.NewDB(t)
	cc := pool.Config().ConnConfig
	return &env{
		t: t, pool: pool, prov: startProvider(t, faults), logDir: t.TempDir(),
		dbURL: fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", cc.User, cc.Password, cc.Host, cc.Port, cc.Database),
	}
}

func (e *env) startWorker(extra ...string) *worker {
	e.t.Helper()
	e.nextID++
	w := &worker{id: fmt.Sprintf("w%d", e.nextID), addr: freeAddr(e.t), exited: make(chan error, 1)}
	w.logPath = filepath.Join(e.logDir, w.id+".log")
	logf, err := os.Create(w.logPath)
	if err != nil {
		e.t.Fatal(err)
	}
	w.cmd = exec.Command(buildWorker(e.t))
	w.cmd.Env = append(os.Environ(), "DATABASE_URL="+e.dbURL, "WORKER_ADDR="+w.addr, "PROVIDER_URL="+e.prov.url(),
		"LOG_LEVEL=info", "ANTHROPIC_API_KEY=")
	w.cmd.Env = append(w.cmd.Env, extra...)
	w.cmd.Stdout, w.cmd.Stderr = logf, logf
	if err := w.cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	go func() { w.exited <- w.cmd.Wait(); logf.Close() }()
	e.t.Cleanup(func() { _ = w.cmd.Process.Kill() })
	return w
}

func (w *worker) status(path string) int {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + w.addr + path)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

var metricLine = regexp.MustCompile(`^messages_total\{channel="(\w+)",status="(\w+)"\} (\S+)$`)

// sentMetric scrapes the worker's messages_total{status="sent"} per channel.
func (w *worker) sentMetric() (map[string]float64, error) {
	resp, err := http.Get("http://" + w.addr + "/metrics")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	out := map[string]float64{}
	for _, line := range strings.Split(string(b), "\n") {
		if m := metricLine.FindStringSubmatch(line); m != nil && m[2] == "sent" {
			v, _ := strconv.ParseFloat(m[3], 64)
			out[m[1]] = v
		}
	}
	return out, nil
}

func (w *worker) logContains(needles ...string) (string, bool) {
	b, _ := os.ReadFile(w.logPath)
	for _, n := range needles {
		if strings.Contains(string(b), n) {
			return n, true
		}
	}
	return "", false
}

// seeded describes a created campaign.
type seeded struct {
	campaign string
	users    int
	messages map[string]int // channel -> enqueued
	expect   map[string]map[string]int
}

// seed creates users with edge cases, a `sending` campaign and its messages, then revokes some consent after
// enqueue. Every 50th user has an invalid phone (permanent 400), every 97th/89th never consented to
// sms/email (not enqueued), every 101st/103rd revokes sms/email consent after enqueue.
func (e *env) seed(n int) seeded {
	e.t.Helper()
	ctx := context.Background()
	var camp string
	must := func(_ any, err error) {
		e.t.Helper()
		if err != nil {
			e.t.Fatal(err)
		}
	}
	if err := e.pool.QueryRow(ctx, `INSERT INTO campaigns (brief, status, language, channels, approved_content_version, approved_at)
		VALUES ('stress', 'sending', 'tr', '{sms,email}', 1, now()) RETURNING id::text`).Scan(&camp); err != nil {
		e.t.Fatal(err)
	}
	must(e.pool.Exec(ctx, `INSERT INTO campaign_content (campaign_id, version, sms_text, email_subject, email_body, source, validation)
		VALUES ($1, 1, 'Merhaba {{first_name}}, {{city}} için indirim!', '{{first_name}}, indirim', E'Merhaba {{first_name}},\n\n{{city}} mağazasında indirim.', 'llm', '{}')`, camp))
	must(e.pool.Exec(ctx, `INSERT INTO users (seed_key, first_name, last_name, email, phone, city, locale, birth_date, signup_at,
			loyalty_tier, sms_consent, email_consent)
		SELECT 's' || i, 'Kullanıcı' || i, 'Test', 'user' || i || '@example.com',
		       CASE WHEN i % 50 = 0 THEN '12' ELSE '+90532' || lpad(i::text, 7, '0') END,
		       'Ankara', 'tr', date '1990-01-01', now(), 'bronze', i % 97 <> 0, i % 89 <> 0
		FROM generate_series(1, $1::int) i`, n))
	must(e.pool.Exec(ctx, `INSERT INTO messages (campaign_id, user_id, channel, content_version)
		SELECT $1, id, 'sms', 1 FROM users WHERE sms_consent`, camp))
	must(e.pool.Exec(ctx, `INSERT INTO messages (campaign_id, user_id, channel, content_version)
		SELECT $1, id, 'email', 1 FROM users WHERE email_consent`, camp))
	must(e.pool.Exec(ctx, `UPDATE users SET sms_consent = false WHERE seed_key LIKE 's%' AND substr(seed_key, 2)::int % 101 = 0`))
	must(e.pool.Exec(ctx, `UPDATE users SET email_consent = false WHERE substr(seed_key, 2)::int % 103 = 0`))

	s := seeded{campaign: camp, users: n, messages: map[string]int{}, expect: map[string]map[string]int{"sms": {}, "email": {}}}
	for _, ch := range []string{"sms", "email"} {
		col := ch + "_consent"
		var total, skipped, failed int
		if err := e.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE NOT u.`+col+`),
				count(*) FILTER (WHERE u.`+col+` AND $2 = 'sms' AND u.phone = '12')
			FROM messages m JOIN users u ON u.id = m.user_id WHERE m.campaign_id = $1 AND m.channel = $2`, camp, ch).Scan(&total, &skipped, &failed); err != nil {
			e.t.Fatal(err)
		}
		s.messages[ch] = total
		s.expect[ch] = map[string]int{"skipped_no_consent": skipped, "failed": failed, "sent": total - skipped - failed}
	}
	return s
}

func (e *env) counts(camp string) (map[string]map[string]int, error) {
	rows, err := e.pool.Query(context.Background(), `SELECT channel, status, count(*) FROM messages WHERE campaign_id = $1 GROUP BY 1, 2`, camp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{"sms": {}, "email": {}}
	for rows.Next() {
		var ch, st string
		var n int
		if err := rows.Scan(&ch, &st, &n); err != nil {
			return nil, err
		}
		out[ch][st] = n
	}
	return out, rows.Err()
}

type progress struct {
	Elapsed time.Duration
	Sent    map[string]int
	Open    int
}

// waitTerminal polls once a second until every message is terminal; hook runs each tick (kill steps etc.).
func (e *env) waitTerminal(camp string, timeout time.Duration, hook func(p progress)) ([]progress, time.Duration, bool) {
	start := time.Now()
	var samples []progress
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		c, err := e.counts(camp)
		if err == nil {
			open := 0
			sent := map[string]int{}
			for ch, m := range c {
				open += m["queued"] + m["retry"] + m["sending"]
				sent[ch] = m["sent"]
			}
			p := progress{Elapsed: time.Since(start), Sent: sent, Open: open}
			samples = append(samples, p)
			if hook != nil {
				hook(p)
			}
			if open == 0 {
				return samples, p.Elapsed, true
			}
		}
		if time.Since(start) > timeout {
			return samples, time.Since(start), false
		}
		<-tick.C
	}
}

// peakPerSecond is the best 5-second average of sent messages per second for one channel.
func peakPerSecond(samples []progress, ch string) float64 {
	best := 0.0
	for i := 5; i < len(samples); i++ {
		dt := (samples[i].Elapsed - samples[i-5].Elapsed).Seconds()
		if dt > 0 {
			best = max(best, float64(samples[i].Sent[ch]-samples[i-5].Sent[ch])/dt)
		}
	}
	return best
}

// checkInvariants asserts KICKOFF §10.2 I1-I7 and records them. metricSent, when non-nil, is a worker's
// messages_total{sent} (only valid when that worker was never killed).
func (e *env) checkInvariants(r *result, s seeded, completed bool, workers []*worker, metricSent map[string]float64, expectExact bool) {
	e.t.Helper()
	ctx := context.Background()
	add := func(id, text string, ok bool, detail string, a ...any) {
		r.Inv = append(r.Inv, invariant{ID: id, Text: text, OK: ok, Detail: fmt.Sprintf(detail, a...)})
		if !ok {
			e.t.Errorf("%s %s: %s", r.Name, id, fmt.Sprintf(detail, a...))
		}
	}
	stats, err := e.prov.stats()
	if err != nil {
		e.t.Fatalf("provider stats: %v", err)
	}
	counts, err := e.counts(s.campaign)
	if err != nil {
		e.t.Fatal(err)
	}

	dups, maxReq, maxDel, rejected := 0, 0, 0, 0
	for _, ch := range []string{"sms", "email"} {
		st := stats[ch]
		dups += st.DuplicateRecipientDeliveries
		maxReq, maxDel, rejected = max(maxReq, st.MaxRequestsInAny1sWindow), max(maxDel, st.MaxDeliveriesInAny1sWindow), rejected+st.RejectedByLimiter
	}
	add("I1", "provider duplicate_recipient_deliveries == 0", dups == 0, "duplicates: %d (sms %d accepted, email %d accepted)", dups, stats["sms"].Accepted, stats["email"].Accepted)

	bad := 0
	for ch, col := range map[string]string{"sms": "sms_consent", "email": "email_consent"} {
		ids, err := e.prov.delivered(ch, s.campaign)
		if err != nil {
			e.t.Fatal(err)
		}
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = ANY($1) AND NOT `+col, ids).Scan(&n); err != nil {
			e.t.Fatal(err)
		}
		bad += n
	}
	add("I2", "no delivery to a user without consent (provider delivered list joined with users)", bad == 0, "deliveries without consent: %d", bad)

	add("I3", "provider-measured max requests/deliveries in any 1 s window <= 100 per channel", maxReq <= 100 && maxDel <= 100,
		"max requests %d (arrivals incl. retries), max deliveries %d, provider-limiter rejections %d", maxReq, maxDel, rejected)

	open := 0
	for _, m := range counts {
		open += m["queued"] + m["retry"] + m["sending"]
	}
	add("I4", "every message reaches a terminal state within the time limit", completed && open == 0, "non-terminal messages at the end: %d", open)

	mismatch := ""
	for _, ch := range []string{"sms", "email"} {
		if stats[ch].Accepted != counts[ch]["sent"] {
			mismatch += fmt.Sprintf("%s: provider accepted %d vs DB sent %d; ", ch, stats[ch].Accepted, counts[ch]["sent"])
		}
		if metricSent != nil && int(metricSent[ch]) != counts[ch]["sent"] {
			mismatch += fmt.Sprintf("%s: metric sent %v vs DB %d; ", ch, metricSent[ch], counts[ch]["sent"])
		}
	}
	detail := fmt.Sprintf("DB sent sms/email %d/%d = provider accepted %d/%d", counts["sms"]["sent"], counts["email"]["sent"], stats["sms"].Accepted, stats["email"].Accepted)
	if metricSent != nil {
		detail += fmt.Sprintf(" = worker metric %d/%d", int(metricSent["sms"]), int(metricSent["email"]))
	} else {
		detail += " (worker metrics not compared: a killed worker loses its counters)"
	}
	if expectExact {
		for _, ch := range []string{"sms", "email"} {
			for st, want := range s.expect[ch] {
				if got := counts[ch][st]; got != want && !(st == "failed" || st == "sent") {
					mismatch += fmt.Sprintf("%s %s: %d, expected %d; ", ch, st, got, want)
				}
			}
		}
	}
	add("I5", "DB counts, provider counts and worker metrics agree", mismatch == "", "%s%s", detail, ternary(mismatch != "", " MISMATCH: "+mismatch, ""))

	var maxAtt, counted int
	if err := e.pool.QueryRow(ctx, `SELECT coalesce(max(attempts), 0) FROM messages WHERE campaign_id = $1`, s.campaign).Scan(&maxAtt); err != nil {
		e.t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT coalesce(max(n), 0) FROM (SELECT count(*) n FROM delivery_attempts a JOIN messages m ON m.id = a.message_id
		WHERE m.campaign_id = $1 AND a.error_class IN ('transient', 'ambiguous') GROUP BY a.message_id) x`, s.campaign).Scan(&counted); err != nil {
		e.t.Fatal(err)
	}
	hist := map[int]int{}
	rows, err := e.pool.Query(ctx, `SELECT attempts, count(*) FROM messages WHERE campaign_id = $1 GROUP BY 1 ORDER BY 1`, s.campaign)
	if err != nil {
		e.t.Fatal(err)
	}
	for rows.Next() {
		var a, n int
		_ = rows.Scan(&a, &n)
		hist[a] = n
	}
	rows.Close()
	var hb strings.Builder
	keys := make([]int, 0, len(hist))
	for k := range hist {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		fmt.Fprintf(&hb, "%d:%d ", k, hist[k])
	}
	add("I6", "attempts <= policy maximum (5 counted tries), no hot loop", maxAtt <= 5 && counted <= 5,
		"max messages.attempts %d, max counted failed tries per message %d; attempts histogram (attempts:messages) %s", maxAtt, counted, strings.TrimSpace(hb.String()))

	panics := ""
	for _, w := range workers {
		if n, ok := w.logContains("panic:", "fatal error:", "DATA RACE"); ok {
			panics += fmt.Sprintf("%s logged %q; ", w.id, n)
		}
	}
	add("I7", "no panics in worker logs (goroutine leaks are covered by goleak in the unit tests)", panics == "", "%s", ternary(panics == "", "worker logs clean", panics))
}

func ternary(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

func fmtRate(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) + "/s" }

func (e *env) stop(ws ...*worker) {
	for _, w := range ws {
		_ = w.cmd.Process.Signal(syscall.SIGTERM)
	}
	for _, w := range ws {
		select {
		case <-w.exited:
		case <-time.After(20 * time.Second):
			_ = w.cmd.Process.Kill()
		}
	}
}

// ---- scenarios ----

func TestStress(t *testing.T) {
	if os.Getenv("STRESS") == "" {
		t.Skip("set STRESS=1 to run the stress scenarios")
	}
	t.Cleanup(writeReport)

	t.Run("S1_baseline", func(t *testing.T) {
		if !wanted("S1") {
			t.Skip()
		}
		n := envInt("STRESS_S1_USERS", 5000)
		e := newEnv(t, config.ProviderFaults{Seed: 11, RateLimit: 100, RetryAfterS: 1}) // faults off, own limiter on
		s := e.seed(n)
		r := &result{Name: "S1 baseline", Desc: "Faults off; 1 worker; the provider enforces its own 100/s limit per channel and reports how often the worker exceeded it.",
			Params: [][2]string{{"users", strconv.Itoa(n)}, {"messages sms/email", fmt.Sprintf("%d / %d", s.messages["sms"], s.messages["email"])},
				{"workers", "1"}, {"rate limit", "100 per channel (window 1 s + 25 ms guard)"}, {"in flight", "32 per channel"}}}
		w := e.startWorker()
		samples, dur, done := e.waitTerminal(s.campaign, 10*time.Minute, nil)
		metricSent, _ := w.sentMetric()
		r.Duration = dur
		c, _ := e.counts(s.campaign)
		r.Params = append(r.Params, [2]string{"time to terminal", dur.Round(time.Second).String()},
			[2]string{"sms sent / avg / peak(5 s)", fmt.Sprintf("%d / %s / %s", c["sms"]["sent"], fmtRate(float64(c["sms"]["sent"])/dur.Seconds()), fmtRate(peakPerSecond(samples, "sms")))},
			[2]string{"email sent / avg / peak(5 s)", fmt.Sprintf("%d / %s / %s", c["email"]["sent"], fmtRate(float64(c["email"]["sent"])/dur.Seconds()), fmtRate(peakPerSecond(samples, "email")))},
			[2]string{"final status sms", fmt.Sprint(c["sms"])}, [2]string{"final status email", fmt.Sprint(c["email"])},
			[2]string{"expected sms / email (consent + invalid numbers)", fmt.Sprint(s.expect["sms"]) + " / " + fmt.Sprint(s.expect["email"])})
		e.checkInvariants(r, s, done, []*worker{w}, metricSent, true)
		e.stop(w)
		addResult(r)
	})

	t.Run("S2_chaos", func(t *testing.T) {
		if !wanted("S2") {
			t.Skip()
		}
		n := envInt("STRESS_S2_USERS", 2000)
		e := newEnv(t, config.ProviderFaults{Seed: 22, LatencyMinMS: 20, LatencyMaxMS: 800, ErrorRate: 0.10, Rate429: 0.05, RetryAfterS: 1,
			HangRate: 0.02, SpikeRate: 0.01, SpikeMS: 2000, RateLimit: 100})
		s := e.seed(n)
		opts := []string{"MAX_IN_FLIGHT_PER_CHANNEL=64", "PROVIDER_TIMEOUT=3s", "LEASE_DURATION=10s"}
		r := &result{Name: "S2 chaos", Desc: "Provider at 10% 5xx, 5% 429 (Retry-After 1 s), 2% hangs (half after recording the delivery), 20-800 ms latency; 2 workers; one is SIGKILLed at 30% progress and a replacement starts 3 s later.",
			Params: [][2]string{{"users", strconv.Itoa(n)}, {"messages sms/email", fmt.Sprintf("%d / %d", s.messages["sms"], s.messages["email"])},
				{"workers", "2, one SIGKILLed mid-run, one replacement"}, {"in flight", "64 per channel (32 cannot cover 400 ms mean latency at 100/s)"}, {"provider timeout / lease", "3 s / 10 s"}}}
		a, b := e.startWorker(opts...), e.startWorker(opts...)
		workers := []*worker{a, b}
		killed := false
		var killAt time.Duration
		replacementCh := make(chan *worker, 1)
		samples, dur, done := e.waitTerminal(s.campaign, 25*time.Minute, func(p progress) {
			total := s.messages["sms"] + s.messages["email"]
			if !killed && p.Sent["sms"]+p.Sent["email"] >= total*3/10 {
				killed = true
				killAt = p.Elapsed
				_ = a.cmd.Process.Kill()
				go func() {
					<-time.After(3 * time.Second)
					replacementCh <- e.startWorker(opts...)
				}()
			}
		})
		select {
		case w := <-replacementCh:
			workers = append(workers, w)
		default:
		}
		r.Duration = dur
		c, _ := e.counts(s.campaign)
		stats, _ := e.prov.stats()
		var attempts, throttled int
		_ = e.pool.QueryRow(context.Background(), `SELECT count(*), count(*) FILTER (WHERE error_class = 'throttled') FROM delivery_attempts`).Scan(&attempts, &throttled)
		r.Params = append(r.Params, [2]string{"killed worker at", killAt.Round(time.Second).String()},
			[2]string{"time to terminal", dur.Round(time.Second).String()},
			[2]string{"sms sent / avg / peak(5 s)", fmt.Sprintf("%d / %s / %s", c["sms"]["sent"], fmtRate(float64(c["sms"]["sent"])/dur.Seconds()), fmtRate(peakPerSecond(samples, "sms")))},
			[2]string{"email sent / avg / peak(5 s)", fmt.Sprintf("%d / %s / %s", c["email"]["sent"], fmtRate(float64(c["email"]["sent"])/dur.Seconds()), fmtRate(peakPerSecond(samples, "email")))},
			[2]string{"final status sms", fmt.Sprint(c["sms"])}, [2]string{"final status email", fmt.Sprint(c["email"])},
			[2]string{"delivery_attempts rows / throttled", fmt.Sprintf("%d / %d", attempts, throttled)},
			[2]string{"provider sms hangs / replayed keys / 429s / 5xx", fmt.Sprintf("%d / %d / %d / %d", stats["sms"].Hangs, stats["sms"].ReplayedKeys, stats["sms"].StatusCounts["429"], stats["sms"].StatusCounts["500"]+stats["sms"].StatusCounts["502"]+stats["sms"].StatusCounts["503"])})
		e.checkInvariants(r, s, done, workers, nil, false)
		// Every non-consenting message is skipped, every invalid number failed; the rest are sent or failed after 5 tries.
		r.Notes = append(r.Notes, fmt.Sprintf("Expected from the seed without faults: sms %v, email %v. Messages that exhausted 5 counted tries under 12%% injected faults per attempt end as failed instead of sent.", s.expect["sms"], s.expect["email"]))
		e.stop(workers[1:]...)
		addResult(r)
	})

	t.Run("S7_dependencies", func(t *testing.T) {
		if !wanted("S7") {
			t.Skip()
		}
		n := envInt("STRESS_S7_USERS", 1500)
		e := newEnv(t, config.ProviderFaults{Seed: 77, RateLimit: 100, RetryAfterS: 1})
		s := e.seed(n)
		opts := []string{"LEASE_DURATION=10s", "PROVIDER_TIMEOUT=2s"}
		r := &result{Name: "S7 dependency restart", Desc: "(a) The provider is unreachable for 10 s (listener closed); (b) the database refuses all connections for 8 s (ALLOW_CONNECTIONS off + all sessions terminated, the closest in-process stand-in for a Postgres restart).",
			Params: [][2]string{{"users", strconv.Itoa(n)}, {"messages sms/email", fmt.Sprintf("%d / %d", s.messages["sms"], s.messages["email"])}, {"workers", "2"}}}
		a, b := e.startWorker(opts...), e.startWorker(opts...)
		var phase int
		var readyDuringOutage, readyAfter, healthDuringOutage atomic.Int64
		var outageWG sync.WaitGroup
		var mu sync.Mutex
		var outageStart time.Time
		_, dur, done := e.waitTerminal(s.campaign, 15*time.Minute, func(p progress) {
			mu.Lock()
			defer mu.Unlock()
			total := s.messages["sms"] + s.messages["email"]
			sent := p.Sent["sms"] + p.Sent["email"]
			switch {
			case phase == 0 && sent >= total/5:
				phase = 1
				e.prov.down()
				outageStart = time.Now()
				go func() {
					<-time.After(10 * time.Second)
					e.prov.up()
				}()
			case phase == 1 && time.Since(outageStart) > 14*time.Second && sent >= total*5/10:
				phase = 2
				admin, err := e.adminConn()
				if err != nil {
					t.Errorf("admin conn: %v", err)
					return
				}
				dbName := e.pool.Config().ConnConfig.Database
				_, _ = admin.Exec(context.Background(), `ALTER DATABASE `+dbName+` WITH ALLOW_CONNECTIONS false`)
				_, _ = admin.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
				outageWG.Go(func() {
					defer admin.Close(context.Background())
					<-time.After(2 * time.Second)
					readyDuringOutage.Store(int64(a.status("/readyz")))
					healthDuringOutage.Store(int64(a.status("/healthz")))
					<-time.After(6 * time.Second)
					_, _ = admin.Exec(context.Background(), `ALTER DATABASE `+dbName+` WITH ALLOW_CONNECTIONS true`)
					deadline := time.Now().Add(20 * time.Second)
					for time.Now().Before(deadline) {
						if readyAfter.Store(int64(a.status("/readyz"))); readyAfter.Load() == 200 {
							return
						}
						<-time.After(200 * time.Millisecond)
					}
				})
			}
		})
		outageWG.Wait()
		rd, hd, ra := int(readyDuringOutage.Load()), int(healthDuringOutage.Load()), int(readyAfter.Load())
		r.Duration = dur
		c, _ := e.counts(s.campaign)
		r.Params = append(r.Params, [2]string{"time to terminal", dur.Round(time.Second).String()},
			[2]string{"final status sms", fmt.Sprint(c["sms"])}, [2]string{"final status email", fmt.Sprint(c["email"])},
			[2]string{"/readyz during DB outage (want 503) / /healthz (want 200)", fmt.Sprintf("%d / %d", rd, hd)},
			[2]string{"/readyz after recovery (want 200)", strconv.Itoa(ra)})
		e.checkInvariants(r, s, done, []*worker{a, b}, nil, false)
		r.Inv = append(r.Inv, invariant{ID: "R-OBS-3", Text: "/readyz is 503 while the database is unreachable and recovers afterwards; /healthz stays 200",
			OK: rd == 503 && hd == 200 && ra == 200, Detail: fmt.Sprintf("during outage readyz=%d healthz=%d, after recovery readyz=%d", rd, hd, ra)})
		if rd != 503 || ra != 200 {
			t.Errorf("readiness: during=%d after=%d", rd, ra)
		}
		var failedByOutage int
		_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM messages WHERE campaign_id = $1 AND status = 'failed' AND last_error NOT LIKE 'http_400%'`, s.campaign).Scan(&failedByOutage)
		r.Notes = append(r.Notes, fmt.Sprintf("Messages that ended `failed` for a reason other than the seeded invalid numbers: %d (provider outage and DB outage together).", failedByOutage))
		e.stop(a, b)
		addResult(r)
	})
}

// adminConn opens a superuser session outside the test database (for ALTER DATABASE).
func (e *env) adminConn() (*pgx.Conn, error) {
	return pgx.Connect(context.Background(), os.Getenv(storetest.EnvURL))
}

func addResult(r *result) {
	resultsMu.Lock()
	results = append(results, r)
	resultsMu.Unlock()
}

// writeReport renders docs/stress/REPORT.md from the collected results.
func writeReport() {
	path := os.Getenv("STRESS_REPORT")
	if path == "" {
		path = filepath.Join("..", "..", "docs", "stress", "REPORT.md")
	}
	resultsMu.Lock()
	defer resultsMu.Unlock()
	if len(results) == 0 {
		return
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	var b strings.Builder
	fmt.Fprintf(&b, "# Delivery stress report\n\nGenerated by `cmd/worker/stress_test.go` on %s (`STRESS=1 go test -tags stress -run TestStress ./cmd/worker/`).\n", time.Now().Format("2006-01-02 15:04 MST"))
	b.WriteString("Real worker processes, in-process mock provider, throwaway Postgres database. Invariants are KICKOFF §10.2.\n\n")
	b.WriteString("| Scenario | Result | Time to terminal |\n|---|---|---|\n")
	for _, r := range results {
		ok := "PASS"
		for _, i := range r.Inv {
			if !i.OK {
				ok = "FAIL"
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", r.Name, ok, r.Duration.Round(time.Second))
	}
	for _, r := range results {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n\n| Parameter / measurement | Value |\n|---|---|\n", r.Name, r.Desc)
		for _, p := range r.Params {
			fmt.Fprintf(&b, "| %s | %s |\n", p[0], strings.ReplaceAll(p[1], "|", "/"))
		}
		b.WriteString("\n| Invariant | Result | Evidence |\n|---|---|---|\n")
		for _, i := range r.Inv {
			fmt.Fprintf(&b, "| %s %s | %s | %s |\n", i.ID, i.Text, ternary(i.OK, "pass", "**FAIL**"), strings.ReplaceAll(i.Detail, "|", "/"))
		}
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "\n%s\n", n)
		}
	}
	b.WriteString("\n" + reportFooter)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		_ = os.WriteFile(path, []byte(b.String()), 0o644)
	}
}

const reportFooter = `## Not covered here

S3 (approve storm), S4 (LLM adversarial), S5 (API load) and S6 (SSE fan-out) need the API and are run by the orchestrator;
S7 here replaces a Postgres container restart by refusing connections to the test database, because a worktree
builder must not touch the compose stack. The same invariant checks (I1-I7) apply to those runs.
`

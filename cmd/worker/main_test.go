package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"smartcampaign/internal/config"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/store/storetest"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// TestWorker_Binary_SendsAndStopsOnSIGTERM_RSND3 runs the real worker binary against an in-process mock
// provider: it must come up ready, deliver queued messages, complete the campaign, and exit 0 on SIGTERM.
func TestWorker_Binary_SendsAndStopsOnSIGTERM_RSND3(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()

	prov := mockprovider.New(config.ProviderFaults{Seed: 1}, metrics.New())
	provSrv := httptest.NewServer(prov.Handler())
	t.Cleanup(func() { prov.Close(); provSrv.Close() })

	// Campaign with 30 users on both channels.
	var camp string
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, status, language, channels, approved_content_version, approved_at)
		VALUES ('b', 'sending', 'tr', '{sms,email}', 1, now()) RETURNING id::text`).Scan(&camp); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO campaign_content (campaign_id, version, sms_text, email_subject, email_body, source, validation)
		 VALUES ('` + camp + `', 1, 'Merhaba {{first_name}}', 'Konu', 'Gövde {{city}}', 'llm', '{}')`,
		`INSERT INTO users (seed_key, first_name, email, phone, city, locale, birth_date, signup_at, loyalty_tier, sms_consent, email_consent)
		 SELECT 'u' || i, 'Ali', 'u' || i || '@example.com', '+90532' || lpad(i::text, 7, '0'), 'Bursa', 'tr', date '1990-01-01', now(), 'bronze', true, true
		 FROM generate_series(1, 30) i`,
		`INSERT INTO messages (campaign_id, user_id, channel, content_version)
		 SELECT '` + camp + `', u.id, ch, 1 FROM users u, unnest('{sms,email}'::text[]) ch`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	bin := filepath.Join(t.TempDir(), "worker")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cc := pool.Config().ConnConfig
	addr := freeAddr(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("DATABASE_URL=postgres://%s:%s@%s:%d/%s?sslmode=disable", cc.User, cc.Password, cc.Host, cc.Port, cc.Database),
		"WORKER_ADDR="+addr, "PROVIDER_URL="+provSrv.URL, "LOG_LEVEL=warn", "ANTHROPIC_API_KEY=")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	poll := func(what string, timeout time.Duration, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			<-tick.C
		}
	}
	get := func(path string) int {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	poll("/readyz 200", 20*time.Second, func() bool { return get("/readyz") == 200 })
	if get("/healthz") != 200 || get("/metrics") != 200 {
		t.Fatal("healthz/metrics not served")
	}
	poll("campaign completed", 20*time.Second, func() bool {
		var s string
		_ = pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, camp).Scan(&s)
		return s == "completed"
	})
	var sent int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE status = 'sent'`).Scan(&sent); err != nil || sent != 60 {
		t.Fatalf("sent = %d (%v), want 60", sent, err)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("worker exit after SIGTERM: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("worker did not exit within 15 s of SIGTERM")
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM worker_heartbeats`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("heartbeat rows left after clean shutdown: %d (%v)", rows, err)
	}
}

package eval

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/clock"
	"smartcampaign/internal/config"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/factory"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/seed"
	"smartcampaign/internal/store/storetest"
)

const briefsPath = "../../../docs/eval/briefs.yaml"

func TestLoad_GoldenSet_RAI1(t *testing.T) {
	cases, err := Load(briefsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 15 {
		t.Fatalf("golden set has %d briefs; KICKOFF §6.11 asks for at least 15", len(cases))
	}
	seen := map[string]bool{}
	tags := map[string]bool{}
	for _, c := range cases {
		if seen[c.ID] {
			t.Fatalf("duplicate id %s", c.ID)
		}
		seen[c.ID] = true
		for _, tag := range c.Tags {
			tags[tag] = true
		}
		if n := utf8.RuneCountInString(c.Brief); c.Pad > 0 && (n < c.Pad-200 || n > 4000) {
			t.Fatalf("%s: padded brief has %d runes", c.ID, n)
		}
	}
	for _, need := range []string{"official", "unsupported", "contradiction", "multi-condition", "consent", "injection", "edge", "long", "english"} {
		if !tags[need] {
			t.Fatalf("no brief tagged %q", need)
		}
	}
	if cases[0].Brief != llm.OfficialBrief {
		t.Fatal("the first case must be the official brief")
	}
}

func seededDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := storetest.NewDB(t)
	if _, err := seed.Run(context.Background(), pool, seed.Generate(5000, 20261108, time.Now())); err != nil {
		t.Fatal(err)
	}
	return pool
}

// The runner itself, with the deterministic mock: plumbing only, but it must score the official brief exactly.
func TestRun_MockPlumbing_RAI2(t *testing.T) {
	cases, err := Load(briefsPath)
	if err != nil {
		t.Fatal(err)
	}
	db := seededDB(t)
	svc := llm.NewService(mock.NewDemoWith(clock.Real{}, 0, 64), nil, llm.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	rs, err := Run(context.Background(), svc, db, cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != len(cases) {
		t.Fatalf("%d results for %d cases", len(rs), len(cases))
	}
	off := rs[0]
	if off.Exact == nil || !*off.Exact || off.ExpectedUsers == 0 || off.ContentValid == nil || !*off.ContentValid {
		t.Fatalf("official brief: %+v", off)
	}
	for _, r := range rs {
		if r.ID == "empty-brief" && (r.ErrorOK == nil || !*r.ErrorOK) {
			t.Fatalf("empty brief must fail validation: %+v", r)
		}
	}
	md := Markdown("mock", mock.DemoModel, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), rs)
	if len(md) < 500 {
		t.Fatal("report too short")
	}
}

// TestEvalReport is what `make eval PROVIDER=…` runs. It is skipped unless EVAL_PROVIDER is set; the anthropic
// provider additionally requires CONFIRM_SPEND=yes, which only the owner passes (need.md N-PLAN-014).
func TestEvalReport(t *testing.T) {
	provider := os.Getenv("EVAL_PROVIDER")
	if provider == "" {
		t.Skip("EVAL_PROVIDER not set; run `make eval PROVIDER=mock|claudecode|anthropic`")
	}
	if provider == config.ProviderAnthropic && os.Getenv("CONFIRM_SPEND") != "yes" {
		t.Fatal("PROVIDER=anthropic spends money: refusing without CONFIRM_SPEND=yes")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.LLMProvider = provider
	p, err := factory.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if provider == config.ProviderMock {
		p = mock.NewDemoWith(clock.Real{}, 0, 64)
	}
	cases, err := Load(briefsPath)
	if err != nil {
		t.Fatal(err)
	}
	db := seededDB(t)
	svc := llm.NewService(p, nil, llm.WithTimeouts(cfg.LLMSegmentTimeout, cfg.LLMContentTimeout))
	rs, err := Run(context.Background(), svc, db, cases)
	if err != nil {
		t.Fatal(err)
	}
	md := Markdown(p.Name(), p.Model(), time.Now(), rs)
	name := fmt.Sprintf("REPORT-%s-%s-%s.md", p.Name(), p.Model(), time.Now().Format("2006-01-02"))
	dir := os.Getenv("EVAL_REPORT_DIR")
	if dir == "" {
		dir = filepath.Dir(briefsPath)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s\n%s", filepath.Join(dir, name), md)
}

//go:build smokelive

package anthropic

import (
	"context"
	"os"
	"testing"
	"time"

	"smartcampaign/internal/llm"
)

// TestSmokeLive is `make smoke-live`: one real segment call and one real content call against the Claude API
// (KICKOFF §6.8). It never runs in `make test` (build tag smokelive). It refuses to run without
// CONFIRM_SPEND=yes, which only the owner passes (need.md N-PLAN-014), and skips without a key.
func TestSmokeLive(t *testing.T) {
	if os.Getenv("CONFIRM_SPEND") != "yes" {
		t.Fatal("smoke-live calls the paid Claude API: refusing without CONFIRM_SPEND=yes")
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY is empty: set it in .env to run the live smoke test")
	}
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-sonnet-5-5"
	}
	a, err := New(Options{APIKey: key, Model: model, RefusalFallback: os.Getenv("ANTHROPIC_REFUSAL_FALLBACK")})
	if err != nil {
		t.Fatal(err)
	}
	svc := llm.NewService(a, nil, llm.WithTimeouts(60*time.Second, 90*time.Second))
	ctx := context.Background()

	seg, err := svc.ProposeSegment(ctx, "", llm.OfficialBrief)
	if err != nil {
		t.Fatalf("segment call failed: %v", err)
	}
	t.Logf("segment: model=%s attempts=%d repaired=%v tokens in/out=%d/%d cache read/write=%d/%d cost=$%.5f latency=%dms",
		seg.Meta.Model, seg.Meta.Attempts, seg.Meta.Repaired, seg.Meta.InputTokens, seg.Meta.OutputTokens,
		seg.Meta.CacheReadTokens, seg.Meta.CacheWriteTokens, seg.Meta.CostUSD, seg.Meta.LatencyMS)
	t.Logf("rule: %+v", seg.Rule.Rule())

	deltas := 0
	cnt, err := svc.ProposeContent(ctx, llm.ContentInput{Brief: llm.OfficialBrief, Language: seg.Proposal.Language,
		Channels: []string{"sms", "email"}}, func(llm.Delta) { deltas++ }, nil)
	if err != nil {
		t.Fatalf("content call failed: %v", err)
	}
	if deltas < 2 {
		t.Fatalf("content was not streamed (%d deltas)", deltas)
	}
	t.Logf("content: deltas=%d attempts=%d cost=$%.5f sms=%q subject=%q", deltas, cnt.Meta.Attempts, cnt.Meta.CostUSD,
		cnt.Content.SMSText, cnt.Content.EmailSubject)
}

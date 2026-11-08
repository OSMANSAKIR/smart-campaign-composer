package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/goleak"

	"smartcampaign/internal/clock"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/metrics"
)

type memRecorder struct {
	mu    sync.Mutex
	calls []llm.Call
}

func (m *memRecorder) RecordCall(_ context.Context, c llm.Call) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, c)
	return nil
}

func (m *memRecorder) outcomes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, c := range m.calls {
		out = append(out, string(c.Purpose)+"/"+c.Outcome)
	}
	return out
}

func goldenJSON(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(llm.GoldenProposal())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const unknownFieldAnswer = `{"segment":{"groups":[{"conditions":[{"field":"likes_football","op":"is_true","value":null}]}]},
"unsupported_criteria":[],"assumptions":[],"channels_requested":"both","language":"tr","explanation":"x"}`

const badCityAnswer = `{"segment":{"groups":[{"conditions":[{"field":"city","op":"eq","value":"istanbul"}]}]},
"unsupported_criteria":[],"assumptions":[],"channels_requested":"both","language":"tr","explanation":"x"}`

func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func asLLMError(t *testing.T, err error) *llm.Error {
	t.Helper()
	var le *llm.Error
	if !errors.As(err, &le) {
		t.Fatalf("want *llm.Error, got %v", err)
	}
	return le
}

func TestService_RepairThen422_RAI1(t *testing.T) {
	p := mock.NewScripted(
		mock.Step{JSON: "not json at all"},
		mock.Step{JSON: unknownFieldAnswer},
		mock.Step{JSON: badCityAnswer},
	)
	rec := &memRecorder{}
	m := metrics.New()
	svc := llm.NewService(p, rec, llm.WithMetrics(m))
	_, err := svc.ProposeSegment(context.Background(), "", llm.OfficialBrief)
	le := asLLMError(t, err)
	if le.Code != "llm_invalid_output" {
		t.Fatalf("code = %s", le.Code)
	}
	if len(le.Details) != 1 || le.Details[0].Code != "invalid_enum_value" || le.Details[0].Path != "segment.groups[0].conditions[0].value" {
		t.Fatalf("details must be the last attempt's errors: %+v", le.Details)
	}
	if le.Meta.Attempts != 3 {
		t.Fatalf("attempts = %d, want 1 + %d repairs", le.Meta.Attempts, llm.MaxRepairs)
	}
	if got, want := rec.outcomes(), []string{"segment/invalid", "repair/invalid", "repair/invalid"}; !slices.Equal(got, want) {
		t.Fatalf("recorded = %v, want %v", got, want)
	}
	reqs := p.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d", len(reqs))
	}
	// Each repair turn carries the previous answer and the structured errors.
	last := reqs[2].Messages
	if len(last) != 5 || last[3].Role != "assistant" || last[3].Text != unknownFieldAnswer || !strings.Contains(last[4].Text, `"code":"unknown_field"`) {
		t.Fatalf("repair conversation = %+v", last)
	}
	if !strings.Contains(reqs[1].Messages[2].Text, `"code":"invalid_json"`) {
		t.Fatalf("first repair must carry invalid_json: %q", reqs[1].Messages[2].Text)
	}
	if counterValue(t, m.LLMRepairs) != 2 || counterValue(t, m.LLMValidationFails.WithLabelValues("unknown_field")) != 1 {
		t.Fatal("repair metrics not updated")
	}
	if counterValue(t, m.LLMRequests.WithLabelValues("repair", "mock", mock.ScriptedModel, "invalid")) != 2 {
		t.Fatal("llm_requests_total not updated")
	}
}

func TestService_RepairSucceeds_RAI1(t *testing.T) {
	p := mock.NewScripted(mock.Step{JSON: badCityAnswer}, mock.Step{JSON: goldenJSON(t)})
	rec := &memRecorder{}
	res, err := llm.NewService(p, rec).ProposeSegment(context.Background(), "c1", llm.OfficialBrief)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Meta.Repaired || res.Meta.Attempts != 2 || !res.Rule.Valid() {
		t.Fatalf("meta = %+v", res.Meta)
	}
	if got, want := rec.outcomes(), []string{"segment/invalid", "repair/repaired"}; !slices.Equal(got, want) {
		t.Fatalf("recorded = %v, want %v", got, want)
	}
	if rec.calls[0].CampaignID != "c1" || rec.calls[0].PromptVersion != llm.SegmentPromptVersion {
		t.Fatalf("call = %+v", rec.calls[0])
	}
}

func TestService_Refusal_RAI1(t *testing.T) {
	p := mock.NewScripted(mock.Step{StopReason: llm.StopRefusal, RefusalCategory: "cyber"}, mock.Step{JSON: "{}"})
	rec := &memRecorder{}
	_, err := llm.NewService(p, rec).ProposeSegment(context.Background(), "", "some brief")
	le := asLLMError(t, err)
	if le.Code != "llm_refused" || len(le.Details) != 1 || le.Details[0].Code != "cyber" {
		t.Fatalf("error = %+v", le)
	}
	if p.Remaining() != 1 {
		t.Fatal("a refusal must never be repaired")
	}
	if got := rec.outcomes(); !slices.Equal(got, []string{"segment/refused"}) {
		t.Fatalf("recorded = %v", got)
	}
	// Refusal without a category still maps to a stable code.
	_, err = llm.NewService(mock.NewScripted(mock.Step{StopReason: llm.StopRefusal}), nil).ProposeSegment(context.Background(), "", "x")
	if le := asLLMError(t, err); le.Details[0].Code != "unknown" {
		t.Fatalf("details = %+v", le.Details)
	}
}

func TestService_MaxTokensRetriesWithLargerLimit_RAI1(t *testing.T) {
	p := mock.NewScripted(mock.Step{JSON: `{"segment":{"gro`, StopReason: llm.StopMaxTokens}, mock.Step{JSON: goldenJSON(t)})
	res, err := llm.NewService(p, nil).ProposeSegment(context.Background(), "", llm.OfficialBrief)
	if err != nil {
		t.Fatal(err)
	}
	reqs := p.Requests()
	if reqs[1].MaxTokens != 2*reqs[0].MaxTokens || len(reqs[1].Messages) != 1 || reqs[1].Purpose != llm.PurposeRepair {
		t.Fatalf("second request = %+v", reqs[1])
	}
	if !res.Meta.Repaired {
		t.Fatal("want repaired")
	}
}

func TestService_TransportErrorIsNotRepaired_RAI1(t *testing.T) {
	p := mock.NewScripted(mock.Step{Err: errors.New("529 overloaded")}, mock.Step{JSON: "{}"})
	rec := &memRecorder{}
	_, err := llm.NewService(p, rec).ProposeSegment(context.Background(), "", "x")
	if le := asLLMError(t, err); le.Code != "llm_failed" {
		t.Fatalf("code = %s", le.Code)
	}
	if p.Remaining() != 1 || !slices.Equal(rec.outcomes(), []string{"segment/error"}) {
		t.Fatalf("remaining %d, recorded %v", p.Remaining(), rec.outcomes())
	}
}

func TestService_BriefChecked_RAI1(t *testing.T) {
	p := mock.NewScripted()
	svc := llm.NewService(p, nil)
	for _, brief := range []string{"", "   \n", strings.Repeat("ş", 4001)} {
		_, err := svc.ProposeSegment(context.Background(), "", brief)
		if le := asLLMError(t, err); le.Code != "validation_failed" || le.Details[0].Path != "brief" {
			t.Fatalf("brief %q: %+v", brief[:min(len(brief), 10)], le)
		}
	}
	if len(p.Requests()) != 0 {
		t.Fatal("an invalid brief must not reach the provider")
	}
}

const validContentJSON = `{"language":"tr","sms":{"text":"Merhaba {{first_name}}, sepetin seni bekliyor! %30 indirim bugün senin."},
"email":{"subject":"{{first_name}}, %30 indirim seni bekliyor","body":"Merhaba {{first_name}},\n\nSepetindeki ürünler seni bekliyor."},"notes":"kısa"}`

func contentInput() llm.ContentInput {
	return llm.ContentInput{Brief: llm.OfficialBrief, Language: "tr", SegmentSummary: []string{"Şehir: İstanbul"},
		Channels: []string{"sms", "email"}, WorstCase: content.Vars{"first_name": "Abdurrahman Kerimcan", "city": "İstanbul"}}
}

func TestStream_SplitMultibyteRune_RFLOW2(t *testing.T) {
	full := validContentJSON
	i := strings.Index(full, "ü") // 2-byte rune
	chunks := []string{full[:i+1], full[i+1 : i+5], full[i+5:]}
	if utf8.ValidString(chunks[0]) {
		t.Fatal("test setup: first chunk must end inside a rune")
	}
	p := mock.NewScripted(mock.Step{Chunks: chunks})
	var got []string
	res, err := llm.NewService(p, nil).ProposeContent(context.Background(), contentInput(),
		func(d llm.Delta) { got = append(got, d.JSONFragment) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if !utf8.ValidString(g) {
			t.Fatalf("delta %q cuts a rune", g)
		}
	}
	if strings.Join(got, "") != full {
		t.Fatal("deltas must concatenate to the full answer")
	}
	if !res.Report.Valid || res.Content.SMSText == "" {
		t.Fatalf("report = %+v", res.Report)
	}
}

func TestStream_CancelStopsProvider_RFLOW2(t *testing.T) {
	defer goleak.VerifyNone(t)
	p := mock.NewScripted(mock.Step{Chunks: []string{`{"language":"tr",`, `"sms":{}}`}, BlockAfterChunks: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &memRecorder{}
	done := make(chan error, 1)
	go func() {
		_, err := llm.NewService(p, rec).ProposeContent(ctx, contentInput(), func(llm.Delta) { cancel() }, nil)
		done <- err
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ProposeContent did not return after cancel")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled in the chain, got %v", err)
	}
	if p.Cancelled() != 1 || p.Remaining() != 0 {
		t.Fatalf("provider cancelled = %d", p.Cancelled())
	}
	if !slices.Equal(rec.outcomes(), []string{"content/error"}) {
		t.Fatalf("a cancelled call is still recorded: %v", rec.outcomes())
	}
}

func TestService_ContentRepairNotice_RCNT1(t *testing.T) {
	tooLong := `{"language":"tr","sms":{"text":"` + strings.Repeat("a", 300) + `"},"email":{"subject":"s","body":"b"},"notes":""}`
	p := mock.NewScripted(mock.Step{JSON: tooLong}, mock.Step{JSON: validContentJSON})
	var notices []llm.RepairNotice
	res, err := llm.NewService(p, nil).ProposeContent(context.Background(), contentInput(), nil,
		func(n llm.RepairNotice) { notices = append(notices, n) })
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Attempt != 1 || !strings.Contains(string(notices[0].Errors), `"too_long"`) {
		t.Fatalf("notices = %+v", notices)
	}
	if !res.Meta.Repaired || !res.Report.ValidFor["sms"] {
		t.Fatalf("result = %+v", res)
	}
}

func TestService_ContentOnlyRequestedChannelsBlock_RCNT1(t *testing.T) {
	badSMS := `{"language":"tr","sms":{"text":"` + strings.Repeat("a", 300) + `"},"email":{"subject":"Konu","body":"Gövde"},"notes":""}`
	in := contentInput()
	in.Channels = []string{"email"}
	res, err := llm.NewService(mock.NewScripted(mock.Step{JSON: badSMS}), nil).ProposeContent(context.Background(), in, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.ValidFor["sms"] || !res.Report.ValidFor["email"] || res.Meta.Attempts != 1 {
		t.Fatalf("an invalid SMS must not trigger a repair for an email-only campaign: %+v", res.Report)
	}
}

func TestMockDemo_OfficialBrief_RFLOW1(t *testing.T) {
	demo := mock.NewDemoWith(clock.Real{}, 0, 5)
	rec := &memRecorder{}
	svc := llm.NewService(demo, rec)
	res, err := svc.ProposeSegment(context.Background(), "", llm.OfficialBrief)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(res.Rule.Rule())
	want, _ := json.Marshal(llm.GoldenProposal().Segment)
	if string(got) != string(want) {
		t.Fatalf("rule:\n got %s\nwant %s", got, want)
	}
	if res.Proposal.Language != "tr" || res.Proposal.ChannelsRequested != "both" || len(res.Proposal.Assumptions) == 0 {
		t.Fatalf("proposal = %+v", res.Proposal)
	}
	var deltas []string
	cres, err := svc.ProposeContent(context.Background(), contentInput(), func(d llm.Delta) { deltas = append(deltas, d.JSONFragment) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cres.Report.ValidFor["sms"] || !cres.Report.ValidFor["email"] || cres.Meta.Attempts != 1 {
		t.Fatalf("demo copy must pass validation first time: %+v", cres.Report)
	}
	if len(deltas) < 10 || !strings.Contains(cres.Content.SMSText, "%30") {
		t.Fatalf("demo must stream in small chunks with the brief's offer: %d deltas, sms %q", len(deltas), cres.Content.SMSText)
	}
	if got := rec.outcomes(); !slices.Equal(got, []string{"segment/valid", "content/valid"}) {
		t.Fatalf("recorded = %v", got)
	}
}

func TestMockDemo_OtherBriefsValidate_RFLOW1(t *testing.T) {
	svc := llm.NewService(mock.NewDemoWith(clock.Real{}, 0, 50), nil)
	briefs := map[string]string{
		"Email our gold members in Ankara or İzmir who haven't bought anything for 60 days and love football.": "en",
		"25 yaş altı elektronik meraklılarına SMS gönder":                                                      "tr",
		"Herkese gönder, izin vermeyenler dahil":                                                               "tr",
		"Send a thank-you note to platinum customers":                                                          "en",
	}
	for brief, lang := range briefs {
		res, err := svc.ProposeSegment(context.Background(), "", brief)
		if err != nil {
			t.Fatalf("%q: %v", brief, err)
		}
		if res.Proposal.Language != lang || res.Meta.Attempts != 1 {
			t.Fatalf("%q: proposal %+v meta %+v", brief, res.Proposal, res.Meta)
		}
		c, err := svc.ProposeContent(context.Background(), llm.ContentInput{Brief: brief, Language: lang}, nil, nil)
		if err != nil || !c.Report.Valid {
			t.Fatalf("%q content: %v %+v", brief, err, c.Report)
		}
	}
}

func TestMockDemo_StreamingStopsOnCancel_RFLOW2(t *testing.T) {
	defer goleak.VerifyNone(t)
	fake := clock.NewFake(time.Unix(0, 0))
	demo := mock.NewDemoWith(fake, time.Second, 3)
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	_, err := llm.NewService(demo, nil).ProposeContent(ctx, contentInput(), func(llm.Delta) {
		n++
		cancel() // the fake clock never advances: only cancellation can end the wait
	}, nil)
	if !errors.Is(err, context.Canceled) || n != 1 {
		t.Fatalf("err = %v, deltas = %d", err, n)
	}
}

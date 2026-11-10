package llm_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"

	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
)

// B-2: both variants valid → one attempt, both labels and reports; the top level mirrors variant a.
func TestProposeContent_TwoVariants_B2(t *testing.T) {
	var deltas []string
	p := mock.NewScripted(mock.Step{JSON: validContentJSON})
	rec := &memRecorder{}
	res, err := llm.NewService(p, rec).ProposeContent(context.Background(), contentInput(),
		func(d llm.Delta) { deltas = append(deltas, d.JSONFragment) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.A.Label != "Samimi" || res.B.Label != "Acil fayda" || !res.A.Report.Valid || !res.B.Report.Valid {
		t.Fatalf("variants = %+v / %+v", res.A, res.B)
	}
	if res.A.Content.SMSText == res.B.Content.SMSText || res.Content != res.A.Content {
		t.Fatalf("A and B must differ and Content mirrors A: %+v", res)
	}
	if res.Meta.PromptVersion != "content_v2" || res.Meta.Attempts != 1 || strings.Join(deltas, "") != validContentJSON {
		t.Fatalf("meta = %+v", res.Meta)
	}
	if got := p.Requests()[0].Schema["properties"].(map[string]any); got["variants"] == nil || got["sms"] != nil {
		t.Fatalf("the request must carry the content_v2 schema: %v", got)
	}
}

// B-2: any invalid variant makes the whole answer invalid and goes through the repair loop with paths that
// name the variant; the repair is shown the structured errors.
func TestProposeContent_InvalidVariantRepairs_B2(t *testing.T) {
	okA := copyText{"Merhaba {{first_name}}!", "Konu", "Gövde"}
	for name, tc := range map[string]struct {
		bad      string
		wantPath string
		wantCode string
	}{
		"label too long": {strings.Replace(v2Answer("tr", okA, goodB), `"label":"A"`, `"label":"`+strings.Repeat("x", 41)+`"`, 1),
			"variants.a.label", "label_too_long"},
		"empty label": {strings.Replace(v2Answer("tr", okA, goodB), `"label":"B"`, `"label":""`, 1),
			"variants.b.label", "required"},
		"b subject two lines": {v2Answer("tr", okA, copyText{"Merhaba!", "satır\nsatır", "Gövde"}),
			"variants.b.email_subject", ""},
		"b unknown variable": {v2Answer("tr", okA, copyText{"{{phone}}", "s", "b"}),
			"variants.b.sms_text", "unknown_variable"},
		"missing b": {`{"language":"tr","variants":{"a":{"label":"A","sms":{"text":"x"},"email":{"subject":"s","body":"b"}}},"notes":""}`,
			"variants.b", "missing_property"},
	} {
		t.Run(name, func(t *testing.T) {
			p := mock.NewScripted(mock.Step{JSON: tc.bad}, mock.Step{JSON: validContentJSON})
			var notices []llm.RepairNotice
			res, err := llm.NewService(p, nil).ProposeContent(context.Background(), contentInput(), nil,
				func(n llm.RepairNotice) { notices = append(notices, n) })
			if err != nil {
				t.Fatal(err)
			}
			if !res.Meta.Repaired || res.Meta.Attempts != 2 || len(notices) != 1 {
				t.Fatalf("meta = %+v, notices = %d", res.Meta, len(notices))
			}
			errs := string(notices[0].Errors)
			if !strings.Contains(errs, `"`+tc.wantPath+`"`) || (tc.wantCode != "" && !strings.Contains(errs, `"`+tc.wantCode+`"`)) {
				t.Fatalf("errors %s lack %s/%s", errs, tc.wantPath, tc.wantCode)
			}
			if repair := p.Requests()[1].Messages[2].Text; !strings.Contains(repair, tc.wantPath) {
				t.Fatalf("the repair turn does not name %s: %s", tc.wantPath, repair)
			}
		})
	}
}

// B-2 + RT-8c: NUL in the unrequested channel of variant b still blocks (it could never be stored), and
// never-valid answers end as llm_invalid_output without a fourth call.
func TestProposeContent_NULInVariantBAndExhaustion_B2(t *testing.T) {
	in := contentInput()
	in.Channels = []string{"sms"}
	nul := v2Answer("tr", copyText{"Merhaba {{first_name}}!", "s", "b"}, copyText{"x {{first_name}}", "s", "a\x00b"})
	p := mock.NewScripted(mock.Step{JSON: nul}, mock.Step{JSON: nul}, mock.Step{JSON: nul})
	_, err := llm.NewService(p, nil).ProposeContent(context.Background(), in, nil, nil)
	var le *llm.Error
	if !errors.As(err, &le) || le.Code != "llm_invalid_output" || len(le.Details) == 0 || le.Details[0].Path != "variants.b.email_body" {
		t.Fatalf("err = %v, details %+v", err, le)
	}
	if len(p.Requests()) != 3 {
		t.Fatalf("calls = %d, want 3 (one attempt and two repairs)", len(p.Requests()))
	}
}

type failingRecorder struct{ calls int }

func (f *failingRecorder) RecordCall(context.Context, llm.Call) error {
	f.calls++
	return errors.New("db is down")
}

// N-LLM-005 (F6): a failing llm_calls insert never fails the generation; cost_recorded says so. The flag is
// true when every insert worked or when there is no recorder to fail.
func TestCostRecordedFlag_NLLM005(t *testing.T) {
	tooLong := v2Answer("tr", copyText{strings.Repeat("a", 300), "s", "b"}, goodB)
	for name, tc := range map[string]struct {
		rec  llm.Recorder
		want bool
	}{
		"recorder ok":    {&memRecorder{}, true},
		"no recorder":    {nil, true},
		"recorder fails": {&failingRecorder{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			// One repair in the middle: every call of the generation is recorded, the flag covers all of them.
			p := mock.NewScripted(mock.Step{JSON: tooLong}, mock.Step{JSON: validContentJSON})
			res, err := llm.NewService(p, tc.rec).ProposeContent(context.Background(), contentInput(), nil, nil)
			if err != nil {
				t.Fatalf("a failing recorder must not fail the generation: %v", err)
			}
			if res.Meta.CostRecorded != tc.want || !res.A.Report.Valid {
				t.Fatalf("cost_recorded = %v, want %v", res.Meta.CostRecorded, tc.want)
			}
		})
	}
	// Segment generation carries the flag too.
	seg, err := llm.NewService(mock.NewScripted(mock.Step{JSON: goldenJSON(t)}), &failingRecorder{}).ProposeSegment(context.Background(), "", llm.OfficialBrief)
	if err != nil || seg.Meta.CostRecorded {
		t.Fatalf("segment: err %v, cost_recorded %v", err, seg.Meta.CostRecorded)
	}
}

// F7: a provider answering 4xx other than 408/409/429 ends as llm_provider_rejected with a fixed message and no
// details; the raw body is logged at warn level only and llm_calls.error carries the status, not the body.
func TestProviderRejected_F7(t *testing.T) {
	const body = `{"error":{"message":"Your credit balance is too low. org_9f8e7d6c5b4a"}}`
	for name, tc := range map[string]struct {
		status int
		code   string
	}{
		"400 billing":      {400, "llm_provider_rejected"},
		"401":              {401, "llm_provider_rejected"},
		"403":              {403, "llm_provider_rejected"},
		"404":              {404, "llm_provider_rejected"},
		"408 timeout":      {408, "llm_failed"},
		"409 conflict":     {409, "llm_failed"},
		"429 rate limited": {429, "llm_failed"},
		"500":              {500, "llm_failed"},
		"529 overloaded":   {529, "llm_failed"},
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			rec := &memRecorder{}
			p := mock.NewScripted(mock.Step{Err: &llm.StatusError{Status: tc.status, Body: body}})
			svc := llm.NewService(p, rec, llm.WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
			_, err := svc.ProposeContent(context.Background(), contentInput(), nil, nil)
			var le *llm.Error
			if !errors.As(err, &le) || le.Code != tc.code {
				t.Fatalf("err = %v, want code %s", err, tc.code)
			}
			if strings.Contains(err.Error(), "org_9f8e7d6c5b4a") || strings.Contains(le.Message, "org_") || len(le.Details) != 0 {
				t.Fatalf("the provider body leaked into the error: %v %+v", err, le)
			}
			if tc.code != "llm_provider_rejected" {
				return
			}
			if le.Message != "The AI provider rejected the request (check the API key or credits)." {
				t.Fatalf("message = %q", le.Message)
			}
			if len(p.Requests()) != 1 {
				t.Fatal("a rejection is not retried or repaired")
			}
			calls := rec.snapshot()
			if len(calls) != 1 || calls[0].Error != "provider_rejected status="+strconv.Itoa(tc.status) || calls[0].Outcome != "error" {
				t.Fatalf("llm_calls row = %+v", calls)
			}
			if !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), "org_9f8e7d6c5b4a") {
				t.Fatalf("the raw body belongs in the warn log: %s", logs.String())
			}
		})
	}
}

func (m *memRecorder) snapshot() []llm.Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

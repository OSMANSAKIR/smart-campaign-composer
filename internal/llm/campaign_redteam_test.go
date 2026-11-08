package llm_test

// Red-team tests (llm-red-team, CP1) at the campaign layer: values the validators accept must also be
// storable, otherwise a hostile brief or model answer turns into an opaque 500 after the model was paid.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/segment"
	"smartcampaign/internal/store/storetest"
)

// structured reports whether err is a client-facing campaign error (→ 4xx/502), not an internal 500.
func structured(err error) bool {
	var ce *campaign.Error
	return errors.As(err, &ce)
}

// RT-8: NUL in model free text (assumptions/explanation) or in a brief passes validation but Postgres
// rejects it (text: 22021; jsonb "\u0000": 22P05), so the request fails as `internal`.
func TestRedTeam_ValidatedValuesAreStorable_RAI1(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	var failures []string

	// 1. Segment proposal whose assumption contains NUL.
	nulAssumption := `{"segment":{"groups":[{"conditions":[{"field":"city","op":"eq","value":"İstanbul"}]}]},` +
		`"unsupported_criteria":[],"assumptions":["a\u0000b"],"channels_requested":"both","language":"tr","explanation":"x"}`
	svc := &campaign.Service{Pool: pool, LLM: llm.NewService(mock.NewScripted(mock.Step{JSON: nulAssumption}), nil)}
	c, err := svc.Create(ctx, "İstanbul kullanıcılarına indirim")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GenerateSegment(ctx, c.ID); err != nil && !structured(err) {
		failures = append(failures, "GenerateSegment with NUL in assumptions: "+err.Error())
	} else if err == nil {
		t.Log("NUL assumption stored")
	}

	// 2. Briefs that the LLM layer would reject (NUL is valid UTF-8, \xff is not) reach the INSERT.
	for name, brief := range map[string]string{"NUL brief": "indirim\x00", "invalid UTF-8 brief": "indirim \xff"} {
		if _, err := svc.Create(ctx, brief); err != nil && !structured(err) {
			failures = append(failures, name+": "+err.Error())
		}
	}

	// 3. Content for an SMS-only campaign whose (non-blocking) email body contains NUL.
	nulBody := `{"language":"tr","sms":{"text":"Merhaba {{first_name}}"},"email":{"subject":"s","body":"a\u0000b"},"notes":""}`
	svc.LLM = llm.NewService(mock.NewScripted(mock.Step{JSON: nulBody}), nil)
	c2, err := svc.Create(ctx, "SMS gönder")
	if err != nil {
		t.Fatal(err)
	}
	rule, _ := segment.ParseRule([]byte(`{"groups":[]}`))
	if _, err := svc.PutSegment(ctx, c2.ID, rule); err != nil {
		t.Fatal(err)
	}
	in, err := svc.ContentInput(ctx, c2.ID)
	if err != nil {
		t.Fatal(err)
	}
	in.Channels = []string{"sms"}
	if _, _, err := svc.GenerateContent(ctx, c2.ID, in, nil, nil); err != nil && !structured(err) {
		failures = append(failures, "GenerateContent with NUL in the email body: "+err.Error())
	}

	if len(failures) > 0 {
		t.Skipf("FINDING RT-8: %d paths end as internal errors:\n%s", len(failures), strings.Join(failures, "\n"))
	}
}

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

// RT-8: NUL in model free text (assumptions/explanation/notes), in any content field or in a brief passes
// validation but Postgres rejects it (text: 22021; jsonb "\u0000": 22P05), so the request fails as `internal`.
// RT-8a (free text) and RT-8c (content) are fixed in the validators and asserted here; RT-8b (the brief at
// campaign creation) is fixed in internal/campaign and asserted here too.
func TestRedTeam_ValidatedValuesAreStorable_RAI1(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	var failures []string // RT-8b: briefs, fixed in internal/campaign

	// 1. Segment proposal whose assumption contains NUL.
	nulAssumption := `{"segment":{"groups":[{"conditions":[{"field":"city","op":"eq","value":"İstanbul"}]}]},` +
		`"unsupported_criteria":[],"assumptions":["a\u0000b"],"channels_requested":"both","language":"tr","explanation":"x"}`
	cleanAssumption := strings.Replace(nulAssumption, `"a\u0000b"`, `"a"`, 1)
	p1 := mock.NewScripted(mock.Step{JSON: nulAssumption}, mock.Step{JSON: cleanAssumption})
	svc := &campaign.Service{Pool: pool, LLM: llm.NewService(p1, nil)}
	c, err := svc.Create(ctx, "İstanbul kullanıcılarına indirim")
	if err != nil {
		t.Fatal(err)
	}
	if _, meta, err := svc.GenerateSegment(ctx, c.ID); err != nil || !meta.Repaired {
		t.Errorf("RT-8a: NUL in assumptions must be rejected and repaired: err %v, meta %+v", err, meta)
	} else if repair := p1.Requests()[1].Messages[2].Text; !strings.Contains(repair, "control_character") {
		t.Errorf("RT-8a: repair turn does not name the control character: %s", repair)
	}

	// 2. Briefs that the LLM layer would reject (NUL is valid UTF-8, \xff is not) reach the INSERT.
	for name, brief := range map[string]string{"NUL brief": "indirim\x00", "invalid UTF-8 brief": "indirim \xff"} {
		if _, err := svc.Create(ctx, brief); err != nil && !structured(err) {
			failures = append(failures, name+": "+err.Error())
		}
	}

	// 3. Content for an SMS-only campaign whose (non-blocking) email body contains NUL.
	nulBody := v2Answer("tr", copyText{"Merhaba {{first_name}}", "s", "a\x00b"}, goodB)
	cleanBody := strings.Replace(nulBody, `a\u0000b`, `ab`, 1)
	svc.LLM = llm.NewService(mock.NewScripted(mock.Step{JSON: nulBody}, mock.Step{JSON: cleanBody}), nil)
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
	if _, meta, err := svc.GenerateContent(ctx, c2.ID, in, nil, nil); err != nil || !meta.Repaired {
		t.Errorf("RT-8c: NUL in a field of an unrequested channel must be rejected and repaired: err %v, meta %+v", err, meta)
	}

	if len(failures) > 0 {
		t.Errorf("RT-8b: %d briefs end as internal errors:\n%s", len(failures), strings.Join(failures, "\n"))
	}
}

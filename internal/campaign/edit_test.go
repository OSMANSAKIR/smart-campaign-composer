package campaign

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"smartcampaign/internal/segment"
)

const officialBrief = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara indirim kampanyası"

// TestManualRuleEdit_KeepsClaudesRuleForAudit_RFLOW1 (F2, need.md N-WEB-001 B): a human edit is validated by
// the same validator, recorded (source edited, edited_at, Claude's original rule kept across further edits)
// and used by content generation; a new generation starts a fresh audit trail.
func TestManualRuleEdit_KeepsClaudesRuleForAudit_RFLOW1(t *testing.T) {
	svc, _ := newService(t, 2000)
	ctx := context.Background()
	c, err := svc.Create(ctx, officialBrief)
	if err != nil {
		t.Fatal(err)
	}
	gen, _, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gen.Segment.Source != "llm" || gen.Segment.OriginalRule != nil || gen.Segment.EditedAt != nil {
		t.Fatalf("generated segment meta %+v", gen.Segment.SegmentMeta)
	}
	claude, _ := json.Marshal(gen.Segment.Rule)

	parse := func(raw string) segment.Rule {
		r, issues := segment.ParseRule([]byte(raw))
		if len(issues) > 0 {
			t.Fatalf("%s: %v", raw, issues)
		}
		return r
	}
	ankara := parse(`{"groups":[{"conditions":[{"field":"city","op":"eq","value":"Ankara"}]}]}`)
	ed, err := svc.PutSegment(ctx, c.ID, ankara)
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := json.Marshal(ed.Segment.OriginalRule)
	if ed.Segment.Source != "edited" || ed.Segment.EditedAt == nil || string(orig) != string(claude) {
		t.Fatalf("after edit: source %q edited_at %v original %s, want edited + Claude's %s", ed.Segment.Source, ed.Segment.EditedAt, orig, claude)
	}
	if ed.Segment.Audience.Total == gen.Segment.Audience.Total {
		t.Fatalf("audience did not change: %d", ed.Segment.Audience.Total)
	}
	if len(ed.Segment.Assumptions) != len(gen.Segment.Assumptions) {
		t.Fatalf("Claude's notes about the brief were dropped")
	}

	// A second edit keeps Claude's rule, not the first human edit.
	ed2, err := svc.PutSegment(ctx, c.ID, parse(`{"groups":[{"conditions":[{"field":"city","op":"in","value":["Ankara","İzmir"]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if orig2, _ := json.Marshal(ed2.Segment.OriginalRule); string(orig2) != string(claude) {
		t.Fatalf("second edit replaced the original rule: %s", orig2)
	}

	// Content generation is built from the stored (edited) rule.
	in, err := svc.ContentInput(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(in.SegmentSummary, " · "); !strings.Contains(s, "Ankara") || strings.Contains(s, "İstanbul") {
		t.Fatalf("content prompt sees %q, want the edited rule", s)
	}

	// A new generation is Claude's rule again: no audit trail to keep.
	regen, _, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if regen.Segment.Source != "llm" || regen.Segment.OriginalRule != nil || regen.Segment.EditedAt != nil {
		t.Fatalf("regenerated meta %+v", regen.Segment.SegmentMeta)
	}
}

// TestPreviewSegment_SameValidatorNeverSaves_RFLOW1: the editor's live preview uses the model-output validator,
// reports problems as data, counts valid rules and never touches a campaign.
func TestPreviewSegment_SameValidatorNeverSaves_RFLOW1(t *testing.T) {
	svc, pool := newService(t, 1000)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		rule  string
		valid bool
		code  string
	}{
		"valid city":    {`{"groups":[{"conditions":[{"field":"city","op":"eq","value":"İzmir"}]}]}`, true, ""},
		"everyone":      {`{"groups":[]}`, true, ""},
		"bad op":        {`{"groups":[{"conditions":[{"field":"city","op":"gt","value":"İzmir"}]}]}`, false, "op_type_mismatch"},
		"out of range":  {`{"groups":[{"conditions":[{"field":"age","op":"gt","value":500}]}]}`, false, "out_of_range"},
		"contradiction": {`{"groups":[{"conditions":[{"field":"age","op":"gt","value":60}]},{"conditions":[{"field":"age","op":"lt","value":20}]}]}`, false, "contradiction"},
		"unknown enum":  {`{"groups":[{"conditions":[{"field":"city","op":"eq","value":"Gotham"}]}]}`, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			r, issues := segment.ParseRule([]byte(tc.rule))
			if len(issues) > 0 {
				t.Fatal(issues)
			}
			p, err := svc.PreviewSegment(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if p.Valid != tc.valid || (p.Audience != nil) != tc.valid || (len(p.Rendered["tr"]) > 0) != tc.valid {
				t.Fatalf("preview %+v", p)
			}
			codes := []string{}
			for _, e := range p.Errors {
				codes = append(codes, e.Code)
			}
			if tc.code != "" && !slices.Contains(codes, tc.code) {
				t.Fatalf("error codes %v, want %s", codes, tc.code)
			}
			if !tc.valid && len(p.Errors) == 0 {
				t.Fatal("invalid rule without errors")
			}
		})
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM campaigns`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("preview wrote campaigns: %d %v", n, err)
	}
}

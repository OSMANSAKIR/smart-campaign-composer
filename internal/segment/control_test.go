package segment

import "testing"

func TestValidateProposal_ControlCharactersInFreeText_RAI1(t *testing.T) {
	base := func() Proposal {
		return Proposal{Segment: Rule{Groups: []Group{}}, UnsupportedCriteria: []string{}, Assumptions: []string{},
			ChannelsRequested: "both", Language: "tr", Explanation: "x"}
	}
	tests := []struct {
		name     string
		mutate   func(*Proposal)
		wantPath string // empty = valid
	}{
		{"clean text with newline and tab", func(p *Proposal) { p.Assumptions = []string{"a\nb\tc"} }, ""},
		{"NUL in assumptions", func(p *Proposal) { p.Assumptions = []string{"ok", "a\x00b"} }, "assumptions[1]"},
		{"NUL in unsupported_criteria", func(p *Proposal) { p.UnsupportedCriteria = []string{"a\x00b"} }, "unsupported_criteria[0]"},
		{"NUL in explanation", func(p *Proposal) { p.Explanation = "a\x00b" }, "explanation"},
		{"escape in explanation", func(p *Proposal) { p.Explanation = "a\x1bb" }, "explanation"},
		{"C1 control in assumptions", func(p *Proposal) { p.Assumptions = []string{"a\u0085b"} }, "assumptions[0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base()
			tt.mutate(&p)
			_, errs, _ := ValidateProposal(p)
			if tt.wantPath == "" {
				if len(errs) != 0 {
					t.Fatalf("unexpected errors %+v", errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Code != "control_character" || errs[0].Path != tt.wantPath {
				t.Fatalf("errors %+v, want one control_character at %s", errs, tt.wantPath)
			}
		})
	}
}

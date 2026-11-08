package segment

import (
	"slices"
	"strings"
	"testing"
)

const goldenProposal = `{
  "segment": {"groups": [
    {"conditions": [{"field": "city", "op": "eq", "value": "İstanbul"}]},
    {"conditions": [{"field": "last_cart_add_at", "op": "within_last_days", "value": 30}]},
    {"conditions": [{"field": "cart_abandoned", "op": "is_true", "value": null}]}
  ]},
  "unsupported_criteria": [],
  "assumptions": ["'alışveriş yapmayan' read as no purchase since the cart add; the other reading is no purchase in the last 30 days"],
  "channels_requested": "both",
  "language": "tr",
  "explanation": "İstanbul users with an abandoned cart from the last 30 days."
}`

func TestParse_Golden_RAI1(t *testing.T) {
	p, issues := Parse([]byte(goldenProposal))
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	vr, errs, warns := ValidateProposal(p)
	if len(errs) != 0 || len(warns) != 0 || !vr.Valid() {
		t.Fatalf("errs=%v warns=%v", errs, warns)
	}
	if len(vr.Rule().Groups) != 3 || p.Language != "tr" || p.ChannelsRequested != "both" {
		t.Fatalf("unexpected proposal %+v", p)
	}
}

func TestParse_Strict_RAI1(t *testing.T) {
	replace := func(old, repl string) string {
		if !strings.Contains(goldenProposal, old) {
			t.Fatalf("fixture does not contain %q", old)
		}
		return strings.Replace(goldenProposal, old, repl, 1)
	}
	tests := []struct {
		name string
		raw  string
		code string
		path string
	}{
		{"not json", `{"segment": `, "invalid_json", ""},
		{"empty", ``, "invalid_json", ""},
		{"markdown fence", "```json\n" + goldenProposal + "\n```", "invalid_json", ""},
		{"trailing data", goldenProposal + ` {"x":1}`, "invalid_json", ""},
		{"two documents", goldenProposal + goldenProposal, "invalid_json", ""},
		{"array root", `[]`, "wrong_type", ""},
		{"unknown top-level", replace(`"language": "tr",`, `"language": "tr", "sql": "SELECT 1",`), "unknown_property", "sql"},
		{"unknown in condition", replace(`"op": "eq", "value": "İstanbul"`, `"op": "eq", "value": "İstanbul", "raw_sql": "1=1"`), "unknown_property", "segment.groups[0].conditions[0].raw_sql"},
		{"missing value", replace(`"op": "is_true", "value": null`, `"op": "is_true"`), "missing_property", "segment.groups[2].conditions[0].value"},
		{"missing explanation", replace(`,
  "explanation": "İstanbul users with an abandoned cart from the last 30 days."`, ``), "missing_property", "explanation"},
		{"groups not array", `{"segment":{"groups":{}},"unsupported_criteria":[],"assumptions":[],"channels_requested":"both","language":"tr","explanation":""}`, "wrong_type", "segment.groups"},
		{"field number", replace(`"field": "city"`, `"field": 7`), "wrong_type", "segment.groups[0].conditions[0].field"},
		{"assumption object", replace(`"assumptions": [`, `"assumptions": [{}, `), "wrong_type", "assumptions[0]"},
		{"null list", replace(`"unsupported_criteria": []`, `"unsupported_criteria": null`), "wrong_type", "unsupported_criteria"},
		{"too large", `{"x":"` + strings.Repeat("a", MaxRawBytes) + `"}`, "invalid_json", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, issues := Parse([]byte(tt.raw))
			if len(issues) == 0 {
				t.Fatal("want issues")
			}
			if !slices.ContainsFunc(issues, func(i Issue) bool { return i.Code == tt.code && i.Path == tt.path }) {
				t.Fatalf("want %s at %q, got %+v", tt.code, tt.path, issues)
			}
		})
	}
}

func TestParseRule_PathsUseSegmentPrefix_RAI1(t *testing.T) {
	r, issues := ParseRule([]byte(`{"groups":[{"conditions":[{"field":"age","op":"gt","value":30}]}]}`))
	if len(issues) != 0 || len(r.Groups) != 1 {
		t.Fatalf("issues=%v rule=%+v", issues, r)
	}
	_, issues = ParseRule([]byte(`{"groups":[{"conditions":[{"field":"age","op":"gt"}]}]}`))
	if len(issues) != 1 || issues[0].Path != "segment.groups[0].conditions[0].value" {
		t.Fatalf("issues=%+v", issues)
	}
}

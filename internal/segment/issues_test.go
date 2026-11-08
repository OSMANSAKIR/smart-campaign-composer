package segment

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCapIssues_AppendsTooManyOnce_RAI1(t *testing.T) {
	mk := func(n int) []Issue {
		out := make([]Issue, n)
		for i := range out {
			out[i] = Issue{Path: "p", Code: "x", Message: "m"}
		}
		return out
	}
	tests := []struct {
		name    string
		in      int
		wantLen int
		wantMsg string
	}{
		{"within the cap", 20, 20, ""},
		{"one over", 21, 21, "and 1 more"},
		{"thousands", 5000, 21, "and 4980 more"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CapIssues(mk(tt.in), MaxIssues)
			if len(got) != tt.wantLen {
				t.Fatalf("len %d, want %d", len(got), tt.wantLen)
			}
			if tt.wantMsg == "" {
				return
			}
			last := got[len(got)-1]
			if last.Code != CodeTooManyErrors || !strings.Contains(last.Message, tt.wantMsg) {
				t.Fatalf("last issue %+v", last)
			}
			if again := CapIssues(got, MaxIssues); len(again) != len(got) || again[len(again)-1] != last {
				t.Fatalf("not idempotent: %+v", again[len(again)-1])
			}
		})
	}
}

func TestSafeText_RAI1(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain", "abc", "abc"},
		{"nul and control", "a\x00b\nc\td", "abcd"},
		{"bidi override", "a\u202eb", "ab"},
		{"line separator", "a\u2028b\u2029c", "abc"},
		{"zero width", "a\u200bb", "ab"},
		{"invalid utf8", "a\xffb", "ab"},
		{"cut at 40 runes", strings.Repeat("ş", 41), strings.Repeat("ş", 40) + "…"},
		{"exactly 40", strings.Repeat("ş", 40), strings.Repeat("ş", 40)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeText(tt.in, MaxQuotedRunes)
			if got != tt.want || !utf8.ValidString(got) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

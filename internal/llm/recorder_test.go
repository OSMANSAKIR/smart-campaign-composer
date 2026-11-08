package llm

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateErr_StorableInPostgres_RAI1(t *testing.T) {
	tests := []struct{ name, in string }{
		{"nul", "segment.a\x00b:unknown_property"},
		{"invalid utf8", "x\xff\xfe"},
		{"long with multibyte at the cut", strings.Repeat("ş", 400)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateErr(tt.in)
			if strings.ContainsRune(got, 0) || !utf8.ValidString(got) || len(got) > 500 {
				t.Fatalf("not storable: %q (%d bytes)", got, len(got))
			}
		})
	}
}

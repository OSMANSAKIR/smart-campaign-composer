package segment

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzParseValidateCompile feeds arbitrary bytes through the whole untrusted-input path. Invariants: no
// panic; Compile succeeds exactly for validated rules; the SQL text never contains a quote or a semicolon
// (values are parameters, so no literal can appear); placeholders match the argument count.
func FuzzParseValidateCompile(f *testing.F) {
	f.Add([]byte(goldenProposal))
	f.Add([]byte(`{"segment":{"groups":[{"conditions":[{"field":"city","op":"in","value":["Ankara","x'; DROP TABLE users; --"]}]}]},"unsupported_criteria":[],"assumptions":[],"channels_requested":"sms","language":"en","explanation":""}`))
	f.Add([]byte(`{"segment":{"groups":[{"conditions":[{"field":"age","op":"between","value":[18,1e308]}]}]},"unsupported_criteria":[],"assumptions":[],"channels_requested":"both","language":"tr","explanation":""}`))
	f.Add([]byte(`{"segment":{"groups":[]},"unsupported_criteria":[],"assumptions":[],"channels_requested":"both","language":"tr","explanation":""} trailing`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, issues := Parse(raw)
		if len(issues) > 0 {
			return
		}
		vr, errs, _ := ValidateProposal(p)
		sql, args, err := Compile(vr, 1)
		if len(errs) > 0 {
			if err == nil {
				t.Fatal("invalid rule compiled")
			}
			return
		}
		if err != nil {
			t.Fatalf("valid rule did not compile: %v", err)
		}
		if strings.ContainsAny(sql, "';\"") {
			t.Fatalf("suspicious SQL text: %s", sql)
		}
		if n := strings.Count(sql, "$"); n != len(args) {
			t.Fatalf("%d placeholders, %d args: %s", n, len(args), sql)
		}
		for _, loc := range []string{"tr", "en"} {
			for _, line := range Render(vr, loc) {
				if !utf8.ValidString(line) || line == "" {
					t.Fatalf("bad rendering %q", line)
				}
			}
		}
	})
}

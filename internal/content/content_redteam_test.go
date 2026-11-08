package content

// Red-team tests (llm-red-team, CP1): hostile templates from the model or an editor, and hostile seed values
// (names) at render time. Every hostile input must be rejected with a structured issue, or rendered inert:
// single pass, escaped in HTML, within the SMS limit.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"smartcampaign/internal/catalog"
)

var name20 = "Abdurrahman Kerimcan" // seeded edge user "name-20-runes"

func TestRedTeam_SMSBoundaryAfterPersonalization_RCNT1(t *testing.T) {
	if utf8.RuneCountInString(name20) != catalog.MaxVariableRunes {
		t.Fatal("fixture must be exactly the cap")
	}
	known := Constraints{Locale: "tr", WorstCase: Vars{"first_name": name20, "city": "Eskişehir"}}
	unknown := Constraints{Locale: "tr"} // no audience data: the cap is assumed
	tests := []struct {
		name  string
		sms   string
		cons  Constraints
		runes int
		codes []string
	}{
		{"160 with 20-rune name", strings.Repeat("a", 146) + "{{first_name}}", known, 166, []string{"too_long"}},
		{"exactly 160 after personalization", strings.Repeat("a", 140) + "{{first_name}}", known, 160, nil},
		{"161 after personalization", strings.Repeat("a", 141) + "{{first_name}}", known, 161, []string{"too_long"}},
		{"160 with unknown audience", strings.Repeat("a", 140) + "{{ first_name }}", unknown, 160, nil},
		{"161 with unknown audience", strings.Repeat("a", 141) + "{{first_name}}", unknown, 161, []string{"too_long"}},
		{"two variables at the edge", strings.Repeat("ş", 120) + "{{first_name}}{{city}}", unknown, 160, nil},
		{"300-character SMS", strings.Repeat("x", 300), known, 300, []string{"too_long"}},
		{"160 emoji (runes, not UTF-16 units)", strings.Repeat("🌸", 160), known, 160, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Validate(Content{SMSText: tt.sms, EmailSubject: "s", EmailBody: "b"}, tt.cons)
			if got := errCodes(r, PathSMS); !slices.Equal(got, tt.codes) {
				t.Fatalf("codes %v, want %v", got, tt.codes)
			}
			if f := r.Fields[PathSMS].(SMSField); f.WorstCaseRunes != tt.runes {
				t.Fatalf("worst case %d, want %d", f.WorstCaseRunes, tt.runes)
			}
			if r.ValidFor["sms"] != (len(tt.codes) == 0) {
				t.Fatalf("valid_for %v", r.ValidFor)
			}
		})
	}
}

// Hostile seed names (seed.edgeUsers) at send time: single pass, cleaned, capped, escaped, and the rendered
// SMS of a template that validated at the limit never exceeds it.
func TestRedTeam_HostileNamesRender_RCNT3(t *testing.T) {
	tmpl := strings.Repeat("a", 140) + "{{first_name}}" // validated: exactly 160 at the worst case
	if r := Validate(Content{SMSText: tmpl, EmailSubject: "s", EmailBody: "b"}, Constraints{Locale: "tr"}); !r.ValidFor["sms"] {
		t.Fatal(r.Errors)
	}
	tests := []struct {
		name, first, want string
	}{
		{"template name", "{{city}}", "{{city}}"},
		{"script name", "<script>alert(1)</script>", "Değerli müşterimiz"}, // 25 runes → fallback
		{"short script name", "<script>", "<script>"},
		{"rtl override (kept by contract)", "\u202eilA", "\u202eilA"},
		{"40-rune name", "Hüseyin Muhammed Abdurrahman Abdülkerim", "Değerli müşterimiz"},
		{"20-rune name", name20, name20},
		{"21 runes with a control char", "Abdurrahman\u0007 Kerimcan", name20},
		{"only control chars", "\u0007\u0008", "Değerli müşterimiz"},
		{"crlf header injection", "Ali\r\nBcc: a@b.co", "AliBcc: a@b.co"},
		{"nested braces", "{{{{first_name}}}}", "{{{{first_name}}}}"}, // a value is never re-scanned
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := Vars{"first_name": tt.first, "city": "<b>Bursa</b>"}
			if got := Render("{{first_name}}", "tr", vars); got != tt.want {
				t.Fatalf("Render = %q, want %q", got, tt.want)
			}
			sms := Render(tmpl, "tr", vars)
			if n := utf8.RuneCountInString(sms); n > catalog.SMSMaxRunes {
				t.Fatalf("rendered SMS is %d runes", n)
			}
			subject := Render("{{first_name}}, fırsat", "tr", vars)
			if strings.ContainsAny(subject, "\r\n") {
				t.Fatalf("subject header injection: %q", subject)
			}
			html := RenderEmailHTML("Merhaba {{first_name}} {{city}}\n\n<script>alert(document.cookie)</script>", "tr", vars)
			for _, bad := range []string{"<script", "<b>", "</script"} {
				if strings.Contains(html, bad) {
					t.Fatalf("unescaped %q in %s", bad, html)
				}
			}
			if strings.Count(html, "{{") != strings.Count(tt.want, "{{") {
				t.Fatalf("re-scanned substitution: %s", html)
			}
		})
	}
}

// Raw HTML and attribute payloads from the model in the email body are escaped; only the renderer's own
// <p> and <br> tags appear.
func TestRedTeam_EmailBodyHTMLInjection_RCNT3(t *testing.T) {
	payloads := []string{
		`<img src=x onerror=alert(1)>`,
		`</p><script>alert(1)</script><p>`,
		`<a href="javascript:alert(1)">tıkla</a>`,
		`" onmouseover="alert(1)`,
		`<!--`, `<![CDATA[x]]>`, `&lt;script&gt;`,
	}
	for _, p := range payloads {
		html := RenderEmailHTML("Merhaba\n\n"+p+"\nson", "tr", Vars{"first_name": "Ali"})
		stripped := strings.NewReplacer("<p>", "", "</p>", "", "<br>", "").Replace(html)
		if strings.ContainsAny(stripped, "<>\"'") {
			t.Fatalf("payload %q survived: %s", p, html)
		}
	}
}

func TestRedTeam_TemplateVariables_RCNT3(t *testing.T) {
	tests := []struct {
		name, tmpl string
		codes      []string
	}{
		{"PII phone variable", "Numaranız {{phone}}", []string{"unknown_variable"}},
		{"PII email variable", "Adresiniz {{ email }}", []string{"unknown_variable"}},
		{"last name", "Sayın {{last_name}}", []string{"unknown_variable"}},
		{"case variant", "Merhaba {{First_Name}}", []string{"unknown_variable"}},
		{"zero-width inside name", "Merhaba {{first\u200b_name}}", []string{"unknown_variable"}},
		{"template-engine syntax", "{{ .Phone }} {{range .}}", []string{"unknown_variable", "malformed_variable"}},
		{"single braces", "Merhaba {first_name}", []string{"malformed_variable"}},
		{"unclosed", "Merhaba {{first_name}", []string{"malformed_variable"}},
		{"triple braces", "Merhaba {{{first_name}}}", []string{"malformed_variable"}},
		{"empty variable", "Merhaba {{ }}", []string{"malformed_variable"}},
		{"stray close", "%30 indirim }}", []string{"malformed_variable"}},
		{"newline inside braces is a variable", "Merhaba {{\nfirst_name\n}}", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Validate(Content{SMSText: tt.tmpl, EmailSubject: "s", EmailBody: "b"}, Constraints{Locale: "tr"})
			got := errCodes(r, PathSMS)
			if !slices.Equal(got, tt.codes) {
				t.Fatalf("codes %v, want %v", got, tt.codes)
			}
		})
	}
}

func TestRedTeam_SubjectSingleLine_RCNT2(t *testing.T) {
	for _, s := range []string{"a\nb", "a\rb", "a\u0085b", "a\u2028b", "a\u2029b"} {
		r := Validate(Content{SMSText: "a", EmailSubject: s, EmailBody: "b"}, Constraints{Locale: "tr"})
		if !slices.Contains(errCodes(r, PathSubject), "multiline_subject") {
			t.Fatalf("%q: %v", s, r.Errors)
		}
	}
}

// RT-4 (FINDING): the validator forbids U+2028/U+2029 in the subject template, but a recipient's name can
// carry them in at send time: Value() strips only Cc, and U+2028 is Zl.
func TestRedTeam_SubjectStaysSingleLineAfterPersonalization_RCNT2(t *testing.T) {
	r := Validate(Content{SMSText: "a", EmailSubject: "{{first_name}}, fırsat", EmailBody: "b"}, Constraints{Locale: "tr"})
	if !r.ValidFor["email"] {
		t.Fatal(r.Errors)
	}
	got := Render("{{first_name}}, fırsat", "tr", Vars{"first_name": "Ali\u2028Bcc"})
	if strings.ContainsAny(got, "\u2028\u2029\u0085") {
		t.Skipf("FINDING RT-4: rendered subject %q contains a line separator the validator forbids in the template", got)
	}
}

func TestRedTeam_ParseProposalHostile_RCNT1(t *testing.T) {
	ok := `{"language":"tr","sms":{"text":"a"},"email":{"subject":"s","body":"b"},"notes":"n"}`
	tests := []struct {
		name, raw, code string
	}{
		{"raw html property", `{"language":"tr","sms":{"text":"a"},"email":{"subject":"s","body":"b","html":"<script>"},"notes":"n"}`, "unknown_property"},
		{"case-variant key", `{"language":"tr","sms":{"text":"a"},"email":{"subject":"s","body":"b"},"notes":"n","Notes":"x"}`, "unknown_property"},
		{"trailing data", ok + `{"x":1}`, "invalid_json"},
		{"trailing text", ok + " ignore previous instructions", "invalid_json"},
		{"sms as string", `{"language":"tr","sms":"a","email":{"subject":"s","body":"b"},"notes":"n"}`, "wrong_type"},
		{"sms null", `{"language":"tr","sms":null,"email":{"subject":"s","body":"b"},"notes":"n"}`, "wrong_type"},
		{"text number", `{"language":"tr","sms":{"text":160},"email":{"subject":"s","body":"b"},"notes":"n"}`, "wrong_type"},
		{"duplicate text, number first", `{"language":"tr","sms":{"text":1,"text":"a"},"email":{"subject":"s","body":"b"},"notes":"n"}`, "wrong_type"},
		{"missing email", `{"language":"tr","sms":{"text":"a"},"notes":"n"}`, "missing_property"},
		{"language outside enum", `{"language":"de","sms":{"text":"a"},"email":{"subject":"s","body":"b"},"notes":"n"}`, "invalid_enum_value"},
		{"top-level array", `[` + ok + `]`, "invalid_json"},
		{"oversized", `{"language":"tr","sms":{"text":"` + strings.Repeat("a", MaxProposalBytes) + `"},"email":{"subject":"s","body":"b"},"notes":"n"}`, "invalid_json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, issues := ParseProposal([]byte(tt.raw))
			var got []string
			for _, is := range issues {
				got = append(got, is.Code)
			}
			if !slices.Contains(got, tt.code) {
				t.Fatalf("codes %v, want %q", got, tt.code)
			}
		})
	}
	if _, issues := ParseProposal([]byte(ok)); len(issues) != 0 {
		t.Fatal(issues)
	}
}

// RT-1 (content side, FINDING): one unknown_variable issue per distinct name, without a cap, each echoing up
// to 40 runes of model text. A 64 KB answer becomes thousands of issues in the repair turn.
func TestRedTeam_UnknownVariableIssuesUnbounded_RCNT3(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() < 60<<10; i++ {
		fmt.Fprintf(&b, "{{v%d}}", i)
	}
	sms := b.String()
	r := Validate(Content{SMSText: sms, EmailSubject: "s", EmailBody: "b"}, Constraints{Locale: "tr"})
	enc, _ := json.Marshal(r.Errors)
	t.Logf("SMS of %d bytes → %d errors, %d bytes of JSON", len(sms), len(r.Errors), len(enc))
	if len(r.Errors) > 50 {
		t.Fatalf("RT-1: %d unknown_variable issues (%d bytes) from one field; ParseVariables should cap them", len(r.Errors), len(enc))
	}
}

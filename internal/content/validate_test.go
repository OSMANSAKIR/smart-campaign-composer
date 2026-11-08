package content

import (
	"slices"
	"strings"
	"testing"

	"smartcampaign/internal/catalog"
)

func TestParse_Vectors_RCNT3(t *testing.T) {
	var file struct {
		Parse []struct {
			Name      string   `json:"name"`
			Template  string   `json:"template"`
			Variables []string `json:"variables"`
			Errors    []string `json:"errors"`
		} `json:"parse"`
	}
	readVectors(t, "template_vars.json", &file)
	if len(file.Parse) == 0 {
		t.Fatal("no parse vectors")
	}
	for _, v := range file.Parse {
		t.Run(v.Name, func(t *testing.T) {
			vars, issues := ParseVariables(v.Template)
			codes := []string{}
			for _, is := range issues {
				if !slices.Contains(codes, is.Code) {
					codes = append(codes, is.Code)
				}
			}
			if !slices.Equal(vars, v.Variables) {
				t.Fatalf("variables = %q, want %q", vars, v.Variables)
			}
			if !slices.Equal(codes, v.Errors) {
				t.Fatalf("errors = %q, want %q", codes, v.Errors)
			}
		})
	}
}

func errCodes(r Report, path string) []string {
	var out []string
	for _, e := range r.Errors {
		if e.Path == path {
			out = append(out, e.Code)
		}
	}
	return out
}

func warnCodes(r Report, path string) []string {
	var out []string
	for _, e := range r.Warnings {
		if e.Path == path {
			out = append(out, e.Code)
		}
	}
	return out
}

var okEmail = Content{EmailSubject: "Sepetin seni bekliyor", EmailBody: "Merhaba {{first_name}},\n\nSepetindeki ürünlerde %30 indirim seni bekliyor."}

func TestValidate_SMSWorstCase_RCNT1(t *testing.T) {
	name20 := "Abdurrahman Kerimcan" // 20 runes, the longest a name can render
	wc := Vars{"first_name": name20, "city": "Eskişehir"}
	tmpl := "Merhaba {{first_name}}, "
	pad := func(total int) string { // SMS whose worst case is exactly total runes
		return tmpl + strings.Repeat("a", total-len([]rune(tmpl))+len("{{first_name}}")-20)
	}
	tests := []struct {
		name      string
		sms       string
		wc        Vars
		wantRunes int
		valid     bool
	}{
		{"exactly 160 after personalization", pad(160), wc, 160, true},
		{"161 after personalization", pad(161), wc, 161, false},
		{"raw text short but worst case too long", "{{first_name}} {{city}} " + strings.Repeat("ş", 130), wc, 20 + 1 + 9 + 1 + 130, false},
		{"fallback longer than names", "{{first_name}}" + strings.Repeat("a", 142), Vars{"first_name": "Değerli müşterimiz", "city": "Bursa"}, 160, true},
		{"unknown audience assumes 20 runes", "{{first_name}}" + strings.Repeat("a", 141), nil, 161, false},
		{"plain 160 turkish", strings.Repeat("ş", 160), wc, 160, true},
		{"emoji counts as one rune", strings.Repeat("👋", 160), wc, 160, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Validate(Content{SMSText: tt.sms, EmailSubject: okEmail.EmailSubject, EmailBody: okEmail.EmailBody},
				Constraints{Locale: "tr", WorstCase: tt.wc})
			f := r.Fields[PathSMS].(SMSField)
			if f.WorstCaseRunes != tt.wantRunes {
				t.Fatalf("worst_case_runes = %d, want %d", f.WorstCaseRunes, tt.wantRunes)
			}
			if r.ValidFor["sms"] != tt.valid {
				t.Fatalf("valid_for.sms = %v, want %v (errors %+v)", r.ValidFor["sms"], tt.valid, r.Errors)
			}
			if !tt.valid && !slices.Equal(errCodes(r, PathSMS), []string{"too_long"}) {
				t.Fatalf("errors = %+v", r.Errors)
			}
			if !r.ValidFor["email"] {
				t.Fatalf("email must stay valid: %+v", r.Errors)
			}
		})
	}
}

func TestValidate_SMSWorstCasePerChannel_RCNT1(t *testing.T) {
	cons := Constraints{Locale: "tr", WorstCaseByChannel: map[string]Vars{
		"sms":   {"first_name": "Ali", "city": "Bursa"},
		"email": {"first_name": "Abdurrahman Kerimcan", "city": "Bursa"},
	}}
	r := Validate(Content{SMSText: "{{first_name}}" + strings.Repeat("a", 157), EmailSubject: "Hi", EmailBody: "x"}, cons)
	if !r.ValidFor["sms"] {
		t.Fatalf("SMS uses the sms audience: %+v", r.Errors)
	}
	if r.WorstCase["first_name"] != "Abdurrahman Kerimcan" {
		t.Fatalf("report worst_case is the longest across channels: %q", r.WorstCase)
	}
}

func TestValidate_Subject_RCNT2(t *testing.T) {
	wc := Vars{"first_name": "Abdurrahman", "city": "Eskişehir"}
	tests := []struct {
		name    string
		subject string
		codes   []string
		runes   int
	}{
		{"short", "Sepetin seni bekliyor", nil, 21},
		{"exactly 80", strings.Repeat("ş", 80), nil, 80},
		{"81", strings.Repeat("ş", 81), []string{"too_long"}, 81},
		{"80 raw but over after personalization", "{{city}} " + strings.Repeat("a", 71), []string{"too_long"}, 9 + 1 + 71},
		{"newline", "Merhaba\nDünya", []string{"multiline_subject"}, 13},
		{"carriage return", "Merhaba\rDünya", []string{"multiline_subject"}, 13},
		{"line separator", "Merhaba Dünya", []string{"multiline_subject"}, 13},
		{"bell character", "Merhaba\u0007", []string{"control_character"}, 8},
		{"empty", "  ", []string{"required"}, 2},
		{"unknown variable", "Merhaba {{last_name}}", []string{"unknown_variable"}, 21},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Validate(Content{SMSText: "Merhaba", EmailSubject: tt.subject, EmailBody: "x"}, Constraints{Locale: "tr", WorstCase: wc})
			if got := errCodes(r, PathSubject); !slices.Equal(got, tt.codes) {
				t.Fatalf("codes = %v, want %v", got, tt.codes)
			}
			if f := r.Fields[PathSubject].(SubjectField); f.WorstCaseRunes != tt.runes || f.Limit != 80 {
				t.Fatalf("field = %+v, want worst case %d", f, tt.runes)
			}
			if r.ValidFor["email"] != (len(tt.codes) == 0) || !r.ValidFor["sms"] {
				t.Fatalf("valid_for = %v", r.ValidFor)
			}
		})
	}
}

func TestValidate_BodyAndControl_RCNT1(t *testing.T) {
	tests := []struct {
		name  string
		c     Content
		path  string
		codes []string
	}{
		{"empty body", Content{SMSText: "a", EmailSubject: "s", EmailBody: ""}, PathBody, []string{"required"}},
		{"body 5000", Content{SMSText: "a", EmailSubject: "s", EmailBody: strings.Repeat("ş", 5000)}, PathBody, nil},
		{"body 5001", Content{SMSText: "a", EmailSubject: "s", EmailBody: strings.Repeat("ş", 5001)}, PathBody, []string{"too_long"}},
		{"body tab and newline ok", Content{SMSText: "a", EmailSubject: "s", EmailBody: "a\n\tb"}, PathBody, nil},
		{"body NUL", Content{SMSText: "a", EmailSubject: "s", EmailBody: "a\x00b"}, PathBody, []string{"control_character"}},
		{"sms newline ok", Content{SMSText: "a\nb", EmailSubject: "s", EmailBody: "b"}, PathSMS, nil},
		{"sms escape", Content{SMSText: "a\x1b[31mb", EmailSubject: "s", EmailBody: "b"}, PathSMS, []string{"control_character"}},
		{"sms empty", Content{SMSText: "", EmailSubject: "s", EmailBody: "b"}, PathSMS, []string{"required"}},
		{"sms malformed", Content{SMSText: "Hi {first_name}", EmailSubject: "s", EmailBody: "b"}, PathSMS, []string{"malformed_variable"}},
		{"body unknown and malformed", Content{SMSText: "a", EmailSubject: "s", EmailBody: "{{coupon}} {{x"}, PathBody, []string{"unknown_variable", "malformed_variable"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Validate(tt.c, Constraints{Locale: "tr", WorstCase: Vars{"first_name": "Ali", "city": "Bursa"}})
			if got := errCodes(r, tt.path); !slices.Equal(got, tt.codes) {
				t.Fatalf("codes = %v, want %v (all %+v)", got, tt.codes, r.Errors)
			}
			if r.Valid != (len(r.Errors) == 0) {
				t.Fatal("valid must equal no errors")
			}
		})
	}
}

func TestValidate_Warnings_RCNT1(t *testing.T) {
	tests := []struct {
		text  string
		codes []string
	}{
		{"Kampanya için https://example.com adresine git", []string{"contains_url"}},
		{"www.example.com.tr adresinde", []string{"contains_url"}},
		{"Detaylar: ornek.com.tr/kampanya", []string{"contains_url"}},
		{"Bize destek@example.com adresinden yaz", []string{"contains_email"}},
		{"Bizi +90 532 123 45 67 numarasından ara", []string{"contains_phone"}},
		{"Bizi 0532 123 4567 numarasından ara", []string{"contains_phone"}},
		{"Sepetinde %30 indirim! 1.299,90 TL yerine 909,93 TL", nil},
		{"Merhaba {{first_name}}, İstanbul'a özel fırsat.", nil},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			r := Validate(Content{SMSText: tt.text, EmailSubject: "s", EmailBody: "b"}, Constraints{Locale: "tr"})
			if got := warnCodes(r, PathSMS); !slices.Equal(got, tt.codes) {
				t.Fatalf("warnings = %v, want %v", got, tt.codes)
			}
			if !r.ValidFor["sms"] {
				t.Fatalf("warnings never block: %+v", r.Errors)
			}
		})
	}
}

func TestValidate_ReportShape_RCNT1(t *testing.T) {
	r := Validate(Content{SMSText: "Merhaba {{first_name}}, {{city}}", EmailSubject: "{{city}} fırsatı", EmailBody: "Selam {{first_name}}"},
		Constraints{Locale: "tr", WorstCase: Vars{"first_name": "Abdurrahman", "city": "Eskişehir"}})
	if !r.Valid || !r.ValidFor["sms"] || !r.ValidFor["email"] {
		t.Fatalf("report = %+v", r)
	}
	if !slices.Equal(r.Variables, []string{"first_name", "city"}) {
		t.Fatalf("variables = %v", r.Variables)
	}
	sms := r.Fields[PathSMS].(SMSField)
	want := SMSField{Runes: 32, WorstCaseRunes: 30, Limit: catalog.SMSMaxRunes, Encoding: "ucs2", UTF16Units: 30, Segments: 1}
	if sms != want {
		t.Fatalf("sms = %+v, want %+v", sms, want)
	}
	if r.WorstCase["first_name"] != "Abdurrahman" || r.WorstCase["city"] != "Eskişehir" {
		t.Fatalf("worst_case = %v", r.WorstCase)
	}
	if r.Errors == nil || r.Warnings == nil {
		t.Fatal("errors and warnings must encode as [] not null")
	}
}

func TestRenderEmailHTML_EscapesAfterSubstitution_RCNT3(t *testing.T) {
	body := "Merhaba {{first_name}},\n\n<b>%30</b> indirim & daha fazlası\nsatır 2\n\n\n  \nSon paragraf"
	got := RenderEmailHTML(body, "tr", Vars{"first_name": "<script>x</script>"})
	want := "<p>Merhaba &lt;script&gt;x&lt;/script&gt;,</p>\n<p>&lt;b&gt;%30&lt;/b&gt; indirim &amp; daha fazlası<br>\nsatır 2</p>\n<p>Son paragraf</p>"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := RenderEmailHTML("Hi {{first_name}}", "en", Vars{"first_name": "{{city}}", "city": "<x>"}); got != "<p>Hi {{city}}</p>" {
		t.Fatalf("single pass broken: %s", got)
	}
}

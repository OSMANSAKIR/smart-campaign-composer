package content

import (
	"strings"
	"testing"
)

const goodV2 = `{"language":"tr","variants":{
"a":{"label":"Samimi","sms":{"text":"Merhaba {{first_name}}!"},"email":{"subject":"Konu","body":"Gövde"}},
"b":{"label":"Acil","sms":{"text":"{{first_name}}, kaçırma!"},"email":{"subject":"Kaçırma","body":"Gövde 2"}}},"notes":"n"}`

func TestParseProposalV2_B2(t *testing.T) {
	for name, tc := range map[string]struct {
		raw       string
		wantPaths []string // issue paths, in order; nil = valid
		wantCode  string
	}{
		"both variants":    {goodV2, nil, ""},
		"missing b":        {strings.Replace(goodV2, `"b":{"label":"Acil","sms":{"text":"{{first_name}}, kaçırma!"},"email":{"subject":"Kaçırma","body":"Gövde 2"}}`, `"c":1`, 1), []string{"variants.b", "variants.c"}, "missing_property"},
		"extra variant":    {strings.Replace(goodV2, `"notes"`, `"variants2":{},"notes"`, 1), []string{"variants2"}, "unknown_property"},
		"missing label":    {strings.Replace(goodV2, `"label":"Acil",`, ``, 1), []string{"variants.b.label"}, "missing_property"},
		"missing sms text": {strings.Replace(goodV2, `"sms":{"text":"{{first_name}}, kaçırma!"}`, `"sms":{}`, 1), []string{"variants.b.sms.text"}, "missing_property"},
		"extra in email":   {strings.Replace(goodV2, `"body":"Gövde 2"`, `"body":"Gövde 2","cc":"x"`, 1), []string{"variants.b.email.cc"}, "unknown_property"},
		"wrong type":       {strings.Replace(goodV2, `"label":"Samimi"`, `"label":7`, 1), []string{"variants.a.label"}, "wrong_type"},
		"variants string":  {strings.Replace(goodV2, `"variants":{`, `"variants":"x","y":{`, 1), []string{"variants", "y"}, ""},
		"v1 shape":         {`{"language":"tr","sms":{"text":"x"},"email":{"subject":"s","body":"b"},"notes":""}`, []string{"email", "sms", "variants"}, ""},
		"trailing data":    {goodV2 + `{}`, []string{""}, "invalid_json"},
		"bad language":     {strings.Replace(goodV2, `"language":"tr"`, `"language":"de"`, 1), []string{"language"}, "invalid_enum_value"},
		"nul in notes":     {strings.Replace(goodV2, `"notes":"n"`, `"notes":"a\u0000b"`, 1), []string{"notes"}, "control_character"},
	} {
		t.Run(name, func(t *testing.T) {
			p, issues := ParseProposalV2([]byte(tc.raw))
			var got []string
			for _, is := range issues {
				got = append(got, is.Path)
			}
			if len(tc.wantPaths) == 0 {
				if len(issues) != 0 || p.Variants.B.Label != "Acil" || p.Variants.A.Content().SMSText != "Merhaba {{first_name}}!" {
					t.Fatalf("issues %+v, proposal %+v", issues, p)
				}
				return
			}
			if strings.Join(got, ",") != strings.Join(tc.wantPaths, ",") {
				t.Fatalf("paths %v, want %v (%+v)", got, tc.wantPaths, issues)
			}
			if tc.wantCode != "" && issues[0].Code != tc.wantCode {
				t.Fatalf("code %q, want %q", issues[0].Code, tc.wantCode)
			}
		})
	}
}

func TestParseRefine_F3(t *testing.T) {
	good := `{"language":"tr","label":"Samimi","sms":{"text":"x"},"email":{"subject":"s","body":"b"},"notes":""}`
	if p, issues := ParseRefine([]byte(good)); len(issues) != 0 || p.Content().SMSText != "x" || p.Label != "Samimi" {
		t.Fatalf("%+v %+v", p, issues)
	}
	for name, raw := range map[string]string{
		"no label":  strings.Replace(good, `"label":"Samimi",`, ``, 1),
		"v2 shape":  goodV2,
		"extra key": strings.Replace(good, `"notes":""`, `"notes":"","x":1`, 1),
	} {
		if _, issues := ParseRefine([]byte(raw)); len(issues) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateLabel_B2(t *testing.T) {
	for name, tc := range map[string]struct {
		label      string
		allowEmpty bool
		code       string
	}{
		"ok":             {"Friendly reminder", false, ""},
		"40 code points": {strings.Repeat("ş", 40), false, ""},
		"41 code points": {strings.Repeat("ş", 41), false, "label_too_long"},
		"empty":          {"", false, "required"},
		"empty stored":   {"", true, ""},
		"newline":        {"a\nb", false, "control_character"},
		"nul":            {"a\x00b", false, "control_character"},
		"invalid utf8":   {"a\xffb", false, "invalid_value"},
	} {
		t.Run(name, func(t *testing.T) {
			issues := ValidateLabel("variants.a.label", tc.label, tc.allowEmpty)
			switch {
			case tc.code == "" && len(issues) != 0, tc.code != "" && (len(issues) != 1 || issues[0].Code != tc.code || issues[0].Path != "variants.a.label"):
				t.Fatalf("issues %+v, want %q", issues, tc.code)
			}
		})
	}
}

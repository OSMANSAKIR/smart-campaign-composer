package content

import (
	"slices"
	"testing"
)

// RT-8a: a NUL in the notes is a parse issue; RT-8c: a NUL in any content field invalidates both channels.
func TestParseProposal_NotesControlCharacter_RAI1(t *testing.T) {
	raw := `{"language":"tr","sms":{"text":"a"},"email":{"subject":"s","body":"b"},"notes":"a\u0000b"}`
	_, issues := ParseProposal([]byte(raw))
	if len(issues) != 1 || issues[0].Code != "control_character" || issues[0].Path != "notes" {
		t.Fatalf("issues %+v", issues)
	}
	ok := `{"language":"tr","sms":{"text":"a"},"email":{"subject":"s","body":"b"},"notes":"a\nb"}`
	if _, issues := ParseProposal([]byte(ok)); len(issues) != 0 {
		t.Fatalf("newline in notes must be fine: %+v", issues)
	}
}

func TestValidate_NULInAnyFieldInvalidatesEveryChannel_RCNT1(t *testing.T) {
	tests := []struct {
		name string
		c    Content
		path string
	}{
		{"sms", Content{SMSText: "a\x00b", EmailSubject: "s", EmailBody: "b"}, PathSMS},
		{"subject", Content{SMSText: "a", EmailSubject: "s\x00", EmailBody: "b"}, PathSubject},
		{"body", Content{SMSText: "a", EmailSubject: "s", EmailBody: "a\x00b"}, PathBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Validate(tt.c, Constraints{Locale: "tr"})
			if r.Valid || r.ValidFor["sms"] || r.ValidFor["email"] {
				t.Fatalf("valid_for %v, errors %+v", r.ValidFor, r.Errors)
			}
			if !slices.Contains(errCodes(r, tt.path), "control_character") {
				t.Fatalf("errors %+v", r.Errors)
			}
			if got := NULFields(tt.c); !slices.Equal(got, []string{tt.path}) {
				t.Fatalf("NULFields %v", got)
			}
		})
	}
}

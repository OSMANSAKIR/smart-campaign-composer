package content

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// Hash identifies one primary copy (the three texts, not the label). A localized row stores the hash of the
// primary copy it was adapted from; when the primary is edited the hashes differ and the row is stale
// (docs/contracts/api.md §9.9, DECISIONS D-22). Field boundaries are length-prefixed so two different
// copies cannot concatenate to the same input.
func Hash(c Content) string {
	h := sha256.New()
	for _, s := range []string{c.SMSText, c.EmailSubject, c.EmailBody} {
		fmt.Fprintf(h, "%d:%s;", len(s), s)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ParseLocalize strictly decodes a localize_v1 answer. It has the shape of a content_v2 answer (language,
// variants a and b, notes), so the strict decoder is shared.
func ParseLocalize(raw []byte) (ProposalV2, []Issue) { return ParseProposalV2(raw) }

// LocalizeFidelity checks what an adaptation must keep from the primary copy that the length and syntax
// rules of Validate cannot see: exactly the same {{variables}} in every field, nothing that looks like a new
// link, email address or phone number, and not the unchanged source text (the model answered in the wrong
// language). Paths are the content field paths; the caller adds the variant prefix.
func LocalizeFidelity(src, dst Content) []Issue {
	var out []Issue
	fields := []struct{ path, from, to string }{
		{PathSMS, src.SMSText, dst.SMSText}, {PathSubject, src.EmailSubject, dst.EmailSubject}, {PathBody, src.EmailBody, dst.EmailBody},
	}
	for _, f := range fields {
		want, _ := ParseVariables(f.from)
		got, _ := ParseVariables(f.to) // malformed or unknown ones are Validate's job
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(want, got) {
			out = append(out, Issue{Path: f.path, Code: "variable_mismatch",
				Message: fmt.Sprintf("use exactly the variables of the source copy (%s), not %s", varList(want), varList(got))})
		}
		if emailRE.MatchString(f.to) && !emailRE.MatchString(f.from) {
			out = append(out, Issue{Path: f.path, Code: "new_email", Message: "the source copy has no email address; do not add one"})
		}
		if urlRE.MatchString(emailRE.ReplaceAllString(f.to, " ")) && !urlRE.MatchString(emailRE.ReplaceAllString(f.from, " ")) {
			out = append(out, Issue{Path: f.path, Code: "new_url", Message: "the source copy has no link; do not add one"})
		}
		if hasPhone(f.to) && !hasPhone(f.from) {
			out = append(out, Issue{Path: f.path, Code: "new_phone", Message: "the source copy has no phone number; do not add one"})
		}
	}
	if dst == src {
		out = append(out, Issue{Code: "not_adapted", Message: "the copy is identical to the source; write it in the target language"})
	}
	return out
}

func varList(vs []string) string {
	if len(vs) == 0 {
		return "none"
	}
	return "{{" + strings.Join(vs, "}}, {{") + "}}"
}

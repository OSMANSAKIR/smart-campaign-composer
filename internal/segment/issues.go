package segment

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxIssues bounds every issue list that is shown to the model (repair turn), the client or the logs. A
// hostile or broken answer can produce thousands of structural problems; the first few are enough to fix
// it, and an unbounded list would be re-billed on every repair turn (RT-1).
const MaxIssues = 20

// CodeTooManyErrors is the code of the single closing issue that CapIssues appends.
const CodeTooManyErrors = "too_many_errors"

// MaxQuotedRunes is how much model-chosen text an issue path or message may quote (RT-5).
const MaxQuotedRunes = 40

// CapIssues keeps the first max issues and appends one too_many_errors issue ("and K more"). A list that is
// already within the cap, or already capped, is returned unchanged, so applying it twice is harmless.
func CapIssues(issues []Issue, max int) []Issue {
	if len(issues) <= max {
		return issues
	}
	if len(issues) == max+1 && issues[max].Code == CodeTooManyErrors {
		return issues
	}
	out := make([]Issue, 0, max+1)
	out = append(out, issues[:max]...)
	return append(out, TooManyIssue(len(issues)-max))
}

// TooManyIssue is the closing issue for k omitted problems.
func TooManyIssue(k int) Issue {
	return Issue{Code: CodeTooManyErrors, Message: fmt.Sprintf("and %d more problems were not listed; fix the ones above first", k)}
}

// ControlIssue reports the first control character (Unicode Cc) in model free text, other than newline and
// tab, as a control_character issue at path. NUL would make Postgres reject the INSERT (text 22021, jsonb
// 22P05), and the other controls have no place in text shown to a marketer (RT-8a).
func ControlIssue(path, s string) []Issue {
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return []Issue{{Path: path, Code: "control_character", Message: fmt.Sprintf("remove the control character U+%04X", r)}}
		}
	}
	return nil
}

// SafeText makes model-chosen text fit to quote in a path or message: invalid UTF-8, control, format,
// line/paragraph separator, private-use and surrogate characters are dropped, and at most n runes are kept
// (an ellipsis marks the cut). The model must not be able to smuggle an instruction, a line break or a bidi
// override through the text the backend echoes back (RT-5).
func SafeText(s string, n int) string {
	var b strings.Builder
	runes := 0
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs) {
			continue
		}
		if runes == n {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		runes++
	}
	return b.String()
}

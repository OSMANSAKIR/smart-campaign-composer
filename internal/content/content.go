// Package content holds the message templates' rules: character counting, personalization variables,
// rendering, validation and the email HTML rendering (KICKOFF §6.5). Owned by the llm builder.
package content

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"smartcampaign/internal/catalog"
)

// Content is one version of the campaign copy.
type Content struct {
	SMSText      string `json:"sms_text"`
	EmailSubject string `json:"email_subject"`
	EmailBody    string `json:"email_body"`
}

// Vars are personalization values for one recipient, keyed by variable name.
type Vars map[string]string

// Fallbacks replace empty or over-long values, per locale (need.md N-ORC-005).
var Fallbacks = map[string]Vars{
	"tr": {"first_name": "Değerli müşterimiz", "city": "Türkiye"},
	"en": {"first_name": "Valued customer", "city": "Türkiye"},
}

// variableRE matches a well-formed variable token; the name is validated separately against the allowlist.
var variableRE = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// Render substitutes variables in one pass: replaced values are never scanned again, so a user named
// "{{city}}" stays literal. Unknown or malformed tokens are left as they are; Validate rejects them earlier.
func Render(tmpl, locale string, vars Vars) string {
	return variableRE.ReplaceAllStringFunc(tmpl, func(tok string) string {
		name := variableRE.FindStringSubmatch(tok)[1]
		if !isAllowed(name) {
			return tok
		}
		return Value(name, locale, vars)
	})
}

// Value returns the cleaned value of one variable, or the locale fallback when it is empty or longer than
// catalog.MaxVariableRunes after cleaning. Cleaning strips what must not travel through a recipient-chosen
// value into a message (RT-4, RT-9): control characters (Cc), the line and paragraph separators U+2028/2029
// (the validator forbids them in a subject template, so a name must not reintroduce them) and the bidi
// controls U+202A–U+202E and U+2066–U+2069 (a name like "<RLO>ilA" would reorder the text around it).
func Value(name, locale string, vars Vars) string {
	v := strings.TrimSpace(stripUnsafe(vars[name]))
	if v == "" || utf8.RuneCountInString(v) > catalog.MaxVariableRunes {
		fb, ok := Fallbacks[locale]
		if !ok {
			fb = Fallbacks["tr"]
		}
		return fb[name]
	}
	return v
}

func stripUnsafe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' ||
			(r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
			return -1
		}
		return r
	}, s)
}

func isAllowed(name string) bool { return slices.Contains(catalog.Variables, name) }

// Counts describes a text for the UI and the validator (display-only encoding info, need.md N-PLAN-005).
type Counts struct {
	Runes       int    `json:"runes"`
	UTF16Units  int    `json:"utf16_units"`
	Encoding    string `json:"encoding"` // gsm7 | ucs2
	GSM7Septets int    `json:"gsm7_septets,omitempty"`
	Segments    int    `json:"segments"`
}

// GSM 03.38 basic table (Unicode GSM0338 mapping, escape excluded) and extension table (2 septets each).
const (
	gsmBasic     = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
	gsmExtension = "\f^{}\\[~]|€"
)

// Count measures s as docs/contracts/testvectors/text_length.json defines.
func Count(s string) Counts {
	c := Counts{Runes: utf8.RuneCountInString(s), Encoding: "gsm7"}
	for _, r := range s {
		c.UTF16Units += utf16Len(r)
		switch {
		case strings.ContainsRune(gsmBasic, r):
			c.GSM7Septets++
		case strings.ContainsRune(gsmExtension, r):
			c.GSM7Septets += 2
		default:
			c.Encoding = "ucs2"
		}
	}
	switch {
	case c.Runes == 0:
		c.Segments = 0
	case c.Encoding == "gsm7":
		c.Segments = segments(c.GSM7Septets, 160, 153)
	default:
		c.GSM7Septets = 0
		c.Segments = segments(c.UTF16Units, 70, 67)
	}
	return c
}

func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

func segments(n, single, part int) int {
	if n <= single {
		return 1
	}
	return (n + part - 1) / part
}

// Report is the validation report (docs/contracts/api.md §2).
type Report struct {
	Valid     bool              `json:"valid"`
	ValidFor  map[string]bool   `json:"valid_for"`
	Fields    map[string]any    `json:"fields"`
	WorstCase map[string]string `json:"worst_case"`
	Variables []string          `json:"variables"`
	Errors    []Issue           `json:"errors"`
	Warnings  []Issue           `json:"warnings"`
}

// Issue is a structured validation error or warning.
type Issue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Constraints are the inputs Validate needs beyond the text.
type Constraints struct {
	Locale string // tr|en: selects the fallback values (need.md N-ORC-005)
	// WorstCase holds the longest rendered value per variable for the audience (see WorstCaseVars).
	WorstCase Vars
	// WorstCaseByChannel overrides WorstCase per channel ("sms", "email"), because the worst case is taken
	// over the users consented to that channel.
	WorstCaseByChannel map[string]Vars
}

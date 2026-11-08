package content

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"smartcampaign/internal/catalog"
)

// Field paths in the report (docs/contracts/api.md §2).
const (
	PathSMS     = "sms_text"
	PathSubject = "email_subject"
	PathBody    = "email_body"
)

// SMSField, SubjectField and BodyField are the per-field entries of Report.Fields.
type SMSField struct {
	Runes          int    `json:"runes"`
	WorstCaseRunes int    `json:"worst_case_runes"`
	Limit          int    `json:"limit"`
	Encoding       string `json:"encoding"`
	UTF16Units     int    `json:"utf16_units"`
	Segments       int    `json:"segments"`
}

type SubjectField struct {
	Runes          int `json:"runes"`
	WorstCaseRunes int `json:"worst_case_runes"`
	Limit          int `json:"limit"`
}

type BodyField struct {
	Runes int `json:"runes"`
	Limit int `json:"limit"`
}

var (
	emailRE = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	urlRE   = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+|\b[a-z0-9\-]+(?:\.[a-z0-9\-]+)*\.(?:com|net|org|io|co|app|shop|store|info|biz|tr|me|ly|link)\b(?:/\S*)?`)
	phoneRE = regexp.MustCompile(`\+?\(?\d[\d\s().\-]{7,}\d`)
)

// Validate checks content against the channel rules (KICKOFF §6.5). Lengths are Unicode code points after
// worst-case personalization (need.md N-PLAN-005, N-ORC-005). valid_for tells per channel whether the copy
// may be approved; warnings never block.
func Validate(c Content, cons Constraints) Report {
	locale := cons.Locale
	if _, ok := Fallbacks[locale]; !ok {
		locale = "tr"
	}
	r := Report{
		ValidFor:  map[string]bool{"sms": true, "email": true},
		Fields:    map[string]any{},
		WorstCase: map[string]string{},
		Variables: []string{},
		Errors:    []Issue{},
		Warnings:  []Issue{},
	}
	add := func(path, code, msg string) {
		r.Errors = append(r.Errors, Issue{Path: path, Code: code, Message: msg})
		if path == PathSMS {
			r.ValidFor["sms"] = false
		} else {
			r.ValidFor["email"] = false
		}
	}
	warn := func(path, code, msg string) {
		r.Warnings = append(r.Warnings, Issue{Path: path, Code: code, Message: msg})
	}

	smsVars, emailVars := cons.worstCase("sms"), cons.worstCase("email")

	// SMS
	smsWC := Render(c.SMSText, locale, smsVars)
	counts := Count(smsWC)
	r.Fields[PathSMS] = SMSField{Runes: utf8.RuneCountInString(c.SMSText), WorstCaseRunes: counts.Runes,
		Limit: catalog.SMSMaxRunes, Encoding: counts.Encoding, UTF16Units: counts.UTF16Units, Segments: counts.Segments}
	if strings.TrimSpace(c.SMSText) == "" {
		add(PathSMS, "required", "SMS text is required")
	} else if counts.Runes > catalog.SMSMaxRunes {
		add(PathSMS, "too_long", fmt.Sprintf("SMS is %d characters after worst-case personalization; the limit is %d", counts.Runes, catalog.SMSMaxRunes))
	}
	checkControl(PathSMS, c.SMSText, "\n", add)

	// Email subject
	subjWC := Render(c.EmailSubject, locale, emailVars)
	subjRunes := utf8.RuneCountInString(subjWC)
	r.Fields[PathSubject] = SubjectField{Runes: utf8.RuneCountInString(c.EmailSubject), WorstCaseRunes: subjRunes, Limit: catalog.EmailSubjectMaxRunes}
	if strings.TrimSpace(c.EmailSubject) == "" {
		add(PathSubject, "required", "Email subject is required")
	} else if subjRunes > catalog.EmailSubjectMaxRunes {
		add(PathSubject, "too_long", fmt.Sprintf("Subject is %d characters after worst-case personalization; the limit is %d", subjRunes, catalog.EmailSubjectMaxRunes))
	}
	if strings.ContainsAny(c.EmailSubject, "\n\r\u0085  ") {
		add(PathSubject, "multiline_subject", "Subject must be a single line")
	}
	checkControl(PathSubject, c.EmailSubject, "\n\r\u0085", add)

	// Email body
	bodyRunes := utf8.RuneCountInString(c.EmailBody)
	r.Fields[PathBody] = BodyField{Runes: bodyRunes, Limit: catalog.EmailBodyMaxRunes}
	if strings.TrimSpace(c.EmailBody) == "" {
		add(PathBody, "required", "Email body is required")
	} else if bodyRunes > catalog.EmailBodyMaxRunes {
		add(PathBody, "too_long", fmt.Sprintf("Body is %d characters; the limit is %d", bodyRunes, catalog.EmailBodyMaxRunes))
	}
	checkControl(PathBody, c.EmailBody, "\n\t", add)

	// Variables and warnings, per field
	for _, f := range []struct{ path, text string }{{PathSMS, c.SMSText}, {PathSubject, c.EmailSubject}, {PathBody, c.EmailBody}} {
		vars, issues := ParseVariables(f.text)
		for _, v := range vars {
			if !slices.Contains(r.Variables, v) {
				r.Variables = append(r.Variables, v)
			}
		}
		for _, is := range issues {
			add(f.path, is.Code, is.Message)
		}
		emails := emailRE.FindAllString(f.text, -1)
		if len(emails) > 0 {
			warn(f.path, "contains_email", "The copy contains an email address; check that it is intended")
		}
		withoutEmails := emailRE.ReplaceAllString(f.text, " ")
		if urlRE.MatchString(withoutEmails) {
			warn(f.path, "contains_url", "The copy contains a link; check that it is intended and safe")
		}
		if hasPhone(withoutEmails) {
			warn(f.path, "contains_phone", "The copy contains a phone number; check that it is intended")
		}
	}

	for _, name := range catalog.Variables {
		a, b := Value(name, locale, smsVars), Value(name, locale, emailVars)
		if utf8.RuneCountInString(b) > utf8.RuneCountInString(a) {
			a = b
		}
		r.WorstCase[name] = a
	}
	r.Valid = len(r.Errors) == 0
	return r
}

// worstCase returns the worst-case values for a channel, falling back to the shared map. A variable with no
// known value gets the longest renderable placeholder, so missing data never makes the check optimistic.
func (c Constraints) worstCase(channel string) Vars {
	src, ok := c.WorstCaseByChannel[channel]
	if !ok {
		src = c.WorstCase
	}
	out := Vars{}
	for _, name := range catalog.Variables {
		if v, ok := src[name]; ok {
			out[name] = v
		} else {
			out[name] = unknownWorstCase
		}
	}
	return out
}

// checkControl reports Unicode control characters (Cc) other than the allowed ones, once per field.
func checkControl(path, s, allowed string, add func(path, code, msg string)) {
	for _, r := range s {
		if unicode.IsControl(r) && !strings.ContainsRune(allowed, r) {
			add(path, "control_character", fmt.Sprintf("Remove the control character U+%04X", r))
			return
		}
	}
}

func hasPhone(s string) bool {
	for _, m := range phoneRE.FindAllString(s, -1) {
		digits := 0
		for _, r := range m {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if digits >= 10 { // Turkish mobile numbers have 10 digits without the leading 0
			return true
		}
	}
	return false
}

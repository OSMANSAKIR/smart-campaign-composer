package delivery

import (
	"html"
	"regexp"
	"strings"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/content"
)

// Rendered is one recipient's message, ready to send.
type Rendered struct {
	To      string
	Text    string // sms text, or the email plain-text part
	Subject string // email only
	HTML    string // email only
}

// Template is one approved content version.
type Template struct {
	SMSText      string
	EmailSubject string
	EmailBody    string
}

// Recipient holds what personalization and addressing need.
type Recipient struct {
	Email, Phone    string
	FirstName, City string
	Locale          string // the campaign language, else the user's locale; picks the fallback text
}

func (r Recipient) vars() content.Vars {
	return content.Vars{"first_name": r.FirstName, "city": r.City}
}

// RenderMessage renders the template for one recipient (KICKOFF §7.6). tooLong is true for an SMS over the
// limit; the dispatcher then fails the message permanently instead of sending it.
func RenderMessage(ch Channel, t Template, r Recipient) (out Rendered, tooLong bool) {
	switch ch {
	case SMS:
		out = Rendered{To: r.Phone, Text: content.Render(t.SMSText, r.Locale, r.vars())}
		return out, content.Count(out.Text).Runes > catalog.SMSMaxRunes
	default:
		text := content.Render(t.EmailBody, r.Locale, r.vars())
		return Rendered{
			To:      r.Email,
			Subject: content.Render(t.EmailSubject, r.Locale, r.vars()),
			Text:    text,
			HTML:    emailHTML(text),
		}, false
	}
}

var paragraphBreak = regexp.MustCompile(`\n{2,}`)

// emailHTML turns the rendered plain-text body into HTML: escape first, then paragraphs and line breaks, so
// nothing a user's name or the model wrote can inject markup. A private helper until the content package
// ships RenderEmailHTML (see open_issues).
func emailHTML(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	b.WriteString("<!doctype html><html><body>")
	for _, p := range paragraphBreak.Split(strings.TrimSpace(text), -1) {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(html.EscapeString(p), "\n", "<br>"))
		b.WriteString("</p>")
	}
	b.WriteString("</body></html>")
	return b.String()
}

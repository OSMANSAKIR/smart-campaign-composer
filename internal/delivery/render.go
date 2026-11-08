package delivery

import (
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
			// Same escaping and paragraph rules that the validator assumed (need.md N-DEL-005).
			HTML: "<!doctype html><html><body>" + content.RenderEmailHTML(t.EmailBody, r.Locale, r.vars()) + "</body></html>",
		}, false
	}
}

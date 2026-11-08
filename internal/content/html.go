package content

import (
	"html"
	"regexp"
	"strings"
)

var paragraphBreak = regexp.MustCompile(`\n[ \t]*\n+`)

// RenderEmailHTML personalizes a plain-text body for one recipient and turns it into an HTML fragment:
// substitute (single pass), then escape everything, then add structure (blank line → new <p>, newline →
// <br>). Escaping happens after substitution, so neither the model's text nor a user's name can inject
// markup; raw HTML from the model is never used (KICKOFF §6.5).
func RenderEmailHTML(body, locale string, vars Vars) string {
	text := strings.ReplaceAll(Render(body, locale, vars), "\r\n", "\n")
	var b strings.Builder
	for _, para := range paragraphBreak.Split(text, -1) {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(html.EscapeString(para), "\n", "<br>\n"))
		b.WriteString("</p>")
	}
	return b.String()
}

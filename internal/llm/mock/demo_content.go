package mock

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"

	"smartcampaign/internal/content"
)

// DemoContent writes two short variants in the requested language that pass validation for a 20-rune name:
// a is the friendly reminder, b the urgency/benefit angle (never an invented deadline).
func DemoContent(brief, lang string) content.ProposalV2 {
	var c content.ProposalV2
	c.Language = lang
	discount := ""
	if m := discountRE.FindStringSubmatch(brief); m != nil {
		discount = m[1] + m[2]
	}
	cart := strings.Contains(fold(brief), "sepet") || strings.Contains(strings.ToLower(brief), "cart")
	a, b := &c.Variants.A, &c.Variants.B
	if lang == "en" {
		hook := "we picked something special for you"
		if cart {
			hook = "the items in your cart are waiting for you"
		}
		offer := "Take a look today."
		if discount != "" {
			offer = "Enjoy " + discount + "% off, just for you, today."
		}
		a.Label = "Friendly reminder"
		a.SMS.Text = "Hi {{first_name}}, " + hook + "! " + offer
		a.Email.Subject = "{{first_name}}, " + subjectOffer(discount, "en")
		a.Email.Body = "Hi {{first_name}},\n\n" + strings.ToUpper(hook[:1]) + hook[1:] + ". " + offer + "\n\nHappy shopping!"

		urgent := "Take a look now, don't miss out."
		if discount != "" {
			urgent = "Get " + discount + "% off and finish now, don't miss it."
		}
		b.Label = "Urgency / benefit"
		b.SMS.Text = "{{first_name}}, don't miss it! " + urgent
		b.Email.Subject = "Don't miss out: " + subjectOffer(discount, "en")
		b.Email.Body = "Hi {{first_name}},\n\n" + urgent + "\n\nIt only takes a moment to come back."
		c.Notes = "Demo copy (mock LLM). Set ANTHROPIC_API_KEY for real generation."
		return c
	}
	hook := "sana özel fırsatlar seni bekliyor"
	if cart {
		hook = "sepetindeki ürünler seni bekliyor"
	}
	offer := "Bugün göz at."
	if discount != "" {
		offer = "Sana özel %" + discount + " indirimle bugün tamamla."
	}
	a.Label = "Samimi hatırlatma"
	a.SMS.Text = "Merhaba {{first_name}}, " + hook + "! " + offer
	a.Email.Subject = "{{first_name}}, " + subjectOffer(discount, "tr")
	a.Email.Body = "Merhaba {{first_name}},\n\n" + strings.ToUpper(hook[:1]) + hook[1:] + ". " + offer + "\n\nKeyifli alışverişler!"

	urgent := "Şimdi göz at, kaçırma."
	if discount != "" {
		urgent = "%" + discount + " indirimden yararlan, hemen tamamla ve kaçırma."
	}
	b.Label = "Acil fayda"
	b.SMS.Text = "{{first_name}}, fırsatı kaçırma! " + urgent
	b.Email.Subject = "Kaçırma: " + subjectOffer(discount, "tr")
	b.Email.Body = "Merhaba {{first_name}},\n\n" + urgent + "\n\nDönmen sadece bir dakikanı alır."
	c.Notes = "Demo metni (mock LLM). Gerçek üretim için ANTHROPIC_API_KEY ayarlayın."
	return c
}

var (
	currentVariantRE = regexp.MustCompile(`(?s)<current_variant>(.*?)</current_variant>`)
	instructionRE    = regexp.MustCompile(`(?s)<instruction>(.*?)</instruction>`)
	sentenceEndRE    = regexp.MustCompile(`[!.?] `)
)

// DemoRefine rewrites the variant of a refine request with a few keyword rules (shorter, longer, formal,
// urgent; anything else adds a closing line). It never repeats the instruction text, so a hostile
// instruction cannot be reflected into the copy.
func DemoRefine(userMessage, lang string) (content.RefineProposal, bool) {
	var p content.RefineProposal
	m := currentVariantRE.FindStringSubmatch(userMessage)
	if m == nil {
		return p, false
	}
	var cur struct {
		Label        string `json:"label"`
		SMSText      string `json:"sms_text"`
		EmailSubject string `json:"email_subject"`
		EmailBody    string `json:"email_body"`
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &cur); err != nil {
		return p, false
	}
	instr := ""
	if im := instructionRE.FindStringSubmatch(userMessage); im != nil {
		instr = fold(html.UnescapeString(im[1]))
	}
	tr := lang != "en"
	say := func(trText, enText string) string {
		if tr {
			return trText
		}
		return enText
	}
	p.Language = lang
	p.Label = cur.Label
	sms, subject, body := cur.SMSText, cur.EmailSubject, cur.EmailBody
	switch {
	case containsAny(instr, "kisa", "short", "brief"):
		// Keep the first sentence of the SMS and the first and last paragraph of the email.
		if loc := sentenceEndRE.FindStringIndex(sms); loc != nil {
			sms = strings.TrimSpace(sms[:loc[0]+1])
		} else if utf8.RuneCountInString(sms) > 60 {
			sms = string([]rune(sms)[:60])
		}
		if paras := strings.Split(body, "\n\n"); len(paras) > 2 {
			body = paras[0] + "\n\n" + paras[len(paras)-1]
		}
		p.Notes = say("Demo: SMS ve e-posta kısaltıldı.", "Demo: the SMS and the email were shortened.")
	case containsAny(instr, "uzun", "detay", "long", "detail"):
		body += say("\n\nSorun olursa bize yazabilirsin.", "\n\nWrite to us if you have any questions.")
		p.Notes = say("Demo: e-postaya bir cümle eklendi.", "Demo: one sentence was added to the email.")
	case containsAny(instr, "resmi", "formal"):
		sms = strings.Replace(sms, "Merhaba", "Sayın", 1)
		sms = strings.Replace(sms, "Hi ", "Dear ", 1)
		body = strings.Replace(body, "Merhaba", "Sayın", 1)
		body = strings.Replace(body, "Hi ", "Dear ", 1)
		p.Notes = say("Demo: hitap resmileştirildi.", "Demo: the greeting was made formal.")
	case containsAny(instr, "acil", "urgent", "hemen"):
		if prefix := say("Son fırsat! ", "Last chance! "); utf8.RuneCountInString(sms)+utf8.RuneCountInString(prefix) <= 120 {
			sms = prefix + sms
		}
		p.Notes = say("Demo: SMS'e aciliyet eklendi.", "Demo: urgency was added to the SMS.")
	default:
		body += say("\n\nTeşekkürler!", "\n\nThank you!")
		p.Notes = say("Demo: e-postaya bir kapanış cümlesi eklendi.", "Demo: a closing line was added to the email.")
	}
	p.SMS.Text, p.Email.Subject, p.Email.Body = sms, subject, body
	return p, true
}

var (
	sourceVariantsRE = regexp.MustCompile(`(?s)<source_variants>(.*?)</source_variants>`)
	targetLanguageRE = regexp.MustCompile(`(?m)^Target language: (tr|en)$`)
	variableTokenRE  = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)
)

// DemoLocalize adapts the variants of a localize request to the target language. It never repeats the source
// text (an instruction hidden in the copy cannot be reflected into the adaptation): the adaptation is the
// demo copy of the target language (DemoContent), with the source's variables put back field by field, so a
// hand-edited source keeps the variables it had. It returns false when the message has no source variants.
func DemoLocalize(userMessage string) (content.ProposalV2, bool) {
	var p content.ProposalV2
	m := sourceVariantsRE.FindStringSubmatch(userMessage)
	if m == nil {
		return p, false
	}
	type variant struct {
		SMSText      string `json:"sms_text"`
		EmailSubject string `json:"email_subject"`
		EmailBody    string `json:"email_body"`
	}
	var src map[string]variant
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &src); err != nil || len(src) == 0 {
		return p, false
	}
	// Only the header after the brief counts (the brief is user text); the brief is escaped, so the first
	// "</brief>" is the real one.
	target := "en"
	_, after, _ := strings.Cut(userMessage, "</brief>")
	if tm := targetLanguageRE.FindStringSubmatch(after); tm != nil {
		target = tm[1]
	}
	brief := ""
	if bm := briefRE.FindStringSubmatch(userMessage); bm != nil {
		brief = html.UnescapeString(bm[1])
	}
	p = DemoContent(brief, target)
	for _, key := range content.VariantKeys {
		s, ok := src[key]
		if !ok {
			continue
		}
		v := &p.Variants.A
		if key == "b" {
			v = &p.Variants.B
		}
		v.SMS.Text = withVariables(v.SMS.Text, s.SMSText, target, false)
		v.Email.Subject = withVariables(v.Email.Subject, s.EmailSubject, target, false)
		v.Email.Body = withVariables(v.Email.Body, s.EmailBody, target, true)
	}
	if target == "en" {
		p.Notes = "Demo adaptation (mock LLM): the offer and the variables were kept."
	} else {
		p.Notes = "Demo uyarlama (mock LLM): teklif ve değişkenler korundu."
	}
	return p, true
}

// withVariables makes the variables of text equal the variables of source: tokens the source does not have
// are dropped, and a source variable that text lacks is added ({{first_name}} as a greeting, anything else
// in brackets at the end), so the adaptation keeps exactly the source's variables.
func withVariables(text, source, lang string, multiline bool) string {
	want := map[string]bool{}
	for _, m := range variableTokenRE.FindAllStringSubmatch(source, -1) {
		want[m[1]] = true
	}
	have := map[string]bool{}
	text = variableTokenRE.ReplaceAllStringFunc(text, func(tok string) string {
		name := variableTokenRE.FindStringSubmatch(tok)[1]
		if !want[name] || have[name] {
			return ""
		}
		have[name] = true
		return tok
	})
	text = strings.NewReplacer(" ,", ",", "  ", " ").Replace(text)
	if want["first_name"] && !have["first_name"] {
		greeting := "Hi"
		if lang == "tr" {
			greeting = "Merhaba"
		}
		if multiline {
			text = greeting + " {{first_name}},\n\n" + text
		} else {
			text = "{{first_name}}, " + text
		}
	}
	if want["city"] && !have["city"] {
		text += " ({{city}})"
	}
	return strings.TrimSpace(text)
}

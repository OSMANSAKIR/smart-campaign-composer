package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/clock"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/segment"
)

// DemoModel is reported as the model name in demo mode.
const DemoModel = "mock-demo-v1"

// Demo is the deterministic keyword/catalog heuristic used when no key is set. It answers through the same
// Service pipeline as a real model, so everything it returns is validated like model output.
type Demo struct {
	clk       clock.Clock
	delay     time.Duration // between streamed chunks, so the streaming UI is visible
	chunkSize int           // runes per streamed chunk
}

// NewDemo returns the demo adapter with small streaming delays.
func NewDemo() *Demo { return &Demo{clk: clock.Real{}, delay: 25 * time.Millisecond, chunkSize: 6} }

// NewDemoWith returns a demo adapter with an injected clock and delay (tests use a zero delay).
func NewDemoWith(clk clock.Clock, delay time.Duration, chunkSize int) *Demo {
	return &Demo{clk: clk, delay: delay, chunkSize: max(chunkSize, 1)}
}

func (*Demo) Name() string  { return "mock" }
func (*Demo) Model() string { return DemoModel }

var (
	briefRE    = regexp.MustCompile(`(?s)<brief>(.*?)</brief>`)
	languageRE = regexp.MustCompile(`(?m)^Language: (tr|en)$`)
)

// Generate answers a segment or content request. Repairs get the same deterministic answer.
func (d *Demo) Generate(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	start := d.clk.Now()
	if len(req.Messages) == 0 {
		return llm.Response{}, fmt.Errorf("mock demo: empty request")
	}
	first := req.Messages[0].Text
	brief := ""
	if m := briefRE.FindStringSubmatch(first); m != nil {
		brief = html.UnescapeString(m[1])
	}
	var out any
	if req.Task == llm.PurposeLocalize {
		lp, ok := DemoLocalize(first)
		if !ok {
			return llm.Response{}, fmt.Errorf("mock demo: localize request without source variants")
		}
		out = lp
	} else if req.Task == llm.PurposeContent || req.Task == llm.PurposeRefine {
		lang := "tr"
		// Only the header after the brief counts: the brief is user text and may contain a "Language: en"
		// line of its own (RT-6). The brief is escaped, so the first "</brief>" is the real one.
		if _, after, ok := strings.Cut(first, "</brief>"); ok {
			if m := languageRE.FindStringSubmatch(after); m != nil {
				lang = m[1]
			}
		}
		if req.Task == llm.PurposeRefine {
			// The current variant and the instruction come after the language header, so a brief cannot
			// forge them: both are escaped and the first closing tags are the real ones.
			rp, ok := DemoRefine(first, lang)
			if !ok {
				return llm.Response{}, fmt.Errorf("mock demo: refine request without a current variant")
			}
			out = rp
		} else {
			out = DemoContent(brief, lang)
		}
	} else {
		out = DemoSegment(brief)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return llm.Response{}, err
	}
	if onDelta != nil {
		runes := []rune(string(b))
		for i := 0; i < len(runes); i += d.chunkSize {
			if d.delay > 0 && i > 0 {
				select {
				case <-ctx.Done():
					return llm.Response{}, ctx.Err()
				case <-d.clk.After(d.delay):
				}
			}
			if err := ctx.Err(); err != nil {
				return llm.Response{}, err
			}
			onDelta(llm.Delta{JSONFragment: string(runes[i:min(i+d.chunkSize, len(runes))])})
		}
	}
	return llm.Response{JSON: b, StopReason: llm.StopEndTurn, Model: DemoModel, Latency: d.clk.Now().Sub(start),
		Usage: llm.Usage{InputTokens: len(first) / 4, OutputTokens: len(b) / 4}}, nil
}

// fold lowercases with Turkish rules and strips Turkish diacritics, so "İSTANBUL'daki", "istanbul" and
// "Istanbul" all match.
func fold(s string) string {
	s = strings.ToLowerSpecial(unicode.TurkishCase, s)
	return strings.NewReplacer("ç", "c", "ğ", "g", "ı", "i", "ö", "o", "ş", "s", "ü", "u", "â", "a", "î", "i", "û", "u").Replace(s)
}

var (
	daysRE       = regexp.MustCompile(`(?:son|last|past)\s+(\d{1,4})\s*(?:gun|day)`)
	daysAltRE    = regexp.MustCompile(`(\d{1,4})\s*(?:gun|days?)\b`)
	underAgeRE   = regexp.MustCompile(`(\d{2})\s*yas\s*(?:alti|altindaki)|under\s+(\d{2})|younger than\s+(\d{2})`)
	overAgeRE    = regexp.MustCompile(`(\d{2})\s*yas\s*(?:ustu|uzeri|ustundeki)|over\s+(\d{2})|older than\s+(\d{2})`)
	spentRE      = regexp.MustCompile(`spent (?:more than|over) (\d{1,8})|(\d{1,8})\s*(?:tl|try)\s*(?:uzeri|ustu)`)
	purchasesRE  = regexp.MustCompile(`at least (\d{1,5}) purchases|en az (\d{1,5}) (?:alisveris|siparis)`)
	ageRangeRE   = regexp.MustCompile(`(\d{2})\s*[-–]\s*(\d{2})\s*(?:yas|years?|age)`)
	turkishChars = regexp.MustCompile(`[çğıöşüÇĞİÖŞÜ]`)
	turkishWords = regexp.MustCompile(`\b(?:ve|icin|olan|gonder|kullanici|kullanicilara|musteri|son|indirim|herkese)\b`)
)

var tierWords = map[string][]string{"bronze": {"bronz", "bronze"}, "silver": {"gumus", "silver"}, "gold": {"altin", "gold"}, "platinum": {"platin", "platinum"}}

var categoryWords = map[string][]string{
	"electronics": {"elektronik", "electronics"}, "fashion": {"moda", "giyim", "fashion"}, "home": {"ev urun", "dekorasyon", "home"},
	"beauty": {"kozmetik", "guzellik", "beauty"}, "sports": {"spor ", "sports"}, "books": {"kitap", "books"},
	"grocery": {"market", "grocery"}, "toys": {"oyuncak", "toys"},
}

var unsupportedWords = []string{"futbol", "football", "basketbol", "basketball", "hava durumu", "weather", "cinsiyet", "gender",
	"kadin", "women", "erkek", " men ", "gelir", "income", "iphone", "android", "instagram", "seven", " love", "ilgi", "interest", "rain", "yagmur"}

// DemoSegment extracts an audience from a brief with keywords and the catalog. The official brief yields
// the golden rule of segment-dsl.md §7.
func DemoSegment(brief string) segment.Proposal {
	f := " " + fold(brief) + " "
	noCities := brief // city names carry Turkish letters in English briefs too
	for _, c := range catalog.Cities {
		noCities = strings.ReplaceAll(noCities, c, "")
	}
	tr := turkishChars.MatchString(noCities) || turkishWords.MatchString(f)
	p := segment.Proposal{Segment: segment.Rule{Groups: []segment.Group{}}, UnsupportedCriteria: []string{}, Assumptions: []string{},
		ChannelsRequested: "both", Language: "en"}
	if tr {
		p.Language = "tr"
	}
	say := func(trText, enText string) string {
		if tr {
			return trText
		}
		return enText
	}
	add := func(field, op string, value any) {
		b, _ := json.Marshal(value)
		p.Segment.Groups = append(p.Segment.Groups, segment.Group{Conditions: []segment.Condition{{Field: field, Op: op, Value: b}}})
	}

	var cities []string
	for _, c := range catalog.Cities {
		if strings.Contains(f, fold(c)) {
			cities = append(cities, c)
		}
	}
	switch len(cities) {
	case 0:
	case 1:
		add("city", "eq", cities[0])
	default:
		add("city", "in", cities)
	}

	days := 0
	if m := daysRE.FindStringSubmatch(f); m != nil {
		days, _ = strconv.Atoi(m[1])
	} else if m := daysAltRE.FindStringSubmatch(f); m != nil {
		days, _ = strconv.Atoi(m[1])
	}
	days = min(max(days, 0), catalog.MaxDays)

	cart := strings.Contains(f, "sepet") || strings.Contains(f, "cart")
	noPurchase := containsAny(f, "alisveris yapmayan", "alisveris yapmamis", "satin almayan", "almayan", "haven't bought", "have not bought",
		"didn't buy", "did not buy", "no purchase", "not purchased", "without buying", "abandon", "terk")
	switch {
	case !cart && containsAny(f, "hic alisveris yapmamis", "hic alisveris yapmayan", "never purchased", "never bought", "never ordered"):
		add("last_purchase_at", "is_null", nil)
	case cart && noPurchase:
		if days > 0 {
			add("last_cart_add_at", "within_last_days", days)
		}
		add("cart_abandoned", "is_true", nil)
		p.Assumptions = append(p.Assumptions, say(
			"\"Alışveriş yapmayan\" sepete eklemeden sonra alışveriş yapmamış olarak yorumlandı (terk edilmiş sepet). Diğer okuma: son 30 günde hiç alışveriş yapmamış.",
			"\"Didn't buy\" was read as no purchase since the cart add (abandoned cart). Other reading: no purchase at all in the period."))
	case cart && days > 0:
		add("last_cart_add_at", "within_last_days", days)
	case noPurchase && days > 0:
		add("last_purchase_at", "not_within_last_days", days)
		p.Assumptions = append(p.Assumptions, say("Hiç alışveriş yapmamış kullanıcılar da dahil edildi.", "Users who never purchased are included."))
	case containsAny(f, "alisveris yapan", "alisveris yapmis", "satin alan", "purchased", "bought", "customers who bought") && days > 0:
		add("last_purchase_at", "within_last_days", days)
	case containsAny(f, "giris yapmayan", "giris yapmamis", "inactive", "pasif", "haven't logged", "not logged") && days > 0:
		add("last_login_at", "not_within_last_days", days)
	case containsAny(f, "signed up", "sign up", "uye olan", "uye olmus", "kayit olan", "new members") && days > 0:
		add("signup_at", "within_last_days", days)
	}
	if m := spentRE.FindStringSubmatch(f); m != nil {
		if n := firstNumber(m); n > 0 && n <= 10000000 {
			add("total_spent", "gt", n)
		}
	}
	if m := purchasesRE.FindStringSubmatch(f); m != nil {
		if n := firstNumber(m); n > 0 && n <= 100000 {
			add("purchase_count", "gte", n)
		}
	}
	if containsAny(f, "english", "ingilizce") {
		add("locale", "eq", "en")
	}

	var tiers []string
	for _, t := range catalog.LoyaltyTiers {
		if containsAny(f, tierWords[t]...) {
			tiers = append(tiers, t)
		}
	}
	if len(tiers) == 1 {
		add("loyalty_tier", "eq", tiers[0])
	} else if len(tiers) > 1 {
		add("loyalty_tier", "in", tiers)
	}

	for _, c := range catalog.Categories {
		if containsAny(f, categoryWords[c]...) {
			add("favorite_category", "eq", c)
			break
		}
	}

	if m := ageRangeRE.FindStringSubmatch(f); m != nil {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		if lo >= 18 && hi <= 100 && lo <= hi {
			add("age", "between", []int{lo, hi})
		}
	} else {
		if n := firstNumber(underAgeRE.FindStringSubmatch(f)); n > 18 && n <= 100 {
			add("age", "lt", n)
		}
		if n := firstNumber(overAgeRE.FindStringSubmatch(f)); n >= 18 && n < 100 {
			add("age", "gt", n)
		}
	}

	for _, w := range unsupportedWords {
		if strings.Contains(f, w) {
			p.UnsupportedCriteria = append(p.UnsupportedCriteria, say("İlgi alanı / demografik veri yok: ", "No data for: ")+strings.TrimSpace(w))
			break
		}
	}
	if containsAny(f, "izin vermeyen", "izni olmayan", "opted out", "opt-out", "unsubscribed") {
		p.Assumptions = append(p.Assumptions, say("İzin vermeyen kullanıcılara gönderim yapılamaz; kanal izni sistem tarafından her zaman uygulanır.",
			"Users who opted out cannot be messaged; channel consent is always enforced by the system."))
	}

	sms, email := strings.Contains(f, "sms"), containsAny(f, "e-posta", "eposta", "email", "e-mail", "mail")
	switch {
	case sms && !email:
		p.ChannelsRequested = "sms"
	case email && !sms:
		p.ChannelsRequested = "email"
	}

	if len(p.Segment.Groups) > catalog.MaxGroups {
		p.Segment.Groups = p.Segment.Groups[:catalog.MaxGroups]
	}
	vr, errs, _ := segment.Validate(p.Segment)
	if len(errs) == 0 {
		p.Explanation = strings.Join(segment.Render(vr, p.Language), " "+segment.Joiner(p.Language)+" ") + "."
	}
	if len(p.Segment.Groups) == 0 {
		p.Assumptions = append(p.Assumptions, say("Demo modu bu brief için bir koşul bulamadı; tüm kullanıcılar hedeflendi.",
			"Demo mode found no condition in this brief; it targets all users."))
	}
	return p
}

func firstNumber(m []string) int {
	for _, s := range m[min(1, len(m)):] {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 0
}

func containsAny(s string, subs ...string) bool {
	return slices.ContainsFunc(subs, func(x string) bool { return strings.Contains(s, x) })
}

var discountRE = regexp.MustCompile(`%\s?(\d{1,2})|(\d{1,2})\s?%`)

func subjectOffer(discount, lang string) string {
	switch {
	case lang == "en" && discount != "":
		return discount + "% off is waiting for you"
	case lang == "en":
		return "something special is waiting for you"
	case discount != "":
		return "%" + discount + " indirim seni bekliyor"
	default:
		return "sana özel fırsatlar seni bekliyor"
	}
}

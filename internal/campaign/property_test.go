package campaign

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"smartcampaign/internal/content"
	"smartcampaign/internal/delivery"
	"smartcampaign/internal/segment"
)

// randomRule builds a random CNF rule from condition templates covering every field type.
func randomRule(rng *rand.Rand) string {
	cities := []string{"İstanbul", "Ankara", "İzmir", "Bursa", "Antalya", "Konya"}
	tiers := []string{"bronze", "silver", "gold", "platinum"}
	templates := []func() string{
		func() string {
			a, b := cities[rng.IntN(len(cities))], cities[rng.IntN(len(cities))]
			return fmt.Sprintf(`{"field":"city","op":"in","value":[%q,%q]}`, a, b)
		},
		func() string {
			lo := 18 + rng.IntN(40)
			return fmt.Sprintf(`{"field":"age","op":"between","value":[%d,%d]}`, lo, lo+5+rng.IntN(30))
		},
		func() string { return fmt.Sprintf(`{"field":"loyalty_tier","op":"eq","value":%q}`, tiers[rng.IntN(len(tiers))]) },
		func() string { return fmt.Sprintf(`{"field":"purchase_count","op":"gte","value":%d}`, rng.IntN(10)) },
		func() string {
			return fmt.Sprintf(`{"field":"last_purchase_at","op":"not_within_last_days","value":%d}`, 7+rng.IntN(180))
		},
		func() string { return `{"field":"cart_abandoned","op":"is_true","value":null}` },
		func() string { return fmt.Sprintf(`{"field":"total_spent","op":"lt","value":%d}`, 100+rng.IntN(20000)) },
	}
	var groups []string
	for range 1 + rng.IntN(3) {
		var cs []string
		for range 1 + rng.IntN(2) {
			cs = append(cs, templates[rng.IntN(len(templates))]())
		}
		groups = append(groups, `{"conditions":[`+strings.Join(cs, ",")+`]}`)
	}
	return `{"groups":[` + strings.Join(groups, ",") + `]}`
}

// TestApprove_ConsentProperty_RAUD2: for random rules × random consent distributions × random channel
// selections, approve never enqueues a message on a channel the user has not consented to, and enqueues
// exactly the consented part of the audience.
func TestApprove_ConsentProperty_RAUD2(t *testing.T) {
	svc, pool := newService(t, 2000)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(20261109, 2))
	approved, empty := 0, 0
	for i := range 30 {
		// A different consent distribution each round: 5–95 % per channel, independently.
		if _, err := pool.Exec(ctx, `UPDATE users SET sms_consent = abs(hashtext(id::text || $1)) % 100 < $2,
				email_consent = abs(hashtext($1 || id::text)) % 100 < $3`, fmt.Sprint(i), 5+rng.IntN(91), 5+rng.IntN(91)); err != nil {
			t.Fatal(err)
		}
		raw := randomRule(rng)
		r, issues := segment.ParseRule([]byte(raw))
		if len(issues) > 0 {
			t.Fatalf("generator produced an unparsable rule: %s %v", raw, issues)
		}
		c, err := svc.Create(ctx, "property "+fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		c, err = svc.PutSegment(ctx, c.ID, r)
		if code := codeOf(err); code == "validation_failed" {
			continue // e.g. a provable contradiction: never reaches approve
		} else if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		v, err := svc.SaveContent(ctx, c.ID, content.Content{SMSText: "Merhaba {{first_name}}!", EmailSubject: "Merhaba",
			EmailBody: "Merhaba {{first_name}},\n\nYeni ürünler."}, 0)
		if err != nil {
			t.Fatal(err)
		}
		channels := [][]string{{"sms"}, {"email"}, {"sms", "email"}}[rng.IntN(3)]
		res, err := svc.Approve(ctx, c.ID, "k"+fmt.Sprint(i), channels, v.Version)
		if codeOf(err) == "empty_audience" {
			empty++
			continue
		}
		if err != nil {
			t.Fatalf("approve %s %v: %v", raw, channels, err)
		}
		approved++
		var bad int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages m JOIN users u ON u.id = m.user_id WHERE m.campaign_id = $1
			AND ((m.channel = 'sms' AND NOT u.sms_consent) OR (m.channel = 'email' AND NOT u.email_consent))`, c.ID).Scan(&bad); err != nil {
			t.Fatal(err)
		}
		if bad != 0 {
			t.Fatalf("round %d: %d messages to users without consent (rule %s)", i, bad, raw)
		}
		a := c.Segment.Audience
		want := map[string]int{"sms": 0, "email": 0}
		for _, ch := range channels {
			want[ch] = map[string]int{"sms": a.SMSReachable, "email": a.EmailReachable}[ch]
		}
		if res.Enqueued["sms"] != want["sms"] || res.Enqueued["email"] != want["email"] {
			t.Fatalf("round %d: enqueued %v, want the consented audience %v (rule %s)", i, res.Enqueued, want, raw)
		}
	}
	t.Logf("%d approved, %d rejected as empty audience", approved, empty)
	if approved < 15 {
		t.Fatalf("only %d rounds reached approve; the property was barely exercised", approved)
	}
}

// TestApprove_SMSFitsForEveryRecipient_RCNT1: an SMS validated at exactly 160 code points worst case, rendered
// the way the worker renders it, fits for every consented recipient of the seeded audience (which includes
// 40-rune names, empty names and hostile names). One more character is rejected before approve.
func TestApprove_SMSFitsForEveryRecipient_RCNT1(t *testing.T) {
	svc, pool := newService(t, 5000)
	ctx := context.Background()
	c, err := svc.Create(ctx, "everyone")
	if err != nil {
		t.Fatal(err)
	}
	all, _ := segment.ParseRule([]byte(`{"groups":[]}`))
	if c, err = svc.PutSegment(ctx, c.ID, all); err != nil {
		t.Fatal(err)
	}
	worst := func(sms string) (int, bool) {
		rep, err := svc.ValidateContent(ctx, content.Content{SMSText: sms, EmailSubject: "s", EmailBody: "b"}, c.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		f, ok := rep.Fields[content.PathSMS].(content.SMSField)
		if !ok {
			b, _ := json.Marshal(rep.Fields[content.PathSMS])
			t.Fatalf("sms field report: %s", b)
		}
		return f.WorstCaseRunes, rep.ValidFor["sms"]
	}
	base := "Merhaba {{first_name}}, {{city}} mağazamızda indirim: "
	w, _ := worst(base)
	sms := base + strings.Repeat("ş", 160-w)
	if w, ok := worst(sms); w != 160 || !ok {
		t.Fatalf("worst case %d valid %v, want exactly 160 and valid", w, ok)
	}
	if w, ok := worst(sms + "ş"); w != 161 || ok {
		t.Fatalf("one more character: worst case %d valid %v, want 161 and invalid", w, ok)
	}
	v, err := svc.SaveContent(ctx, c.ID, content.Content{SMSText: sms, EmailSubject: "s", EmailBody: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, c.ID, "every-user", []string{"sms"}, v.Version); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT u.phone, u.first_name, u.city, COALESCE(c.language, u.locale)
		FROM messages m JOIN users u ON u.id = m.user_id JOIN campaigns c ON c.id = m.campaign_id WHERE m.campaign_id = $1`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n, longest := 0, 0
	for rows.Next() {
		var r delivery.Recipient
		if err := rows.Scan(&r.Phone, &r.FirstName, &r.City, &r.Locale); err != nil {
			t.Fatal(err)
		}
		out, tooLong := delivery.RenderMessage(delivery.SMS, delivery.Template{SMSText: sms}, r)
		if l := utf8.RuneCountInString(out.Text); tooLong || l > 160 {
			t.Fatalf("recipient %q / %q renders %d code points", r.FirstName, r.City, l)
		} else {
			longest = max(longest, l)
		}
		n++
	}
	t.Logf("%d recipients rendered, longest %d code points", n, longest)
	if n < 3000 {
		t.Fatalf("only %d recipients", n)
	}
}

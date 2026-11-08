package campaign

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"smartcampaign/internal/content"
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

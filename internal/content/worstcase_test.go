package content

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"smartcampaign/internal/segment"
	"smartcampaign/internal/store/storetest"
)

func TestWorstCaseVars_PerChannelConsentedAndCapped_RCNT1(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	users := []struct {
		name, city string
		sms, email bool
	}{
		{"Ali", "İstanbul", true, true},
		{"Hüseyin Muhammed Abdurrahman Abdülkerim", "İstanbul", false, true}, // > 20 runes → fallback
		{"Mehmetcan", "İstanbul", true, false},
		{"Zeynepgülnurayşe", "İstanbul", false, false}, // no consent: must not count
		{"Abdurrahman Kerimcan", "Ankara", true, true}, // outside the rule
		{"Ay\u0007şe", "İstanbul", true, false},        // control char stripped → 4 runes
	}
	now := time.Now()
	for i, u := range users {
		_, err := pool.Exec(ctx, `INSERT INTO users (seed_key, first_name, email, phone, city, locale, birth_date, signup_at,
			loyalty_tier, sms_consent, email_consent) VALUES ($1, $2, $3, '+905550000000', $4, 'tr', $5::date, $5::timestamptz, 'bronze', $6, $7)`,
			fmt.Sprintf("w-%d", i), u.name, fmt.Sprintf("w%d@example.com", i), u.city, now.AddDate(-30, 0, 0), u.sms, u.email)
		if err != nil {
			t.Fatal(err)
		}
	}
	vr, errs, _ := segment.Validate(segment.Rule{Groups: []segment.Group{{Conditions: []segment.Condition{
		{Field: "city", Op: "eq", Value: json.RawMessage(`"İstanbul"`)}}}}})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	got, err := WorstCaseVars(ctx, pool, vr)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Vars{
		"sms":   {"first_name": "Mehmetcan", "city": "İstanbul"},
		"email": {"first_name": "Değerli müşterimiz", "city": "İstanbul"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("worst case (-want +got):\n%s", diff)
	}
	if _, err := WorstCaseVars(ctx, pool, segment.ValidRule{}); err == nil {
		t.Fatal("unvalidated rule must be rejected")
	}
}

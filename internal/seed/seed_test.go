package seed

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"smartcampaign/internal/store/storetest"
)

var anchor = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func TestGenerate_IsDeterministic_RRES2(t *testing.T) {
	a := Generate(500, 42, anchor)
	b := Generate(500, 42, anchor)
	if diff := cmp.Diff(a, b); diff != "" {
		t.Fatalf("same seed, different users (-a +b):\n%s", diff)
	}
	if cmp.Equal(a, Generate(500, 43, anchor)) {
		t.Fatal("different seeds produced identical users")
	}
}

func TestGenerate_ShapeAndEdgeCases_RRES2(t *testing.T) {
	users := Generate(5000, 20261108, anchor)
	if len(users) != 5000 {
		t.Fatalf("len = %d", len(users))
	}
	e164 := regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	keys := map[string]bool{}
	var invalidPhones, noConsent int
	for _, u := range users {
		if keys[u.SeedKey] {
			t.Fatalf("duplicate seed key %s", u.SeedKey)
		}
		keys[u.SeedKey] = true
		if !e164.MatchString(u.Phone) {
			invalidPhones++
		}
		if !u.SMSConsent && !u.EmailConsent {
			noConsent++
		}
		if u.LastPurchaseAt != nil && u.LastPurchaseAt.After(anchor) || u.LastCartAddAt != nil && u.LastCartAddAt.After(anchor) {
			t.Fatalf("future timestamp in %s", u.SeedKey)
		}
	}
	for _, k := range []string{"edge-template-name", "edge-script-name", "edge-rtl-name", "edge-long-name", "edge-empty-name", "edge-invalid-phone", "edge-no-consent"} {
		if !keys[k] {
			t.Errorf("edge user %s missing", k)
		}
	}
	if invalidPhones != 1 {
		t.Errorf("invalid phones = %d, want exactly the edge case", invalidPhones)
	}
	if noConsent < 100 {
		t.Errorf("only %d users without any consent; consent filtering would be untested", noConsent)
	}
}

func TestRun_IsIdempotent_RRES2(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	users := Generate(300, 7, anchor)
	n, err := Run(ctx, pool, users)
	if err != nil || n != 300 {
		t.Fatalf("first run: %d, %v", n, err)
	}
	n, err = Run(ctx, pool, Generate(300, 7, anchor.Add(time.Hour)))
	if err != nil || n != 0 {
		t.Fatalf("second run inserted %d, %v; want 0", n, err)
	}
	var abandonedInIstanbul int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE city = 'İstanbul' AND cart_abandoned
		AND last_cart_add_at >= $1::timestamptz - interval '30 days'`, anchor).Scan(&abandonedInIstanbul)
	if err != nil || abandonedInIstanbul == 0 {
		t.Fatalf("official brief audience is empty (%d, %v)", abandonedInIstanbul, err)
	}
}

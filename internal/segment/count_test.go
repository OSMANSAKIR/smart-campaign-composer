package segment

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/seed"
	"smartcampaign/internal/store/storetest"
)

// testUser inserts one user with sensible defaults; set overrides SQL column values.
type testUser struct {
	city          string
	lastPurchase  *time.Duration // ago; nil = never
	lastCartAdd   *time.Duration
	sms, email    bool
	favoriteCat   *string
	purchaseCount int
	birthYearsAgo int
	locale        string // "" = tr
}

func ago(d time.Duration) *time.Duration { return &d }

func insertUsers(t *testing.T, pool *pgxpool.Pool, users []testUser) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	ts := func(d *time.Duration) *time.Time {
		if d == nil {
			return nil
		}
		x := now.Add(-*d)
		return &x
	}
	for i, u := range users {
		city := u.city
		if city == "" {
			city = "İstanbul"
		}
		years := u.birthYearsAgo
		if years == 0 {
			years = 30
		}
		locale := u.locale
		if locale == "" {
			locale = "tr"
		}
		_, err := pool.Exec(ctx, `INSERT INTO users (seed_key, first_name, email, phone, city, locale, birth_date, signup_at,
			last_purchase_at, purchase_count, last_cart_add_at, favorite_category, loyalty_tier, sms_consent, email_consent)
			VALUES ($1, 'Test', $2, '+905550000000', $3, $12, $4, $5, $6, $7, $8, $9, 'bronze', $10, $11)`,
			fmt.Sprintf("t-%d", i), fmt.Sprintf("t%d@example.com", i), city, now.AddDate(-years, 0, -1), now.Add(-400*24*time.Hour),
			ts(u.lastPurchase), u.purchaseCount, ts(u.lastCartAdd), u.favoriteCat, u.sms, u.email, locale)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func mustValid(t *testing.T, r Rule) ValidRule {
	t.Helper()
	vr, errs, _ := Validate(r)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	return vr
}

const day = 24 * time.Hour

func TestNotWithinLastDays_IncludesNull_RAUD1(t *testing.T) {
	pool := storetest.NewDB(t)
	books := "books"
	insertUsers(t, pool, []testUser{
		{lastPurchase: nil, sms: true, email: true},                                 // never purchased, category unknown
		{lastPurchase: ago(100 * day), sms: true, email: true, favoriteCat: &books}, // long ago
		{lastPurchase: ago(5 * day), sms: true, email: true},                        // recent
	})
	ctx := context.Background()
	tests := []struct {
		name string
		r    Rule
		want int
	}{
		{"not_within includes never", rule(one(c("last_purchase_at", "not_within_last_days", "30"))), 2},
		{"within excludes never", rule(one(c("last_purchase_at", "within_last_days", "30"))), 1},
		{"is_null", rule(one(c("last_purchase_at", "is_null", "null"))), 1},
		{"before excludes never", rule(one(c("last_purchase_at", "before", `"2100-01-01"`))), 2},
		{"neq excludes null category", rule(one(c("favorite_category", "neq", `"fashion"`))), 1},
		{"not_in excludes null category", rule(one(c("favorite_category", "not_in", `["fashion"]`))), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := Count(ctx, pool, mustValid(t, tt.r))
			if err != nil {
				t.Fatal(err)
			}
			if a.Total != tt.want {
				t.Fatalf("total = %d, want %d", a.Total, tt.want)
			}
		})
	}
}

func TestCount_ExcludesNonConsented_RAUD2(t *testing.T) {
	pool := storetest.NewDB(t)
	insertUsers(t, pool, []testUser{
		{sms: true, email: true},
		{sms: true, email: false},
		{sms: false, email: true},
		{sms: false, email: false},
		{sms: false, email: false},
		{city: "Ankara", sms: true, email: true}, // outside the rule
	})
	a, err := Count(context.Background(), pool, mustValid(t, rule(one(c("city", "eq", `"İstanbul"`)))))
	if err != nil {
		t.Fatal(err)
	}
	a.ByLocale = nil // covered by TestCount_PerLocale_BThree
	want := Audience{Total: 5, SMSReachable: 2, EmailReachable: 2, Both: 1}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("audience = %+v, want %+v", a, want)
	}
}

// B-3: the count also reports consented counts per recipient locale; both locales are always present, and
// the per-locale counts add up to the totals.
func TestCount_PerLocale_BThree(t *testing.T) {
	pool := storetest.NewDB(t)
	insertUsers(t, pool, []testUser{
		{sms: true, email: true}, {sms: true, email: false}, {sms: false, email: false},
		{sms: true, email: true, locale: "en"}, {sms: false, email: true, locale: "en"},
		{city: "Ankara", sms: true, email: true, locale: "en"}, // outside the rule
	})
	a, err := Count(context.Background(), pool, mustValid(t, rule(one(c("city", "eq", `"İstanbul"`)))))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]LocaleAudience{
		"tr": {Total: 3, SMSReachable: 2, EmailReachable: 1, Both: 1},
		"en": {Total: 2, SMSReachable: 1, EmailReachable: 2, Both: 1},
	}
	if !reflect.DeepEqual(a.ByLocale, want) {
		t.Fatalf("by_locale = %+v, want %+v", a.ByLocale, want)
	}
	if a.Total != 5 || a.SMSReachable != 3 || a.EmailReachable != 3 || a.Both != 2 {
		t.Fatalf("totals = %+v do not add up", a)
	}
	empty, err := Count(context.Background(), pool, mustValid(t, rule(one(c("city", "eq", `"İzmir"`)))))
	if err != nil || len(empty.ByLocale) != 2 || empty.Total != 0 {
		t.Fatalf("empty audience = %+v, %v; want both locales present with zeros", empty, err)
	}
}

// Count must agree with hand-written SQL on the real seed data for a spread of rules.
func TestCount_MatchesDirectSQL_RFLOW1(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	if _, err := seed.Run(ctx, pool, seed.Generate(3000, 20261108, time.Now())); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		r      Rule
		direct string
	}{
		{"official brief golden rule", goldenRule(),
			`u.city = 'İstanbul' AND u.last_cart_add_at >= now() - interval '30 days' AND u.cart_abandoned`},
		{"age band or premium tier", rule(one(c("age", "between", "[25, 34]"), c("loyalty_tier", "in", `["gold","platinum"]`))),
			`(EXTRACT(YEAR FROM age(u.birth_date))::int BETWEEN 25 AND 34 OR u.loyalty_tier IN ('gold','platinum'))`},
		{"lapsed buyers incl. never", rule(one(c("last_purchase_at", "not_within_last_days", "90"))),
			`(u.last_purchase_at IS NULL OR u.last_purchase_at < now() - interval '90 days')`},
		{"signed up before date", rule(one(c("signup_at", "before", `"2026-01-01"`))),
			`u.signup_at < timestamptz '2026-01-01 00:00:00 Europe/Istanbul'`},
		{"signed up after date", rule(one(c("signup_at", "after", `"2025-06-30"`))),
			`u.signup_at >= timestamptz '2025-07-01 00:00:00 Europe/Istanbul'`},
		{"category not books", rule(one(c("favorite_category", "neq", `"books"`))), `u.favorite_category <> 'books'`},
		{"big spenders with many purchases", rule(one(c("total_spent", "gt", "1500.5")), one(c("purchase_count", "gte", "3"))),
			`u.total_spent > 1500.5 AND u.purchase_count >= 3`},
		{"no abandoned cart", rule(one(c("cart_abandoned", "is_false", "null"))), `NOT u.cart_abandoned`},
		{"everyone", Rule{}, `TRUE`},
	}
	nonZero := 0
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Count(ctx, pool, mustValid(t, tt.r))
			if err != nil {
				t.Fatal(err)
			}
			var want Audience
			err = pool.QueryRow(ctx, `SELECT count(*),
				count(*) FILTER (WHERE sms_consent), count(*) FILTER (WHERE email_consent),
				count(*) FILTER (WHERE sms_consent AND email_consent) FROM users u WHERE `+tt.direct).
				Scan(&want.Total, &want.SMSReachable, &want.EmailReachable, &want.Both)
			if err != nil {
				t.Fatal(err)
			}
			byLocale := got.ByLocale
			got.ByLocale = nil
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Count = %+v, direct SQL = %+v", got, want)
			}
			if sum := byLocale["tr"].Total + byLocale["en"].Total; sum != want.Total {
				t.Fatalf("by_locale totals add up to %d, want %d", sum, want.Total)
			}
			if got.Total > 0 {
				nonZero++
			}
		})
	}
	if nonZero < len(tests)-1 {
		t.Fatalf("only %d of %d rules matched any user; the comparison is too weak", nonZero, len(tests))
	}
}

func TestReadWithTimeout_StopsSlowQueryAndDoesNotLeak_RAUD1(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	err := ReadWithTimeout(ctx, pool, 50*time.Millisecond, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT pg_sleep(2)")
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("want query_canceled (57014), got %v", err)
	}
	// Inside a caller's transaction the SET LOCAL must not survive the helper.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var before, after string
	_ = tx.QueryRow(ctx, "SHOW statement_timeout").Scan(&before)
	if err := ReadWithTimeout(ctx, tx, 1234*time.Millisecond, func(pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_ = tx.QueryRow(ctx, "SHOW statement_timeout").Scan(&after)
	if before != after {
		t.Fatalf("statement_timeout leaked: before %q, after %q", before, after)
	}
}

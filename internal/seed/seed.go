// Package seed generates the synthetic users (R-RES-2). Generation is deterministic for a given random seed:
// only the anchor time differs between runs, so relative briefs ("last 30 days") keep working on review days
// (need.md N-ORC-007). Inserting is idempotent via the unique seed_key.
package seed

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/catalog"
)

// User is one synthetic user row.
type User struct {
	SeedKey          string
	FirstName        string
	LastName         string
	Email            string
	Phone            string
	City             string
	Locale           string
	BirthDate        time.Time
	SignupAt         time.Time
	LastLoginAt      *time.Time
	LastPurchaseAt   *time.Time
	PurchaseCount    int
	TotalSpent       float64
	LastCartAddAt    *time.Time
	FavoriteCategory *string
	LoyaltyTier      string
	SMSConsent       bool
	EmailConsent     bool
	ConsentUpdatedAt *time.Time
}

const day = 24 * time.Hour

var (
	trFirst = []string{"Ayşe", "Fatma", "Zeynep", "Elif", "Emine", "Hatice", "Merve", "Büşra", "Şeyma", "Gülşen", "Özge", "İrem", "Sıla", "Çağla", "Ebru", "Kübra", "Işıl", "Ilgın",
		"Mehmet", "Mustafa", "Ahmet", "Ali", "Hüseyin", "Hasan", "İbrahim", "İsmail", "Yusuf", "Murat", "Ömer", "Emre", "Burak", "Çağrı", "Barış", "Gökhan", "Serkan", "Uğur", "Yiğit", "Doğukan"}
	trLast  = []string{"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk", "Aydın", "Özdemir", "Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Kara", "Koç", "Kurt", "Özkan", "Şimşek", "Polat", "Güneş", "Aksoy", "Bulut"}
	enFirst = []string{"John", "Emma", "Oliver", "Sophia", "Liam", "Mia", "Noah", "Ava", "Lucas", "Grace"}
	enLast  = []string{"Smith", "Brown", "Taylor", "Wilson", "Clark", "Evans", "Walker", "Hughes"}
	// Weighted cities: İstanbul ≈ 30%, Ankara ≈ 12%, İzmir ≈ 10%, the rest share the remainder.
	cityWeights = []int{30, 12, 10, 7, 7, 6, 6, 5, 5, 4, 4, 4}
	asciiFold   = strings.NewReplacer("ç", "c", "Ç", "c", "ğ", "g", "Ğ", "g", "ı", "i", "İ", "i", "ö", "o", "Ö", "o", "ş", "s", "Ş", "s", "ü", "u", "Ü", "u")
)

// Generate returns exactly n users. The first users are hand-written edge cases (when n allows).
func Generate(n int, randomSeed uint64, now time.Time) []User {
	now = now.UTC().Truncate(time.Second)
	users := make([]User, 0, n)
	for _, e := range edgeUsers(now) {
		if len(users) == n {
			return users
		}
		users = append(users, e)
	}
	r := rand.New(rand.NewPCG(randomSeed, randomSeed^0x5eed))
	for i := 1; len(users) < n; i++ {
		users = append(users, regularUser(r, i, now))
	}
	return users
}

func regularUser(r *rand.Rand, i int, now time.Time) User {
	u := User{SeedKey: fmt.Sprintf("u-%06d", i), Locale: "tr"}
	if r.IntN(100) < 8 {
		u.Locale = "en"
		u.FirstName, u.LastName = pick(r, enFirst), pick(r, enLast)
	} else {
		u.FirstName, u.LastName = pick(r, trFirst), pick(r, trLast)
	}
	u.Email = fmt.Sprintf("%s.%s.%d@example.com", strings.ToLower(asciiFold.Replace(u.FirstName)), strings.ToLower(asciiFold.Replace(u.LastName)), i)
	u.Phone = fmt.Sprintf("+905%09d", r.IntN(1_000_000_000))
	u.City = catalog.Cities[weighted(r, cityWeights)]

	age := 18 + r.IntN(58)
	u.BirthDate = now.AddDate(-age, 0, -r.IntN(365)).Truncate(day)
	signupDays := 1 + r.IntN(1500)
	u.SignupAt = now.Add(-ago(r, signupDays))
	if r.IntN(100) < 85 {
		u.LastLoginAt = ptr(now.Add(-ago(r, min(signupDays, 1+r.IntN(90)))))
	}

	if r.IntN(100) >= 30 {
		u.PurchaseCount = 1 + int(math.Floor(r.ExpFloat64()*8))
		u.TotalSpent = math.Round(float64(u.PurchaseCount)*(50+r.Float64()*750)*100) / 100
		u.LastPurchaseAt = ptr(now.Add(-ago(r, min(signupDays, 1+r.IntN(365)))))
	}
	if r.IntN(100) < 45 {
		cartDays := 31 + r.IntN(150)
		if r.IntN(100) < 60 {
			cartDays = 1 + r.IntN(30)
		}
		cart := now.Add(-ago(r, min(signupDays, cartDays)))
		u.LastCartAddAt = &cart
		// For about half of the buyers with a cart, the last purchase came after the cart add (not abandoned).
		if u.LastPurchaseAt != nil && r.IntN(2) == 0 {
			after := cart.Add(time.Duration(r.Int64N(int64(now.Sub(cart)) + 1)))
			u.LastPurchaseAt = &after
		}
	}
	if r.IntN(100) >= 10 {
		u.FavoriteCategory = ptr(pick(r, catalog.Categories))
	}
	u.LoyaltyTier = tier(u.TotalSpent)
	u.SMSConsent = r.IntN(100) < 70
	u.EmailConsent = r.IntN(100) < 80
	u.ConsentUpdatedAt = ptr(u.SignupAt.Add(time.Duration(r.Int64N(int64(now.Sub(u.SignupAt)) + 1))))
	return u
}

// edgeUsers exercise rendering, escaping, consent and provider failure paths. Most of them match the
// official example brief (İstanbul, cart added within 30 days, no purchase since) so they show up in the demo.
func edgeUsers(now time.Time) []User {
	base := func(key, first string) User {
		cart := now.Add(-3 * day)
		return User{
			SeedKey: "edge-" + key, FirstName: first, LastName: "Test", Email: "edge." + key + "@example.com",
			Phone: "+905550000" + fmt.Sprintf("%03d", len(key)*7%1000), City: "İstanbul", Locale: "tr",
			BirthDate: now.AddDate(-30, 0, 0).Truncate(day), SignupAt: now.Add(-400 * day),
			LastLoginAt: ptr(now.Add(-2 * day)), LastCartAddAt: &cart, LoyaltyTier: "bronze",
			FavoriteCategory: ptr("fashion"), SMSConsent: true, EmailConsent: true, ConsentUpdatedAt: ptr(now.Add(-400 * day)),
		}
	}
	with := func(u User, f func(*User)) User { f(&u); return u }
	purchased := func(d time.Duration) *time.Time { return ptr(now.Add(-d)) }
	users := []User{
		base("template-name", "{{city}}"),
		base("script-name", "<script>"),
		base("rtl-name", "\u202eilA"),
		base("long-name", "Hüseyin Muhammed Abdurrahman Abdülkerim"),
		base("name-20-runes", "Abdurrahman Kerimcan"),
		base("empty-name", ""),
		base("emoji-name", "Ayşe 🌸"),
		base("control-char-name", "Ay\u0007şe"),
		with(base("invalid-phone", "Selin"), func(u *User) { u.Phone = "05321234567" }),
		with(base("invalid-email", "Deniz"), func(u *User) { u.Email = "not-an-email" }),
		with(base("no-consent", "Kerem"), func(u *User) { u.SMSConsent, u.EmailConsent = false, false }),
		with(base("sms-only", "Nazlı"), func(u *User) { u.EmailConsent = false }),
		with(base("email-only", "Tolga"), func(u *User) { u.SMSConsent = false }),
		with(base("purchased-after-cart", "Ece"), func(u *User) { u.PurchaseCount, u.TotalSpent, u.LastPurchaseAt = 3, 900, purchased(day) }),
		with(base("purchased-before-cart", "Can"), func(u *User) { u.PurchaseCount, u.TotalSpent, u.LastPurchaseAt = 5, 1500, purchased(20*day) }),
		with(base("null-category", "Pınar"), func(u *User) { u.FavoriteCategory = nil }),
		with(base("english", "John"), func(u *User) { u.Locale, u.City, u.LastName = "en", "İzmir", "Smith" }),
		with(base("age-18", "Arda"), func(u *User) { u.BirthDate = now.AddDate(-18, 0, -1).Truncate(day) }),
	}
	for i := range users {
		users[i].LoyaltyTier = tier(users[i].TotalSpent)
	}
	return users
}

// Run inserts the users; existing seed_keys are left untouched (idempotent). It returns how many were new.
func Run(ctx context.Context, pool *pgxpool.Pool, users []User) (int, error) {
	const q = `INSERT INTO users (seed_key, first_name, last_name, email, phone, city, locale, birth_date, signup_at,
		last_login_at, last_purchase_at, purchase_count, total_spent, last_cart_add_at, favorite_category, loyalty_tier,
		sms_consent, email_consent, consent_updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT (seed_key) DO NOTHING`
	inserted := 0
	for start := 0; start < len(users); start += 1000 {
		batch := &pgx.Batch{}
		for _, u := range users[start:min(start+1000, len(users))] {
			batch.Queue(q, u.SeedKey, u.FirstName, u.LastName, u.Email, u.Phone, u.City, u.Locale, u.BirthDate, u.SignupAt,
				u.LastLoginAt, u.LastPurchaseAt, u.PurchaseCount, u.TotalSpent, u.LastCartAddAt, u.FavoriteCategory, u.LoyaltyTier,
				u.SMSConsent, u.EmailConsent, u.ConsentUpdatedAt)
		}
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			br := tx.SendBatch(ctx, batch)
			for range batch.Len() {
				tag, err := br.Exec()
				if err != nil {
					br.Close()
					return err
				}
				inserted += int(tag.RowsAffected())
			}
			return br.Close()
		})
		if err != nil {
			return inserted, fmt.Errorf("insert users: %w", err)
		}
	}
	return inserted, nil
}

func tier(spent float64) string {
	switch {
	case spent >= 15000:
		return "platinum"
	case spent >= 5000:
		return "gold"
	case spent >= 1000:
		return "silver"
	default:
		return "bronze"
	}
}

func ago(r *rand.Rand, maxDays int) time.Duration {
	return time.Duration(r.Int64N(int64(maxDays) * int64(day)))
}

func weighted(r *rand.Rand, weights []int) int {
	total := 0
	for _, w := range weights {
		total += w
	}
	n := r.IntN(total)
	for i, w := range weights {
		if n < w {
			return i
		}
		n -= w
	}
	return len(weights) - 1
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }
func ptr[T any](v T) *T                  { return &v }

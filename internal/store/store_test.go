package store_test

import (
	"context"
	"testing"

	"smartcampaign/internal/store"
	"smartcampaign/internal/store/storetest"
)

func TestMigrate_IsIdempotentAndComplete_RDEL2(t *testing.T) {
	pool := storetest.NewDB(t) // already migrated once
	ctx := context.Background()

	applied, err := store.Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("second run applied %v, want nothing", applied)
	}
	ok, err := store.MigrationsApplied(ctx, pool)
	if err != nil || !ok {
		t.Fatalf("MigrationsApplied = %v, %v", ok, err)
	}
	for _, table := range []string{"users", "campaigns", "campaign_content", "messages", "delivery_attempts", "llm_calls", "worker_heartbeats"} {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || !exists {
			t.Errorf("table %s missing (%v)", table, err)
		}
	}
}

func TestMessages_UniquePerCampaignUserChannel_RSND3(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	mustExec := func(sql string, args ...any) error {
		_, err := pool.Exec(ctx, sql, args...)
		return err
	}
	if err := mustExec(`INSERT INTO users (seed_key, email, phone, city, locale, birth_date, signup_at, loyalty_tier, sms_consent, email_consent)
		VALUES ('t1', 'a@example.com', '+905320000001', 'Ankara', 'tr', '1990-01-01', now(), 'bronze', true, true)`); err != nil {
		t.Fatal(err)
	}
	var campaignID string
	if err := pool.QueryRow(ctx, "INSERT INTO campaigns (brief) VALUES ('x') RETURNING id").Scan(&campaignID); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO messages (campaign_id, user_id, channel, content_version)
		SELECT $1, id, 'sms', 1 FROM users ON CONFLICT (campaign_id, user_id, channel) DO NOTHING`
	for range 2 {
		if err := mustExec(insert, campaignID); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM messages").Scan(&n); err != nil || n != 1 {
		t.Fatalf("messages = %d, %v; want 1", n, err)
	}
}

// B-3: the migration backfills what existed before it: content rows become the primary copy in the campaign's
// language (tr when it has none), messages take their user's locale, campaigns default to 'campaign'. The
// pre-B-3 shape is rebuilt on a migrated database by undoing exactly what the migration adds.
func TestMigration_B3_BackfillsLegacyRows(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`ALTER TABLE campaign_content DROP CONSTRAINT campaign_content_pkey, DROP COLUMN locale, DROP COLUMN adapted_from,
		ADD PRIMARY KEY (campaign_id, version, variant)`)
	exec(`ALTER TABLE messages DROP COLUMN locale`)
	exec(`ALTER TABLE campaigns DROP COLUMN language_mode`)
	exec(`ALTER TABLE llm_calls DROP CONSTRAINT llm_calls_purpose_check,
		ADD CONSTRAINT llm_calls_purpose_check CHECK (purpose IN ('segment', 'content', 'repair', 'refine'))`)
	exec(`DELETE FROM schema_migrations WHERE version = '20261111120000_b3_recipient_language'`)

	exec(`INSERT INTO users (seed_key, email, phone, city, locale, birth_date, signup_at, loyalty_tier, sms_consent, email_consent)
		VALUES ('u-en', 'e@example.com', '+905320000001', 'Ankara', 'en', '1990-01-01', now(), 'bronze', true, true)`)
	var en, none string
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, language) VALUES ('x', 'en') RETURNING id`).Scan(&en); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief) VALUES ('y') RETURNING id`).Scan(&none); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{en, none} {
		exec(`INSERT INTO campaign_content (campaign_id, version, variant, sms_text, email_subject, email_body, source, validation)
			VALUES ($1, 1, 'a', 's', 'e', 'b', 'llm', '{}')`, id)
	}
	exec(`INSERT INTO messages (campaign_id, user_id, channel, content_version)
		SELECT $1, id, 'sms', 1 FROM users`, en)

	applied, err := store.Migrate(ctx, pool)
	if err != nil || len(applied) != 1 {
		t.Fatalf("migrate = %v, %v; want exactly the B-3 migration", applied, err)
	}
	var locEn, locNone, mode, msgLocale string
	var adaptedNull bool
	if err := pool.QueryRow(ctx, `SELECT locale, adapted_from IS NULL FROM campaign_content WHERE campaign_id = $1`, en).Scan(&locEn, &adaptedNull); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT locale FROM campaign_content WHERE campaign_id = $1`, none).Scan(&locNone); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT language_mode FROM campaigns WHERE id = $1`, en).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT locale FROM messages WHERE campaign_id = $1`, en).Scan(&msgLocale); err != nil {
		t.Fatal(err)
	}
	if locEn != "en" || !adaptedNull || locNone != "tr" || mode != "campaign" || msgLocale != "en" {
		t.Fatalf("backfill: content locale %q (primary %v), no-language campaign %q, mode %q, message locale %q",
			locEn, adaptedNull, locNone, mode, msgLocale)
	}
}

// B-3: a version holds one row per (variant, locale); a second copy of the same locale is refused; the locale
// and the language mode are constrained; llm_calls accepts the `localize` purpose.
func TestMigration_B3_SchemaConstraints(t *testing.T) {
	pool := storetest.NewDB(t)
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, language) VALUES ('x', 'tr') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	insert := func(locale string) error {
		_, err := pool.Exec(ctx, `INSERT INTO campaign_content (campaign_id, version, variant, locale, sms_text, email_subject, email_body, source, validation)
			VALUES ($1, 1, 'a', $2, 's', 'e', 'b', 'llm', '{}')`, id, locale)
		return err
	}
	if err := insert("tr"); err != nil {
		t.Fatal(err)
	}
	if err := insert("en"); err != nil {
		t.Fatalf("a second locale of the same variant must be allowed: %v", err)
	}
	if err := insert("en"); err == nil {
		t.Error("two rows for the same (campaign, version, variant, locale) must be refused")
	}
	if err := insert("de"); err == nil {
		t.Error("an unknown locale must be refused")
	}
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET language_mode = 'everyone' WHERE id = $1`, id); err == nil {
		t.Error("an unknown language_mode must be refused")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO llm_calls (campaign_id, purpose, provider, model, prompt_version, outcome)
		VALUES ($1, 'localize', 'mock', 'm', 'localize_v1', 'valid')`, id); err != nil {
		t.Errorf("llm_calls purpose localize: %v", err)
	}
}

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

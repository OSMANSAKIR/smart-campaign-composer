package segment

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CountTimeout is the statement timeout for the dry-run count. 5k seeded users count in milliseconds; the
// timeout only protects the database from a pathological rule (KICKOFF §6.3 step 6).
var CountTimeout = 2 * time.Second

// Count runs the dry-run count with a statement timeout. Reachable means consent for that channel; both
// means consent for both (R-AUD-2). Consent is never part of the rule; it is applied here.
func Count(ctx context.Context, q Querier, v ValidRule) (Audience, error) {
	where, args, err := Compile(v, 1)
	if err != nil {
		return Audience{}, err
	}
	sql := `SELECT u.locale, count(*),
		count(*) FILTER (WHERE u.sms_consent),
		count(*) FILTER (WHERE u.email_consent),
		count(*) FILTER (WHERE u.sms_consent AND u.email_consent)
		FROM users u WHERE ` + where + ` GROUP BY u.locale`
	a := Audience{ByLocale: map[string]LocaleAudience{"tr": {}, "en": {}}}
	err = ReadWithTimeout(ctx, q, CountTimeout, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var loc string
			var l LocaleAudience
			if err := rows.Scan(&loc, &l.Total, &l.SMSReachable, &l.EmailReachable, &l.Both); err != nil {
				return err
			}
			a.ByLocale[loc] = l
			a.Total += l.Total
			a.SMSReachable += l.SMSReachable
			a.EmailReachable += l.EmailReachable
			a.Both += l.Both
		}
		return rows.Err()
	})
	if err != nil {
		return Audience{}, fmt.Errorf("segment: count: %w", err)
	}
	return a, nil
}

// ReadWithTimeout runs the read-only fn in a transaction (a savepoint when q is already a transaction) whose
// statements are limited by `SET LOCAL statement_timeout`. It always rolls back: that undoes the SET LOCAL
// even inside a caller's transaction (a released savepoint would keep it), and fn must not write.
func ReadWithTimeout(ctx context.Context, q Querier, d time.Duration, fn func(pgx.Tx) error) error {
	tx, err := q.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The value is our own integer, not input; SET does not accept bind parameters.
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", d.Milliseconds())); err != nil {
		return err
	}
	return fn(tx)
}

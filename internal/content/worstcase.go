package content

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/segment"
)

// WorstCaseTimeout bounds the worst-case query like the audience count.
var WorstCaseTimeout = 2 * time.Second

// unknownWorstCase stands in for a variable whose audience values are unknown: the longest value that can
// ever be rendered (catalog.MaxVariableRunes), so validation without data errs on the safe side.
var unknownWorstCase = strings.Repeat("X", catalog.MaxVariableRunes)

// WorstCaseVars returns, per channel ("sms", "email"), the longest value each variable can render to for the
// rule's users who consented to that channel. Values go through the same Value() cleaning and fallback rule
// as send-time rendering (need.md N-ORC-005); when a fallback applies, the longest fallback of any locale is
// used, so the result is an upper bound whatever locale the sender picks. The values are used for validation
// and the report only; they are never sent to the model (the content prompt gets their lengths).
func WorstCaseVars(ctx context.Context, q segment.Querier, v segment.ValidRule) (map[string]Vars, error) {
	where, args, err := segment.Compile(v, 1)
	if err != nil {
		return nil, err
	}
	// Distinct (name, city) pairs are bounded by name diversity, not by audience size; Go then applies the
	// exact Value() semantics (Unicode trimming and Cc stripping) that SQL could only approximate.
	sql := `SELECT u.first_name, u.city, bool_or(u.sms_consent), bool_or(u.email_consent)
		FROM users u WHERE (` + where + `) AND (u.sms_consent OR u.email_consent)
		GROUP BY u.first_name, u.city`
	out := map[string]Vars{"sms": {}, "email": {}}
	err = segment.ReadWithTimeout(ctx, q, WorstCaseTimeout, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var first, city string
			var sms, email bool
			if err := rows.Scan(&first, &city, &sms, &email); err != nil {
				return err
			}
			raw := Vars{"first_name": first, "city": city}
			for ch, ok := range map[string]bool{"sms": sms, "email": email} {
				if ok {
					keepLongest(out[ch], raw)
				}
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("content: worst case: %w", err)
	}
	return out, nil
}

func keepLongest(dst, raw Vars) {
	for _, name := range catalog.Variables {
		for locale := range Fallbacks {
			val := Value(name, locale, raw)
			if cur, ok := dst[name]; !ok || utf8.RuneCountInString(val) > utf8.RuneCountInString(cur) {
				dst[name] = val
			}
		}
	}
}

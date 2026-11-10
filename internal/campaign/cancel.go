package campaign

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// StatusCancelled is the terminal status of a campaign cancelled while scheduled or sending (§9.5).
const StatusCancelled = "cancelled"

// CancelResult is the 200 body of POST …/cancel.
type CancelResult struct {
	Campaign  Campaign `json:"campaign"`
	Cancelled int      `json:"cancelled"` // messages moved from queued/retry to cancelled by this call
}

// Cancel stops a scheduled or sending campaign (F4, docs/contracts/api.md §9.5). One transaction: guarded
// `scheduled | sending → cancelled`, then every queued or retry message of the campaign becomes `cancelled`
// in one statement. Messages already claimed (`sending`) finish and record their outcome; the worker turns a
// retry result or an expired lease of a cancelled campaign into `cancelled` instead of retrying
// (internal/delivery). Calling it again on a cancelled campaign is a no-op that reports 0.
func (s *Service) Cancel(ctx context.Context, id string) (CancelResult, error) {
	if !isUUID(id) {
		return CancelResult{}, ErrNotFound
	}
	var n int
	byChannel := map[string]int{}
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE campaigns SET status = 'cancelled', cancelled_at = now(), updated_at = now()
			WHERE id = $1 AND status IN ('scheduled', 'sending')`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			// Lost the guard: unknown, already cancelled (idempotent, also when a concurrent cancel won and
			// committed first: the UPDATE above waited for its row lock) or in a status that cannot be cancelled.
			var status string
			if err := tx.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, id).Scan(&status); err != nil {
				return notFound(err)
			}
			if status != StatusCancelled {
				return invalidState(status, "cancel")
			}
			return nil
		}
		rows, err := tx.Query(ctx, `UPDATE messages SET status = 'cancelled', lease_until = NULL, updated_at = now()
			WHERE campaign_id = $1 AND status IN ('queued', 'retry') RETURNING channel`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ch string
			if err := rows.Scan(&ch); err != nil {
				return err
			}
			byChannel[ch]++
			n++
		}
		return rows.Err()
	})
	if err != nil {
		return CancelResult{}, err
	}
	// Counted after the commit, in the API process that performed the cancel (a rolled-back cancel counts nothing).
	if s.Metrics != nil {
		for ch, k := range byChannel {
			s.Metrics.Messages.WithLabelValues(ch, StatusCancelled).Add(float64(k))
		}
	}
	c, err := s.Get(ctx, id)
	if err != nil {
		return CancelResult{}, err
	}
	if n > 0 && s.Log != nil {
		s.Log.Info("campaign cancelled", "campaign_id", id, "cancelled", n)
	}
	return CancelResult{Campaign: c, Cancelled: n}, nil
}

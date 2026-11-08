// Package campaign is the glue between the API, the LLM service, segment counting and the queue: campaign
// state machine, segment/content versions and the approve transaction (KICKOFF §5.4, §7.1).
// Owned by the orchestrator.
//
// Phase 0 skeleton: statuses and the guarded-transition helper; the service is built during W1.
package campaign

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Campaign statuses (docs/contracts/api.md §3).
const (
	StatusDraft                 = "draft"
	StatusSegmented             = "segmented"
	StatusContentReady          = "content_ready"
	StatusSending               = "sending"
	StatusCompleted             = "completed"
	StatusCompletedWithFailures = "completed_with_failures"
)

// ErrInvalidState is returned when a guarded transition updated no row (→ HTTP 409).
var ErrInvalidState = errors.New("campaign: invalid state for this action")

// Execer is satisfied by *pgxpool.Pool and pgx.Tx.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Transition moves a campaign from one of the expected statuses to next with a guarded UPDATE, so two
// concurrent requests can never both win.
func Transition(ctx context.Context, db Execer, id string, expected []string, next string) error {
	tag, err := db.Exec(ctx, `UPDATE campaigns SET status = $1, updated_at = now() WHERE id = $2 AND status = ANY($3)`,
		next, id, expected)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidState
	}
	return nil
}

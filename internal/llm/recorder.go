package llm

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is satisfied by *pgxpool.Pool and pgx.Tx.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PGRecorder writes one llm_calls row per call.
type PGRecorder struct{ db Execer }

// NewPGRecorder returns a recorder on db.
func NewPGRecorder(db Execer) *PGRecorder { return &PGRecorder{db: db} }

// RecordCall inserts the row. An empty CampaignID is stored as NULL (e.g. eval runs).
func (r *PGRecorder) RecordCall(ctx context.Context, c Call) error {
	var stop *string
	if c.StopReason != "" {
		s := string(c.StopReason)
		stop = &s
	}
	var errText *string
	if c.Error != "" {
		errText = &c.Error
	}
	_, err := r.db.Exec(ctx, `INSERT INTO llm_calls (campaign_id, purpose, provider, model, prompt_version,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, latency_ms, stop_reason, outcome, error)
		VALUES (NULLIF($1, '')::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		c.CampaignID, string(c.Purpose), c.Provider, c.Model, c.PromptVersion,
		c.Usage.InputTokens, c.Usage.OutputTokens, c.Usage.CacheReadTokens, c.Usage.CacheWriteTokens,
		c.CostUSD, c.Latency.Milliseconds(), stop, c.Outcome, errText)
	if err != nil {
		return fmt.Errorf("llm: record call: %w", err)
	}
	return nil
}

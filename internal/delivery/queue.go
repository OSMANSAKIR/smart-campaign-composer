package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Queue is the Postgres outbox for one channel: the `messages` table is the queue (KICKOFF §7, N-PLAN-010).
// All time arithmetic uses the database clock (now()), so replicas agree on leases and due times.
type Queue struct {
	Pool    *pgxpool.Pool
	Channel Channel
	Lease   time.Duration
}

// Claimed is a message this dispatcher now holds a lease on, joined with what rendering needs.
type Claimed struct {
	ID             string
	CampaignID     string
	UserID         int64
	ContentVersion int
	Attempts       int // value after the claim: the number of this try
	Throttled      int
	Skipped        bool // consent was revoked after enqueue: already moved to skipped_no_consent
	Recipient      Recipient
}

// Claim takes up to n due messages. One statement does it all: FOR UPDATE SKIP LOCKED picks rows no other
// claimer holds (R-SND-3 layer 3), the join re-checks consent at send time (R-AUD-2), and the update moves
// consented rows to `sending` with a lease while unconsented rows go straight to skipped_no_consent.
// Only messages of campaigns in `sending` are due, so a cancelled campaign stops draining.
func (q *Queue) Claim(ctx context.Context, n int) ([]Claimed, error) {
	consent := "u.sms_consent" // the column comes from the Channel constant, never from input
	if q.Channel == Email {
		consent = "u.email_consent"
	}
	rows, err := q.Pool.Query(ctx, fmt.Sprintf(`
		UPDATE messages m SET
			status      = CASE WHEN %[1]s THEN 'sending' ELSE 'skipped_no_consent' END,
			lease_until = CASE WHEN %[1]s THEN now() + make_interval(secs => $3) END,
			attempts    = CASE WHEN %[1]s THEN m.attempts + 1 ELSE m.attempts END,
			updated_at  = now()
		FROM users u, campaigns c
		WHERE m.id IN (
			SELECT pm.id FROM messages pm JOIN campaigns pc ON pc.id = pm.campaign_id
			WHERE pm.channel = $1 AND pm.status IN ('queued', 'retry') AND pm.next_attempt_at <= now()
			  AND pc.status = 'sending'
			ORDER BY pm.next_attempt_at, pm.created_at
			LIMIT $2
			FOR UPDATE OF pm SKIP LOCKED)
		  AND u.id = m.user_id AND c.id = m.campaign_id
		RETURNING m.id::text, m.campaign_id::text, m.user_id, m.content_version, m.attempts, m.throttled_count,
		          m.status = 'skipped_no_consent', u.email, u.phone, u.first_name, u.city,
		          COALESCE(c.language, u.locale)`, consent),
		string(q.Channel), n, q.Lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Claimed
	for rows.Next() {
		var c Claimed
		if err := rows.Scan(&c.ID, &c.CampaignID, &c.UserID, &c.ContentVersion, &c.Attempts, &c.Throttled, &c.Skipped,
			&c.Recipient.Email, &c.Recipient.Phone, &c.Recipient.FirstName, &c.Recipient.City, &c.Recipient.Locale); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReclaimExpired makes messages whose lease ran out claimable again (a worker died mid-send). The lost try
// already counted as an attempt; a message that used up all attempts this way fails instead of looping
// forever (a poison message that crashes the worker). The retry keeps the same Idempotency-Key, so a send
// that did reach the provider is deduplicated there.
func (q *Queue) ReclaimExpired(ctx context.Context, maxAttempts int) (requeued, failed int, err error) {
	rows, err := q.Pool.Query(ctx, `
		UPDATE messages SET
			status          = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'retry' END,
			last_error      = CASE WHEN attempts >= $2 THEN 'lease_expired' ELSE last_error END,
			lease_until     = NULL,
			next_attempt_at = now(),
			updated_at      = now()
		WHERE channel = $1 AND status = 'sending' AND lease_until < now()
		RETURNING status = 'failed'`, string(q.Channel), maxAttempts)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var f bool
		if err := rows.Scan(&f); err != nil {
			return 0, 0, err
		}
		if f {
			failed++
		} else {
			requeued++
		}
	}
	return requeued, failed, rows.Err()
}

// Outcome is the result of one try, written together with a delivery_attempts row.
type Outcome struct {
	Status            string        // sent | retry | failed
	AttemptDelta      int           // -1 when the try must not count (throttled, config)
	ThrottleDelta     int           // 1 for a throttled try
	Delay             time.Duration // next_attempt_at = now() + Delay (retry only)
	LastError         string
	ProviderMessageID string
	HTTPStatus        *int // nil for timeouts, connection errors and sends that never happened
	Class             Class
	Latency           time.Duration
}

// Finish records the result of a claimed try. The guard `status = 'sending' AND attempts = claimed` makes a
// late writer a no-op: if the lease expired and another dispatcher reclaimed the row, its result wins.
// It reports whether this call changed the row.
func (q *Queue) Finish(ctx context.Context, c Claimed, o Outcome) (bool, error) {
	tag, err := q.Pool.Exec(ctx, `
		WITH upd AS (
			UPDATE messages SET
				status              = $3,
				attempts            = attempts + $4,
				throttled_count     = throttled_count + $5,
				next_attempt_at     = CASE WHEN $3 = 'retry' THEN now() + make_interval(secs => $6) ELSE next_attempt_at END,
				lease_until         = NULL,
				last_error          = NULLIF($7, ''),
				provider_message_id = COALESCE(NULLIF($8, ''), provider_message_id),
				sent_at             = CASE WHEN $3 = 'sent' THEN now() ELSE sent_at END,
				updated_at          = now()
			WHERE id = $1 AND status = 'sending' AND attempts = $2
			RETURNING id)
		INSERT INTO delivery_attempts (message_id, attempt, http_status, error_class, latency_ms)
		SELECT id, $2, $9, $10, $11 FROM upd`,
		c.ID, c.Attempts, o.Status, o.AttemptDelta, o.ThrottleDelta, o.Delay.Seconds(), o.LastError,
		o.ProviderMessageID, o.HTTPStatus, string(o.Class), int(o.Latency.Milliseconds()))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Release hands an unsent claimed message back without counting a try: used on shutdown, when the channel is
// paused, and when a transient database error prevented rendering.
func (q *Queue) Release(ctx context.Context, c Claimed, delay time.Duration) error {
	_, err := q.Pool.Exec(ctx, `
		UPDATE messages SET
			status          = CASE WHEN attempts - 1 > 0 OR throttled_count > 0 THEN 'retry' ELSE 'queued' END,
			attempts        = attempts - 1,
			lease_until     = NULL,
			next_attempt_at = now() + make_interval(secs => $3),
			updated_at      = now()
		WHERE id = $1 AND status = 'sending' AND attempts = $2`,
		c.ID, c.Attempts, delay.Seconds())
	return err
}

// Template loads one approved content version.
func (q *Queue) Template(ctx context.Context, campaignID string, version int) (Template, error) {
	var t Template
	err := q.Pool.QueryRow(ctx, `SELECT sms_text, email_subject, email_body FROM campaign_content
		WHERE campaign_id = $1 AND version = $2`, campaignID, version).Scan(&t.SMSText, &t.EmailSubject, &t.EmailBody)
	return t, err
}

// Depth returns the number of messages per status for the queue_depth gauge.
func (q *Queue) Depth(ctx context.Context) (map[string]int, error) {
	rows, err := q.Pool.Query(ctx, `SELECT status, count(*) FROM messages
		WHERE channel = $1 AND status IN ('queued', 'retry', 'sending') GROUP BY status`, string(q.Channel))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

// Heartbeat upserts this dispatcher's row in worker_heartbeats (read by GET /api/system/status).
func (q *Queue) Heartbeat(ctx context.Context, workerID string, holdsLock bool, pausedUntil time.Time) error {
	var paused *time.Time
	if !pausedUntil.IsZero() {
		paused = &pausedUntil
	}
	_, err := q.Pool.Exec(ctx, `
		INSERT INTO worker_heartbeats (worker_id, channel, holds_lock, paused_until, at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (worker_id, channel) DO UPDATE SET holds_lock = $3, paused_until = $4, at = now()`,
		workerID, string(q.Channel), holdsLock, paused)
	return err
}

// DropHeartbeat removes this dispatcher's row on clean shutdown, so the status page does not show a ghost.
func (q *Queue) DropHeartbeat(ctx context.Context, workerID string) error {
	_, err := q.Pool.Exec(ctx, `DELETE FROM worker_heartbeats WHERE worker_id = $1 AND channel = $2`, workerID, string(q.Channel))
	return err
}

// Reconcile moves `sending` campaigns whose messages are all terminal to completed or
// completed_with_failures (docs/contracts/api.md §3). The guarded UPDATE is idempotent, so every worker
// replica may run it. It returns the ids it completed.
func Reconcile(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		UPDATE campaigns c SET
			status = CASE WHEN EXISTS (SELECT 1 FROM messages f WHERE f.campaign_id = c.id AND f.status = 'failed')
			              THEN 'completed_with_failures' ELSE 'completed' END,
			completed_at = now(), updated_at = now()
		WHERE c.status = 'sending'
		  AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.campaign_id = c.id
		                  AND m.status IN ('queued', 'retry', 'sending'))
		RETURNING c.id::text`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

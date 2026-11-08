package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/content"
	"smartcampaign/internal/segment"
)

// MaxIdempotencyKeyLen bounds the Idempotency-Key header (docs/contracts/api.md §6).
const MaxIdempotencyKeyLen = 200

// Approve freezes the latest content version and enqueues one message per consented recipient and selected
// channel, all in one transaction (R-FLOW-3, R-SND-1, R-SND-3, R-AUD-2). A repeated key replays the stored
// response; concurrent approves cannot both win because the campaign row is locked and the transition is
// guarded.
func (s *Service) Approve(ctx context.Context, id, key string, channels []string, contentVersion int) (ApproveResult, error) {
	switch {
	case key == "":
		return ApproveResult{}, errorf("idempotency_key_required", "Send an Idempotency-Key header with every approve.")
	case len(key) > MaxIdempotencyKeyLen:
		return ApproveResult{}, errorf("bad_request", fmt.Sprintf("Idempotency-Key is longer than %d characters.", MaxIdempotencyKeyLen))
	}
	chans, err := normalizeChannels(channels)
	if err != nil {
		return ApproveResult{}, err
	}
	if _, _, err := s.load(ctx, s.Pool, id, false); err != nil {
		return ApproveResult{}, err
	}

	var out ApproveResult
	replayed := false
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var (
			status    string
			storedKey *string
			stored    []byte
		)
		// NOWAIT: a second approve does not queue behind the first; it gets 409 approve_in_progress.
		err := tx.QueryRow(ctx, `SELECT status, approve_idempotency_key, approve_response FROM campaigns WHERE id = $1 FOR UPDATE NOWAIT`, id).
			Scan(&status, &storedKey, &stored)
		if err != nil {
			if isPg(err, "55P03") {
				return errorf("approve_in_progress", "Another approve for this campaign is in progress.")
			}
			return notFound(err)
		}
		if storedKey != nil {
			if *storedKey == key && stored != nil {
				replayed = true
				return json.Unmarshal(stored, &out)
			}
			return errorf("already_approved", "This campaign was already approved with a different Idempotency-Key.")
		}
		if status != StatusContentReady {
			return invalidState(status, "approve")
		}

		c, v, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if c.Content == nil || c.Content.Version != contentVersion {
			latest := 0
			if c.Content != nil {
				latest = c.Content.Version
			}
			return errorf("stale_content_version", fmt.Sprintf("Version %d is not the latest (%d).", contentVersion, latest),
				Issue{Path: "content_version", Code: "stale", Message: fmt.Sprintf("latest is %d", latest)})
		}
		// Re-validate now instead of trusting the stored report: the audience (and its worst case) can change.
		wc, err := s.worstCase(ctx, tx, v)
		if err != nil {
			return err
		}
		cv := c.Content
		report := content.Validate(content.Content{SMSText: cv.SMSText, EmailSubject: cv.EmailSubject, EmailBody: cv.EmailBody},
			constraints(c.Language, wc))
		var invalid []Issue
		for _, ch := range chans {
			if !report.ValidFor[ch] {
				invalid = append(invalid, channelIssues(ch, report.Errors)...)
				if len(invalid) == 0 {
					invalid = append(invalid, Issue{Path: ch, Code: "invalid", Message: "Content is not valid for this channel."})
				}
			}
		}
		if len(invalid) > 0 {
			return errorf("content_invalid", "The content is not valid for every selected channel.", invalid...)
		}

		where, args, err := segment.Compile(*v, 4) // $1..$3 are campaign, channel, version
		if err != nil {
			return err
		}
		if strings.TrimSpace(where) == "" {
			where = "TRUE"
		}
		out.Enqueued = map[string]int{"sms": 0, "email": 0}
		for _, ch := range chans {
			// Consent is enforced here in SQL, never by the DSL (KICKOFF §6.3); the worker checks it again at claim.
			var n int
			q := fmt.Sprintf(`WITH ins AS (
					INSERT INTO messages (campaign_id, user_id, channel, content_version)
					SELECT $1, u.id, $2, $3 FROM users u WHERE (%s) AND %s
					ON CONFLICT (campaign_id, user_id, channel) DO NOTHING
					RETURNING 1)
				SELECT count(*) FROM ins`, where, consentColumn[ch])
			if err := tx.QueryRow(ctx, q, append([]any{id, ch, contentVersion}, args...)...).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return errorf("empty_audience", fmt.Sprintf("No user in this segment has %s consent.", ch),
					Issue{Path: "channels", Code: "empty_audience", Message: ch})
			}
			out.Enqueued[ch] = n
		}
		tag, err := tx.Exec(ctx, `UPDATE campaigns SET status = 'sending', channels = $2, approved_content_version = $3,
				approve_idempotency_key = $4, approved_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'content_ready'`, id, chans, contentVersion, key)
		if err != nil {
			if isPg(err, "23505") {
				return errorf("bad_request", "This Idempotency-Key was already used for another campaign.")
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			return errorf("invalid_state", "The campaign changed during approve.")
		}
		approved, _, err := s.load(ctx, tx, id, false)
		if err != nil {
			return err
		}
		out.Campaign = approved
		_, err = tx.Exec(ctx, `UPDATE campaigns SET approve_response = $2 WHERE id = $1`, id, mustJSON(out))
		return err
	})
	if err != nil {
		return ApproveResult{}, err
	}
	if !replayed && s.Metrics != nil {
		s.Metrics.CampaignsApproved.Inc()
		for ch, n := range out.Enqueued {
			s.Metrics.MessagesEnqueued.WithLabelValues(ch).Add(float64(n))
		}
	}
	if !replayed && s.Log != nil {
		s.Log.Info("campaign approved", "campaign_id", id, "enqueued_sms", out.Enqueued["sms"], "enqueued_email", out.Enqueued["email"])
	}
	return out, nil
}

// normalizeChannels checks the selection and returns it in catalog order without duplicates.
func normalizeChannels(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, errorf("bad_request", "Select at least one channel.", Issue{Path: "channels", Code: "required", Message: "sms, email"})
	}
	for _, ch := range in {
		if !slices.Contains(catalog.Channels, ch) {
			return nil, errorf("bad_request", "Unknown channel.", Issue{Path: "channels", Code: "invalid", Message: ch})
		}
	}
	var out []string
	for _, ch := range catalog.Channels {
		if slices.Contains(in, ch) {
			out = append(out, ch)
		}
	}
	return out, nil
}

// channelIssues keeps the errors that concern one channel's fields (sms_text, email_subject, email_body).
func channelIssues(ch string, errs []content.Issue) []Issue {
	var out []Issue
	for _, e := range errs {
		if strings.HasPrefix(e.Path, ch+"_") || e.Path == "" {
			out = append(out, Issue{Path: e.Path, Code: e.Code, Message: e.Message})
		}
	}
	return out
}

func isPg(err error, code string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == code
}

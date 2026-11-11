package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/content"
	"smartcampaign/internal/segment"
)

// MaxIdempotencyKeyLen bounds the Idempotency-Key header (docs/contracts/api.md §6).
const MaxIdempotencyKeyLen = 200

// enqueueLockID serializes the bulk INSERT … SELECT of concurrent approves (transaction-scoped advisory lock).
// Measured on the compose Postgres (stress S3, need.md N-ORC-013): ten all-user approves enqueueing in
// parallel took 14–55 s each under buffer and spinlock contention, while one at a time all ten finished in
// 3.5 s. Approves of different campaigns therefore queue for a fraction of a second instead of all stalling.
const enqueueLockID = 7_261_108_002

// Send-at window (docs/contracts/api.md §9.6): the contract says "1 minute .. 7 days"; the minimum is 10 s
// lower so clock skew between browser and server and the request latency cannot reject a valid "in 1 minute".
const (
	MinSendAtLead = 50 * time.Second
	MaxSendAtLead = 7 * 24 * time.Hour
)

// variantsOf lists the variants each send mode renders, in order.
var variantsOf = map[string][]string{"a": {"a"}, "b": {"b"}, "ab": {"a", "b"}}

// ApproveOptions are the phase-2 inputs of approve (docs/contracts/api.md §9.3, §9.6).
type ApproveOptions struct {
	Variant string     // "a" (default), "b" or "ab" (deterministic 50/50 split)
	SendAt  *time.Time // schedule the send; nil sends now
	// LanguageMode is "campaign" (default: everyone gets the campaign-language copy) or "recipient" (everyone
	// gets the copy in their own locale; every locale among the recipients needs a valid, non-stale copy).
	LanguageMode string
}

// Approve freezes the latest content version and enqueues one message per consented recipient and selected
// channel, all in one transaction (R-FLOW-3, R-SND-1, R-SND-3, R-AUD-2). A repeated key replays the stored
// response; concurrent approves cannot both win because the campaign row is locked and the transition is
// guarded. It sends variant A immediately; ApproveWith takes the phase-2 options.
func (s *Service) Approve(ctx context.Context, id, key string, channels []string, contentVersion int) (ApproveResult, error) {
	return s.ApproveWith(ctx, id, key, channels, contentVersion, ApproveOptions{})
}

// ApproveWith is Approve with a variant choice (F1, B-2) and an optional send time (F5, B-1).
func (s *Service) ApproveWith(ctx context.Context, id, key string, channels []string, contentVersion int, opt ApproveOptions) (ApproveResult, error) {
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
	if opt.Variant == "" {
		opt.Variant = "a"
	}
	toSend, ok := variantsOf[opt.Variant]
	if !ok {
		return ApproveResult{}, errorf("bad_request", "Unknown variant.", Issue{Path: "variant", Code: "invalid", Message: "Use a, b or ab."})
	}
	if opt.LanguageMode == "" {
		opt.LanguageMode = "campaign"
	}
	if opt.LanguageMode != "campaign" && opt.LanguageMode != "recipient" {
		return ApproveResult{}, errorf("bad_request", "Unknown language_mode.", Issue{Path: "language_mode", Code: "invalid", Message: "Use campaign or recipient."})
	}
	var sendAt *time.Time
	if opt.SendAt != nil {
		u := opt.SendAt.UTC()
		sendAt = &u
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
		if sendAt != nil {
			// The database clock, like the scheduler that will activate the campaign (docs/contracts/api.md §9.6).
			var now time.Time
			if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
				return err
			}
			if sendAt.Before(now.Add(MinSendAtLead)) || sendAt.After(now.Add(MaxSendAtLead)) {
				return errorf("invalid_send_at", "send_at must be between 1 minute and 7 days from now.",
					Issue{Path: "send_at", Code: "out_of_range", Message: "now + 1 minute .. now + 7 days"})
			}
		}

		c, v, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		// The latest version is read here, not from c.Content: a version is one row per variant.
		var latest int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM campaign_content WHERE campaign_id = $1`, id).Scan(&latest); err != nil {
			return err
		}
		if latest == 0 || latest != contentVersion {
			return errorf("stale_content_version", fmt.Sprintf("Version %d is not the latest (%d).", contentVersion, latest),
				Issue{Path: "content_version", Code: "stale", Message: fmt.Sprintf("latest is %d", latest)})
		}
		texts, err := variantTexts(ctx, tx, id, contentVersion)
		if err != nil {
			return err
		}
		for _, vr := range toSend {
			if _, ok := texts[vr]; !ok {
				return errorf("variant_missing", fmt.Sprintf("Content version %d has no variant %s.", contentVersion, vr),
					Issue{Path: "variant", Code: "variant_missing", Message: vr})
			}
		}
		// Re-validate now instead of trusting the stored report: the audience (and its worst case) can change.
		// Every variant that will be sent must be valid for every selected channel; variant A being valid says
		// nothing about B (F1).
		wc, err := s.worstCase(ctx, tx, v)
		if err != nil {
			return err
		}
		var invalid []Issue
		for _, vr := range toSend {
			report := content.Validate(texts[vr], constraints(c.Language, wc))
			prefix := "variants." + vr + "."
			for _, ch := range chans {
				if report.ValidFor[ch] {
					continue
				}
				before := len(invalid)
				for _, is := range channelIssues(ch, report.Errors) {
					is.Path = prefix + is.Path
					invalid = append(invalid, is)
				}
				if len(invalid) == before {
					invalid = append(invalid, Issue{Path: prefix + ch, Code: "invalid", Message: "Content is not valid for this channel."})
				}
			}
		}
		if len(invalid) > 0 {
			return errorf("content_invalid", "The content is not valid for every selected channel.", invalid...)
		}

		if opt.LanguageMode == "recipient" {
			// B-3: nothing is enqueued for a locale that has no usable copy (api.md §9.10).
			missing, err := s.localizationIssues(ctx, tx, c, v, chans, toSend, wc)
			if err != nil {
				return err
			}
			if len(missing) > 0 {
				return errorf("missing_localization", "Each recipient can only get their own language when every language among the recipients has a valid, current copy.", missing...)
			}
		}

		where, args, err := segment.Compile(*v, 5) // $1..$4 are campaign, channel, version, variant mode
		if err != nil {
			return err
		}
		if strings.TrimSpace(where) == "" {
			where = "TRUE"
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, enqueueLockID); err != nil {
			return err
		}
		out.Enqueued = map[string]int{"sms": 0, "email": 0}
		for _, ch := range chans {
			// Consent is enforced here in SQL, never by the DSL (KICKOFF §6.3); the worker checks it again at claim.
			// The split of `ab` is a pure function of (campaign, user), the same for every channel, so a user
			// gets one variant on SMS and email and anyone can recompute it from the two ids (§9.3). md5 is only
			// a uniform, stable hash here, not a security primitive.
			var n int
			q := fmt.Sprintf(`WITH ins AS (
					INSERT INTO messages (campaign_id, user_id, channel, content_version, variant, locale)
					SELECT $1::uuid, u.id, $2, $3,
					       CASE WHEN $4::text = 'ab'
					            THEN CASE WHEN get_byte(decode(md5($1::uuid::text || ':' || u.id::text), 'hex'), 0) %% 2 = 0 THEN 'a' ELSE 'b' END
					            ELSE $4::text END,
					       u.locale
					FROM users u WHERE (%s) AND %s
					ON CONFLICT (campaign_id, user_id, channel) DO NOTHING
					RETURNING 1)
				SELECT count(*) FROM ins`, where, consentColumn[ch])
			if err := tx.QueryRow(ctx, q, append([]any{id, ch, contentVersion, opt.Variant}, args...)...).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return errorf("empty_audience", fmt.Sprintf("No user in this segment has %s consent.", ch),
					Issue{Path: "channels", Code: "empty_audience", Message: ch})
			}
			out.Enqueued[ch] = n
		}
		// With send_at the campaign waits in `scheduled` (the messages are enqueued now: the audience is frozen
		// at approve); the worker's scheduler moves it to `sending` when the database clock passes scheduled_at.
		tag, err := tx.Exec(ctx, `UPDATE campaigns SET
				status = CASE WHEN $5::timestamptz IS NULL THEN 'sending' ELSE 'scheduled' END,
				channels = $2, approved_content_version = $3, approve_idempotency_key = $4,
				scheduled_at = $5, send_variant = $6, language_mode = $7, approved_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'content_ready'`, id, chans, contentVersion, key, sendAt, opt.Variant, opt.LanguageMode)
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
		s.Log.Info("campaign approved", "campaign_id", id, "variant", opt.Variant, "language_mode", opt.LanguageMode, "scheduled", sendAt != nil,
			"enqueued_sms", out.Enqueued["sms"], "enqueued_email", out.Enqueued["email"])
	}
	return out, nil
}

// localizationIssues lists what keeps language_mode 'recipient' from being approved: for every selected
// channel, every locale among the consented recipients of that channel other than the campaign's own needs,
// for every variant that will be sent, a localized copy that exists (missing), is not stale (stale: the
// primary was edited after the adaptation) and is valid for that channel under the locale's constraints
// (invalid). The campaign-language copy is validated by the caller like in 'campaign' mode.
func (s *Service) localizationIssues(ctx context.Context, tx pgx.Tx, c Campaign, v *segment.ValidRule, chans, toSend []string, wc map[string]content.Vars) ([]Issue, error) {
	where, args, err := segment.Compile(*v, 1)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(where) == "" {
		where = "TRUE"
	}
	var out []Issue
	seen := map[string]bool{}
	add := func(is Issue) {
		if k := is.Path + "|" + is.Code; !seen[k] {
			seen[k] = true
			out = append(out, is)
		}
	}
	for _, ch := range chans {
		rows, err := tx.Query(ctx, `SELECT DISTINCT u.locale FROM users u WHERE (`+where+`) AND `+consentColumn[ch], args...)
		if err != nil {
			return nil, err
		}
		locales, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		slices.Sort(locales)
		for _, loc := range locales {
			if loc == c.Language {
				continue
			}
			for _, vr := range toSend {
				path := "variants." + vr + ".localized." + loc
				lc := c.Content.Variants.Get(vr).Localized[loc]
				switch {
				case lc == nil:
					add(Issue{Path: path, Code: "missing", Message: fmt.Sprintf("Recipients read %s on %s, but variant %s has no %s copy.", loc, ch, vr, loc)})
				case lc.Stale:
					add(Issue{Path: path, Code: "stale", Message: fmt.Sprintf("The %s copy of variant %s was adapted from an earlier primary copy; re-adapt it.", loc, vr)})
				default:
					report := content.Validate(lc.Content(), constraints(loc, wc))
					if report.ValidFor[ch] {
						continue
					}
					before := len(out)
					for _, is := range channelIssues(ch, report.Errors) {
						is.Path = path + "." + is.Path
						add(is)
					}
					if len(out) == before {
						add(Issue{Path: path + "." + ch, Code: "invalid", Message: "The " + loc + " copy is not valid for this channel."})
					}
				}
			}
		}
	}
	return out, nil
}

// variantTexts reads the primary rows of one content version, keyed by variant (the localized rows of B-3 are
// read through Campaign.Content). It queries campaign_content itself because a version is one row per variant
// and locale (migrations 20261110230000, 20261111120000).
func variantTexts(ctx context.Context, db DB, id string, version int) (map[string]content.Content, error) {
	rows, err := db.Query(ctx, `SELECT variant, sms_text, email_subject, email_body FROM campaign_content
		WHERE campaign_id = $1 AND version = $2 AND adapted_from IS NULL`, id, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]content.Content{}
	for rows.Next() {
		var v string
		var c content.Content
		if err := rows.Scan(&v, &c.SMSText, &c.EmailSubject, &c.EmailBody); err != nil {
			return nil, err
		}
		out[v] = c
	}
	return out, rows.Err()
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

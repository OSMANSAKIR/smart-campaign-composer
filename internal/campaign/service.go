package campaign

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/segment"
)

// DB is satisfied by *pgxpool.Pool and pgx.Tx.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Service implements the campaign use cases behind the HTTP API.
type Service struct {
	Pool    *pgxpool.Pool
	LLM     *llm.Service
	Metrics *metrics.Metrics
	Log     *slog.Logger
}

// ErrNotFound is returned for unknown or malformed campaign ids.
var ErrNotFound = errorf("not_found", "Campaign not found.")

// Statuses in which the segment may be (re)generated or edited.
var segmentEditable = []string{StatusDraft, StatusSegmented, StatusContentReady}

// Statuses in which content may be generated or saved.
var contentEditable = []string{StatusSegmented, StatusContentReady}

// consentColumn whitelists the per-channel consent column; channel names never reach SQL as text.
var consentColumn = map[string]string{"sms": "u.sms_consent", "email": "u.email_consent"}

// Create stores a new draft campaign.
func (s *Service) Create(ctx context.Context, brief string) (Campaign, error) {
	brief = strings.TrimSpace(brief)
	switch n := utf8.RuneCountInString(brief); {
	case !utf8.ValidString(brief) || strings.ContainsFunc(brief, disallowedInBrief):
		// Postgres text cannot hold NUL or invalid UTF-8; reject here instead of failing the INSERT (RT-8).
		return Campaign{}, errorf("validation_failed", "The brief contains invalid or control characters.",
			Issue{Path: "brief", Code: "control_character", Message: "Remove invisible control characters."})
	case n == 0:
		return Campaign{}, errorf("validation_failed", "The brief is empty.",
			Issue{Path: "brief", Code: "required", Message: "Write a campaign brief."})
	case n > catalog.MaxBriefRunes:
		return Campaign{}, errorf("validation_failed", "The brief is too long.",
			Issue{Path: "brief", Code: "too_long", Message: fmt.Sprintf("%d characters; the limit is %d.", n, catalog.MaxBriefRunes)})
	}
	var id string
	if err := s.Pool.QueryRow(ctx, `INSERT INTO campaigns (brief) VALUES ($1) RETURNING id::text`, brief).Scan(&id); err != nil {
		return Campaign{}, err
	}
	return s.Get(ctx, id)
}

// disallowedInBrief reports control characters other than line breaks and tabs.
func disallowedInBrief(r rune) bool {
	return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
}

// Get returns the campaign with its latest content and, once approved, a progress snapshot.
func (s *Service) Get(ctx context.Context, id string) (Campaign, error) {
	c, _, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return Campaign{}, err
	}
	if c.ApprovedAt != nil {
		p, err := s.Progress(ctx, id)
		if err != nil {
			return Campaign{}, err
		}
		c.Progress = &p
	}
	return c, nil
}

// List returns campaigns newest first. The cursor is opaque to clients.
func (s *Service) List(ctx context.Context, limit int, cursor string) ([]Summary, string, error) {
	args := []any{limit + 1}
	where := ""
	if cursor != "" {
		at, id, ok := decodeCursor(cursor)
		if !ok {
			return nil, "", errorf("bad_request", "Invalid cursor.", Issue{Path: "cursor", Code: "invalid", Message: "Use next_cursor from a previous page."})
		}
		where = `WHERE (created_at, id) < ($2, $3)`
		args = append(args, at, id)
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text, brief, status, coalesce(language, 'tr'), channels,
			(audience->>'total')::int, created_at, approved_at, completed_at
		FROM campaigns `+where+` ORDER BY created_at DESC, id DESC LIMIT $1`, args...)
	if err != nil {
		return nil, "", err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Summary, error) {
		var x Summary
		err := r.Scan(&x.ID, &x.Brief, &x.Status, &x.Language, &x.Channels, &x.AudienceTotal, &x.CreatedAt, &x.ApprovedAt, &x.CompletedAt)
		return x, err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeCursor(last.CreatedAt, last.ID)
	}
	return items, next, nil
}

// GenerateSegment asks the model for a segment, validates and counts it, and stores it (status → segmented).
func (s *Service) GenerateSegment(ctx context.Context, id string) (Campaign, llm.Meta, error) {
	c, _, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return Campaign{}, llm.Meta{}, err
	}
	if !slices.Contains(segmentEditable, c.Status) {
		return Campaign{}, llm.Meta{}, invalidState(c.Status, "generate a segment")
	}
	res, err := s.LLM.ProposeSegment(ctx, id, c.Brief)
	if err != nil {
		return Campaign{}, llm.Meta{}, fromLLM(err)
	}
	p := res.Proposal
	meta := SegmentMeta{
		Source: "llm", UnsupportedCriteria: p.UnsupportedCriteria, Assumptions: p.Assumptions,
		Explanation: p.Explanation, ChannelsRequested: p.ChannelsRequested, Language: p.Language, Warnings: res.Warnings,
	}
	language := p.Language
	if !slices.Contains(catalog.Locales, language) {
		language = c.Language
	}
	if err := s.storeSegment(ctx, id, res.Rule, meta, language); err != nil {
		return Campaign{}, llm.Meta{}, err
	}
	out, err := s.Get(ctx, id)
	return out, res.Meta, err
}

// PutSegment replaces the segment with a user-edited rule, checked by the same validator as model output.
func (s *Service) PutSegment(ctx context.Context, id string, rule segment.Rule) (Campaign, error) {
	c, _, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return Campaign{}, err
	}
	if !slices.Contains(segmentEditable, c.Status) {
		return Campaign{}, invalidState(c.Status, "edit the segment")
	}
	v, errs, warns := segment.Validate(rule)
	if len(errs) > 0 {
		return Campaign{}, errorf("validation_failed", "The segment rule is invalid.", errs...)
	}
	now := time.Now().UTC()
	meta := SegmentMeta{Source: "edited", ChannelsRequested: "both", Language: c.Language, Warnings: warns, EditedAt: &now}
	if c.Segment != nil { // keep what the model said about the brief; only the rule changed
		prev := c.Segment.SegmentMeta
		meta.UnsupportedCriteria, meta.Assumptions, meta.Explanation = prev.UnsupportedCriteria, prev.Assumptions, prev.Explanation
		meta.ChannelsRequested = prev.ChannelsRequested
		// Audit trail of a human edit (api.md §9.2): Claude's rule survives any number of later edits.
		meta.OriginalRule = prev.OriginalRule
		if prev.Source == "llm" {
			orig := c.Segment.Rule
			meta.OriginalRule = &orig
		}
	}
	if err := s.storeSegment(ctx, id, v, meta, c.Language); err != nil {
		return Campaign{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) storeSegment(ctx context.Context, id string, v segment.ValidRule, meta SegmentMeta, language string) error {
	aud, err := segment.Count(ctx, s.Pool, v)
	if err != nil {
		return fmt.Errorf("count audience: %w", err)
	}
	meta.Rendered = map[string][]string{}
	for _, loc := range catalog.Locales {
		meta.Rendered[loc] = orEmpty(segment.Render(v, loc))
	}
	meta.UnsupportedCriteria, meta.Assumptions = orEmpty(meta.UnsupportedCriteria), orEmpty(meta.Assumptions)
	meta.Warnings = orEmpty(meta.Warnings)
	if aud.Total == 0 { // need.md N-ORC-004: allowed, but approve will refuse it
		meta.Warnings = append(meta.Warnings, Issue{Path: "segment", Code: "empty_audience",
			Message: "No user matches this segment; approve stays disabled."})
	}
	count := AudienceCount{Audience: aud, CountedAt: time.Now().UTC()}
	tag, err := s.Pool.Exec(ctx, `UPDATE campaigns SET segment_rule = $2, segment_version = segment_version + 1,
			segment_meta = $3, audience = $4, language = $5, status = 'segmented', updated_at = now()
		WHERE id = $1 AND status = ANY($6)`,
		id, mustJSON(v.Rule()), mustJSON(meta), mustJSON(count), language, segmentEditable)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 { // approved (or changed) while the model was thinking
		return errorf("invalid_state", "The campaign changed while the segment was being generated.")
	}
	return nil
}

// ContentInput checks that content can be generated now and assembles what the content prompt needs. The
// API calls it before opening the event stream, so 404/409 stay plain JSON errors.
func (s *Service) ContentInput(ctx context.Context, id string) (llm.ContentInput, error) {
	c, v, err := s.load(ctx, s.Pool, id, true)
	if err != nil {
		return llm.ContentInput{}, err
	}
	if !slices.Contains(contentEditable, c.Status) {
		return llm.ContentInput{}, invalidState(c.Status, "generate content")
	}
	wc, err := s.worstCase(ctx, s.Pool, v)
	if err != nil {
		return llm.ContentInput{}, err
	}
	return buildContentInput(id, c, wc), nil
}

// buildContentInput is what the content and refine prompts need about the campaign; c has a segment.
func buildContentInput(id string, c Campaign, wc map[string]content.Vars) llm.ContentInput {
	channels := catalog.Channels
	if ch := c.Segment.ChannelsRequested; ch == "sms" || ch == "email" {
		channels = []string{ch}
	}
	return llm.ContentInput{
		CampaignID: id, Brief: c.Brief, Language: c.Language, SegmentSummary: c.Segment.Rendered[c.Language],
		Audience: c.Segment.Audience.Audience, Channels: channels, WorstCaseByChannel: wc,
	}
}

// GenerateContent runs the streamed content generation and saves the two variants as a new version of `llm`
// rows (one transaction, both rows).
func (s *Service) GenerateContent(ctx context.Context, id string, in llm.ContentInput, onDelta func(llm.Delta), onRepair func(llm.RepairNotice)) (ContentVersion, llm.Meta, error) {
	res, err := s.LLM.ProposeContent(ctx, in, onDelta, onRepair)
	if err != nil {
		return ContentVersion{}, llm.Meta{}, fromLLM(err)
	}
	// The generation may outlive the client; saving must not depend on the request context.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	a := VariantInput{Label: res.A.Label, Source: "llm", Content: res.A.Content, Report: res.A.Report}
	b := VariantInput{Label: res.B.Label, Source: "llm", Content: res.B.Content, Report: res.B.Report}
	cv, err := s.saveVersion(saveCtx, id, a, &b, -1)
	return cv, res.Meta, err
}

// SaveContent stores a user-edited one-variant version (the v1 body of PUT …/content), even when invalid
// (the report says why). baseVersion must be the latest version (0 when there is none).
func (s *Service) SaveContent(ctx context.Context, id string, c content.Content, baseVersion int) (ContentVersion, error) {
	if baseVersion < 0 {
		return ContentVersion{}, errorf("bad_request", "base_version must be 0 or greater.")
	}
	camp, v, err := s.load(ctx, s.Pool, id, true)
	if err != nil {
		return ContentVersion{}, err
	}
	if !slices.Contains(contentEditable, camp.Status) {
		return ContentVersion{}, invalidState(camp.Status, "edit content")
	}
	wc, err := s.worstCase(ctx, s.Pool, v)
	if err != nil {
		return ContentVersion{}, err
	}
	report := content.Validate(c, constraints(camp.Language, wc))
	return s.saveVersion(ctx, id, VariantInput{Source: "edited", Content: c, Report: report}, nil, baseVersion)
}

// saveVersion appends a content version with one row per variant (b == nil: a one-variant version) in one
// transaction; baseVersion < 0 skips the staleness check (model output).
func (s *Service) saveVersion(ctx context.Context, id string, a VariantInput, b *VariantInput, baseVersion int) (ContentVersion, error) {
	var cv ContentVersion
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
			return notFound(err)
		}
		if !slices.Contains(contentEditable, status) {
			return invalidState(status, "save content")
		}
		var latest int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM campaign_content WHERE campaign_id = $1`, id).Scan(&latest); err != nil {
			return err
		}
		if baseVersion >= 0 && baseVersion != latest {
			return errorf("stale_content_version", fmt.Sprintf("Version %d is not the latest (%d); reload and edit again.", baseVersion, latest),
				Issue{Path: "base_version", Code: "stale", Message: fmt.Sprintf("latest is %d", latest)})
		}
		cv = ContentVersion{Version: latest + 1}
		rows := []struct {
			key string
			in  VariantInput
		}{{"a", a}}
		if b != nil {
			rows = append(rows, struct {
				key string
				in  VariantInput
			}{"b", *b})
		}
		for _, r := range rows {
			cvr := ContentVariant{Label: r.in.Label, Source: r.in.Source, Instruction: r.in.Instruction,
				SMSText: r.in.Content.SMSText, EmailSubject: r.in.Content.EmailSubject, EmailBody: r.in.Content.EmailBody,
				Validation: normalizeReport(r.in.Report)}
			if err := tx.QueryRow(ctx, `INSERT INTO campaign_content (campaign_id, version, variant, label, sms_text, email_subject,
					email_body, source, instruction, validation)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING created_at`,
				id, cv.Version, r.key, cvr.Label, cvr.SMSText, cvr.EmailSubject, cvr.EmailBody, cvr.Source, cvr.Instruction,
				mustJSON(cvr.Validation)).Scan(&cv.CreatedAt); err != nil {
				return err
			}
			if r.key == "a" {
				cv.Variants.A = cvr
			} else {
				cv.Variants.B = &cvr
			}
		}
		cv.mirrorA()
		_, err := tx.Exec(ctx, `UPDATE campaigns SET status = 'content_ready', updated_at = now() WHERE id = $1`, id)
		return err
	})
	return cv, err
}

// ValidateContent is the stateless validator behind POST /api/content/validate. The worst case comes from the
// campaign's audience when campaignID is set, else from all consented users.
func (s *Service) ValidateContent(ctx context.Context, c content.Content, campaignID, language string) (content.Report, error) {
	var v *segment.ValidRule
	if campaignID != "" {
		camp, rule, err := s.load(ctx, s.Pool, campaignID, true)
		if err != nil {
			return content.Report{}, err
		}
		v = rule
		if language == "" {
			language = camp.Language
		}
	}
	if language == "" {
		language = "tr"
	}
	if !slices.Contains(catalog.Locales, language) {
		return content.Report{}, errorf("bad_request", "Unsupported language.", Issue{Path: "language", Code: "invalid", Message: "Use tr or en."})
	}
	wc, err := s.worstCase(ctx, s.Pool, v)
	if err != nil {
		return content.Report{}, err
	}
	return normalizeReport(content.Validate(c, constraints(language, wc))), nil
}

// Messages lists a campaign's messages, ordered by id for a stable cursor.
func (s *Service) Messages(ctx context.Context, id, status, channel string, limit int, cursor string) ([]Message, string, error) {
	if !isUUID(id) {
		return nil, "", ErrNotFound
	}
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns WHERE id = $1)`, id).Scan(&exists); err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", ErrNotFound
	}
	if status != "" && !slices.Contains(MessageStatuses, status) {
		return nil, "", errorf("bad_request", "Unknown message status.", Issue{Path: "status", Code: "invalid", Message: strings.Join(MessageStatuses, ", ")})
	}
	if channel != "" && !slices.Contains(catalog.Channels, channel) {
		return nil, "", errorf("bad_request", "Unknown channel.", Issue{Path: "channel", Code: "invalid", Message: "Use sms or email."})
	}
	if cursor != "" {
		if !isUUID(cursor) {
			return nil, "", errorf("bad_request", "Invalid cursor.", Issue{Path: "cursor", Code: "invalid", Message: "Use next_cursor from a previous page."})
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text, user_id, channel, status, variant, attempts, last_error, sent_at, updated_at
		FROM messages
		WHERE campaign_id = $1 AND ($2 = '' OR status = $2) AND ($3 = '' OR channel = $3) AND ($4 = '' OR id > $4::uuid)
		ORDER BY id LIMIT $5`, id, status, channel, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) {
		var m Message
		err := r.Scan(&m.ID, &m.UserID, &m.Channel, &m.Status, &m.Variant, &m.Attempts, &m.LastError, &m.SentAt, &m.UpdatedAt)
		return m, err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}

// MessageStatuses are the message states of docs/contracts/api.md §3.
var MessageStatuses = []string{"queued", "sending", "sent", "retry", "failed", "skipped_no_consent", "cancelled"}

// load reads a campaign and its latest content version. With needRule it also re-validates the stored rule:
// a ValidRule only ever comes out of segment.Validate, even for rules we stored ourselves.
func (s *Service) load(ctx context.Context, db DB, id string, needRule bool) (Campaign, *segment.ValidRule, error) {
	if !isUUID(id) {
		return Campaign{}, nil, ErrNotFound
	}
	var (
		c                      Campaign
		ruleRaw, metaRaw, audR []byte
		segVersion             int
	)
	err := db.QueryRow(ctx, `SELECT id::text, brief, status, coalesce(language, 'tr'), segment_rule, segment_version,
			segment_meta, audience, channels, approved_content_version, send_variant, scheduled_at, cancelled_at,
			created_at, approved_at, completed_at
		FROM campaigns WHERE id = $1`, id).Scan(&c.ID, &c.Brief, &c.Status, &c.Language, &ruleRaw, &segVersion,
		&metaRaw, &audR, &c.Channels, &c.ApprovedContentVersion, &c.SendVariant, &c.ScheduledAt, &c.CancelledAt,
		&c.CreatedAt, &c.ApprovedAt, &c.CompletedAt)
	if err != nil {
		return Campaign{}, nil, notFound(err)
	}
	if c.Channels == nil {
		c.Channels = []string{}
	}
	for _, t := range []**time.Time{&c.ScheduledAt, &c.CancelledAt} { // RFC 3339 UTC on the wire, whatever the host zone
		if *t != nil {
			u := (*t).UTC()
			*t = &u
		}
	}
	var valid *segment.ValidRule
	if ruleRaw != nil {
		seg := &Segment{Version: segVersion}
		if err := json.Unmarshal(ruleRaw, &seg.Rule); err != nil {
			return Campaign{}, nil, fmt.Errorf("stored segment_rule: %w", err)
		}
		if metaRaw != nil {
			if err := json.Unmarshal(metaRaw, &seg.SegmentMeta); err != nil {
				return Campaign{}, nil, fmt.Errorf("stored segment_meta: %w", err)
			}
		}
		if audR != nil {
			seg.Audience = &AudienceCount{}
			if err := json.Unmarshal(audR, seg.Audience); err != nil {
				return Campaign{}, nil, fmt.Errorf("stored audience: %w", err)
			}
		}
		c.Segment = seg
		if needRule {
			v, errs, _ := segment.Validate(seg.Rule)
			if len(errs) > 0 {
				return Campaign{}, nil, fmt.Errorf("stored segment no longer validates: %v", errs)
			}
			valid = &v
		}
	} else if needRule && slices.Contains(contentEditable, c.Status) {
		return Campaign{}, nil, fmt.Errorf("campaign %s is %s without a segment", id, c.Status)
	}
	cv, err := loadLatestContent(ctx, db, id)
	if err != nil {
		return Campaign{}, nil, err
	}
	c.Content = cv
	return c, valid, nil
}

// worstCase returns the per-channel worst-case variable values for the audience (content.WorstCaseVars, the
// same cleaning and fallback as send time). v == nil means all users.
func (s *Service) worstCase(ctx context.Context, q segment.Querier, v *segment.ValidRule) (map[string]content.Vars, error) {
	if v == nil {
		all, errs, _ := segment.Validate(segment.Rule{})
		if len(errs) > 0 {
			return nil, fmt.Errorf("empty rule does not validate: %v", errs)
		}
		v = &all
	}
	return content.WorstCaseVars(ctx, q, *v)
}

func constraints(locale string, wc map[string]content.Vars) content.Constraints {
	return content.Constraints{Locale: locale, WorstCaseByChannel: wc}
}

func normalizeReport(r content.Report) content.Report {
	if r.ValidFor == nil {
		r.ValidFor = map[string]bool{}
	}
	if r.Fields == nil {
		r.Fields = map[string]any{}
	}
	if r.WorstCase == nil {
		r.WorstCase = map[string]string{}
	}
	r.Variables, r.Errors, r.Warnings = orEmpty(r.Variables), orEmpty(r.Errors), orEmpty(r.Warnings)
	return r
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func invalidState(status, action string) *Error {
	return errorf("invalid_state", fmt.Sprintf("Cannot %s while the campaign is %s.", action, status),
		Issue{Path: "status", Code: status, Message: "current status"})
}

// fromLLM turns terminal LLM failures into API errors; everything else stays an internal error.
func fromLLM(err error) error {
	var le *llm.Error
	switch {
	case errors.As(err, &le):
		return &Error{Code: le.Code, Message: le.Message, Details: orEmpty(le.Details)}
	case errors.Is(err, context.DeadlineExceeded):
		return errorf("llm_failed", "The model did not answer in time.")
	}
	return err
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isUUID rejects malformed ids before they reach Postgres (which would answer 22P02 instead of "not found").
func isUUID(s string) bool { return uuidRE.MatchString(s) }

func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(s string) (time.Time, string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", false
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return time.Time{}, "", false
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil || !isUUID(id) {
		return time.Time{}, "", false
	}
	return at, id, true
}

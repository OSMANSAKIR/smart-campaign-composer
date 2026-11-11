package campaign

import (
	"encoding/json"
	"time"

	"smartcampaign/internal/content"
	"smartcampaign/internal/segment"
)

// Campaign is the API object of docs/contracts/api.md §2.
type Campaign struct {
	ID                     string          `json:"id"`
	Brief                  string          `json:"brief"`
	Status                 string          `json:"status"`
	Language               string          `json:"language"`
	LanguageMode           string          `json:"language_mode"` // campaign | recipient (§9.9)
	Segment                *Segment        `json:"segment"`
	Content                *ContentVersion `json:"content"`
	Channels               []string        `json:"channels"`
	ApprovedContentVersion *int            `json:"approved_content_version"`
	SendVariant            *string         `json:"send_variant"` // a | b | ab; null before approve (§9.3)
	ScheduledAt            *time.Time      `json:"scheduled_at"` // set when approved with send_at (§9.6)
	CancelledAt            *time.Time      `json:"cancelled_at"` // set by cancel (§9.5)
	CreatedAt              time.Time       `json:"created_at"`
	ApprovedAt             *time.Time      `json:"approved_at"`
	CompletedAt            *time.Time      `json:"completed_at"`
	Progress               *Progress       `json:"progress,omitempty"`
}

// Summary is one row of the campaign list.
type Summary struct {
	ID            string     `json:"id"`
	Brief         string     `json:"brief"`
	Status        string     `json:"status"`
	Language      string     `json:"language"`
	Channels      []string   `json:"channels"`
	AudienceTotal *int       `json:"audience_total"`
	CreatedAt     time.Time  `json:"created_at"`
	ApprovedAt    *time.Time `json:"approved_at"`
	ScheduledAt   *time.Time `json:"scheduled_at"` // set when approved with send_at (§9.6)
	CompletedAt   *time.Time `json:"completed_at"`
}

// Segment is the campaign's current segment: the validated rule plus what the model said about it.
type Segment struct {
	Version int          `json:"version"`
	Rule    segment.Rule `json:"rule"`
	SegmentMeta
	Audience *AudienceCount `json:"audience"`
}

// SegmentMeta is stored in campaigns.segment_meta.
type SegmentMeta struct {
	Source              string              `json:"source"` // llm | edited
	Rendered            map[string][]string `json:"rendered"`
	UnsupportedCriteria []string            `json:"unsupported_criteria"`
	Assumptions         []string            `json:"assumptions"`
	Explanation         string              `json:"explanation"`
	ChannelsRequested   string              `json:"channels_requested"`
	Language            string              `json:"language"`
	Warnings            []segment.Issue     `json:"warnings"`
	// OriginalRule is Claude's last generated rule once a human has edited it (kept for audit; api.md §9.2).
	OriginalRule *segment.Rule `json:"original_rule"`
	EditedAt     *time.Time    `json:"edited_at"`
}

// AudienceCount is stored in campaigns.audience.
type AudienceCount struct {
	segment.Audience
	CountedAt time.Time `json:"counted_at"`
}

// ContentVersion is one version of the campaign copy: one campaign_content row per variant (api.md §9.3).
// The top-level source, texts and validation mirror variant a (the v1 shape).
type ContentVersion struct {
	Version      int            `json:"version"`
	Source       string         `json:"source"` // llm | edited | refined (variant a)
	SMSText      string         `json:"sms_text"`
	EmailSubject string         `json:"email_subject"`
	EmailBody    string         `json:"email_body"`
	Validation   content.Report `json:"validation"`
	CreatedAt    time.Time      `json:"created_at"`
	Variants     Variants       `json:"variants"`
}

// Variants holds variant a (always present) and variant b (nil for a one-variant version).
type Variants struct {
	A ContentVariant  `json:"a"`
	B *ContentVariant `json:"b"`
}

// Get returns the variant for key "a" or "b", nil when it does not exist.
func (v Variants) Get(key string) *ContentVariant {
	switch key {
	case "a":
		return &v.A
	case "b":
		return v.B
	}
	return nil
}

// ContentVariant is one campaign_content row.
type ContentVariant struct {
	Label        string         `json:"label"`
	Source       string         `json:"source"` // llm | edited | refined
	Instruction  *string        `json:"instruction"`
	SMSText      string         `json:"sms_text"`
	EmailSubject string         `json:"email_subject"`
	EmailBody    string         `json:"email_body"`
	Validation   content.Report `json:"validation"`
	// Locale is the language of this primary copy (the campaign's language when it was saved). Localized holds
	// the adaptations to the other locale, keyed by locale; always an object (api.md §9.9).
	Locale    string                    `json:"locale"`
	Localized map[string]*LocalizedCopy `json:"localized"`
}

// Content returns the variant's copy.
func (v ContentVariant) Content() content.Content {
	return content.Content{SMSText: v.SMSText, EmailSubject: v.EmailSubject, EmailBody: v.EmailBody}
}

// LocalizedCopy is one campaign_content row of a non-primary locale: the adaptation of a primary copy.
type LocalizedCopy struct {
	Label        string         `json:"label"`
	Source       string         `json:"source"` // llm | edited
	SMSText      string         `json:"sms_text"`
	EmailSubject string         `json:"email_subject"`
	EmailBody    string         `json:"email_body"`
	Validation   content.Report `json:"validation"`
	// Stale is true when the primary copy changed after this row was adapted or last edited (D-22).
	Stale bool `json:"stale"`
	// adaptedFrom is the content.Hash of the primary copy this row was adapted from.
	adaptedFrom string
}

// Content returns the adapted copy.
func (l LocalizedCopy) Content() content.Content {
	return content.Content{SMSText: l.SMSText, EmailSubject: l.EmailSubject, EmailBody: l.EmailBody}
}

// VariantInput is one variant to store: its copy, label, provenance and validation report.
type VariantInput struct {
	Label       string
	Source      string // llm | edited | refined
	Instruction *string
	Content     content.Content
	Report      content.Report
	// Localized are the adaptations stored with this variant, keyed by locale.
	Localized map[string]LocalizedInput
}

// LocalizedInput is one adapted copy to store: its provenance and the primary hash it answers to.
type LocalizedInput struct {
	Label       string
	Source      string // llm | edited
	Content     content.Content
	Report      content.Report
	AdaptedFrom string
}

// Message is one row of GET /api/campaigns/{id}/messages.
type Message struct {
	ID        string     `json:"id"`
	UserID    int64      `json:"user_id"`
	Channel   string     `json:"channel"`
	Status    string     `json:"status"`
	Variant   string     `json:"variant"` // a | b: the content variant this message renders (§9.3)
	Attempts  int        `json:"attempts"`
	LastError *string    `json:"last_error"`
	SentAt    *time.Time `json:"sent_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// ApproveResult is the 202 body of POST …/approve; it is stored and replayed for a repeated key.
type ApproveResult struct {
	Campaign Campaign       `json:"campaign"`
	Enqueued map[string]int `json:"enqueued"`
}

// Issue is one entry of error.details.
type Issue = segment.Issue

// Error is a client-facing failure; Code is one of the API error codes (docs/contracts/api.md §1) and the
// API layer maps it to an HTTP status.
type Error struct {
	Code    string
	Message string
	Details []Issue
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errorf(code, message string, details ...Issue) *Error {
	return &Error{Code: code, Message: message, Details: details}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only plain structs are marshalled here
	}
	return b
}

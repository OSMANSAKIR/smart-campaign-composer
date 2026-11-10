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

// ContentVersion is one row of campaign_content.
type ContentVersion struct {
	Version      int            `json:"version"`
	Source       string         `json:"source"` // llm | edited
	SMSText      string         `json:"sms_text"`
	EmailSubject string         `json:"email_subject"`
	EmailBody    string         `json:"email_body"`
	Validation   content.Report `json:"validation"`
	CreatedAt    time.Time      `json:"created_at"`
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

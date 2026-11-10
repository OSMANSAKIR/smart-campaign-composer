// Package llm is the LLM boundary: the Provider interface that the adapters implement (anthropic, mock,
// claudecode) and the Service that owns prompts, validation, the repair loop and llm_calls accounting, so
// every adapter goes through the same checks (KICKOFF §6). Owned by the llm builder.
//
// "The model proposes, the backend disposes": nothing a provider returns is trusted until it has passed
// strict decoding and the deterministic validators in internal/segment and internal/content.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"smartcampaign/internal/content"
	"smartcampaign/internal/segment"
)

// ErrNotImplemented marks unfinished code paths.
var ErrNotImplemented = errors.New("llm: not implemented")

// Purpose is recorded in llm_calls.purpose.
type Purpose string

const (
	PurposeSegment Purpose = "segment"
	PurposeContent Purpose = "content"
	PurposeRepair  Purpose = "repair"
	PurposeRefine  Purpose = "refine"
)

// StopReason is the normalized reason the model stopped.
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
)

// SystemBlock is one block of the system prompt; Cache marks the end of the stable, cacheable prefix.
type SystemBlock struct {
	Text  string
	Cache bool
}

// Message is one conversation turn (role "user" or "assistant").
type Message struct {
	Role string
	Text string
}

// Request is everything an adapter needs for one call.
type Request struct {
	Purpose   Purpose
	Task      Purpose // segment, content or refine, also for repair calls (adapters pick per-task settings)
	System    []SystemBlock
	Messages  []Message
	Schema    map[string]any // wire schema from internal/catalog
	MaxTokens int
	Stream    bool
}

// Usage is the token accounting of one call.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
}

// Response is the raw result of one call. JSON is the model's structured output, not yet validated.
type Response struct {
	JSON            []byte
	StopReason      StopReason
	RefusalCategory string
	Usage           Usage
	Model           string
	Latency         time.Duration
	// CostUSD, when > 0, is the provider-reported cost (claudecode); otherwise the price table is used.
	CostUSD float64
}

// Delta is a streamed fragment of the model's JSON text. Adapters normalize text_delta (anthropic) and
// input_json_delta (claudecode) into this one shape.
type Delta struct {
	JSONFragment string
}

// Provider is implemented by each adapter. Adapters are transports only: no validation, no retries beyond
// the SDK's own, no prompts.
type Provider interface {
	Name() string
	Model() string
	Generate(ctx context.Context, req Request, onDelta func(Delta)) (Response, error)
}

// Meta is the per-generation metadata returned to the API (docs/contracts/api.md §2).
type Meta struct {
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	PromptVersion    string  `json:"prompt_version"`
	LatencyMS        int64   `json:"latency_ms"`
	Attempts         int     `json:"attempts"`
	Repaired         bool    `json:"repaired"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	// CostRecorded is false when any llm_calls insert of this generation failed (api.md §9.7); the
	// generation itself still succeeded.
	CostRecorded bool `json:"cost_recorded"`
}

// SegmentResult is a validated segment proposal.
type SegmentResult struct {
	Proposal segment.Proposal
	Rule     segment.ValidRule
	Warnings []segment.Issue
	Meta     Meta
}

// ContentInput is what the content prompt needs. The model never sees user rows or values, only
// aggregates: WorstCase values are used for validation, and only their lengths go into the prompt.
type ContentInput struct {
	CampaignID     string
	Brief          string
	Language       string
	SegmentSummary []string // server rendering of the validated rule
	Audience       segment.Audience
	Channels       []string // channels the copy must be valid for; empty = sms and email
	WorstCase      content.Vars
	// WorstCaseByChannel, when set, overrides WorstCase per channel (content.WorstCaseVars).
	WorstCaseByChannel map[string]content.Vars
}

// VariantResult is one validated content variant.
type VariantResult struct {
	Label   string
	Content content.Content
	Report  content.Report
}

// ContentResult is the two validated variants plus their reports. Content and Report mirror variant A (the
// v1 shape).
type ContentResult struct {
	A, B    VariantResult
	Content content.Content
	Report  content.Report
	Notes   string
	Meta    Meta
}

// RefineInput is what the refine prompt needs: the campaign context, the variant to rewrite and the
// marketer's instruction. The instruction is data and never relaxes a limit.
type RefineInput struct {
	ContentInput
	Variant     string // "a" or "b"
	Label       string
	Current     content.Content
	Instruction string
}

// RefineResult is the validated rewrite of one variant.
type RefineResult struct {
	Label   string
	Content content.Content
	Report  content.Report
	Notes   string
	Meta    Meta
}

// StatusError is a provider answer with an HTTP status (the adapters' typed error). Its text never carries
// the provider's response body: the body can hold account identifiers and goes to the warn log only.
type StatusError struct {
	Status int
	Body   string // raw provider body; log at warn level, never send to a client
	Err    error  // the transport's own error (its text may embed the body); reachable with errors.As only
}

func (e *StatusError) Error() string { return fmt.Sprintf("provider returned HTTP %d", e.Status) }

// Unwrap exposes the transport error to errors.As without ever printing it.
func (e *StatusError) Unwrap() error { return e.Err }

// IsRejection reports whether a provider status is a rejection of the request itself (401/403, billing or
// credit errors, a malformed request): 4xx other than 408, 409 and 429 (api.md §9.8).
func IsRejection(status int) bool {
	return status >= 400 && status < 500 && status != 408 && status != 409 && status != 429
}

// Error is returned for terminal LLM failures; Code matches the API error codes
// (llm_refused, llm_invalid_output, llm_failed, validation_failed for a bad brief).
type Error struct {
	Code    string
	Message string
	Details []segment.Issue
	Meta    Meta  // accounting of the attempts made before the failure
	Err     error // underlying transport error, e.g. context.Canceled
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Unwrap exposes the transport error so callers can detect a client disconnect (context.Canceled).
func (e *Error) Unwrap() error { return e.Err }

// Recorder persists one row per call into llm_calls.
type Recorder interface {
	RecordCall(ctx context.Context, c Call) error
}

// Call is one llm_calls row.
type Call struct {
	CampaignID    string
	Purpose       Purpose
	Provider      string
	Model         string
	PromptVersion string
	Usage         Usage
	CostUSD       float64
	Latency       time.Duration
	StopReason    StopReason
	Outcome       string // valid|repaired|invalid|refused|error
	Error         string
}

// RepairNotice is passed to onRepair when a streamed attempt was invalid and a repair starts.
type RepairNotice struct {
	Attempt int
	Errors  json.RawMessage
}

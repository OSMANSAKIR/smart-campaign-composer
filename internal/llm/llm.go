// Package llm is the LLM boundary: the Provider interface that the adapters implement (anthropic, mock,
// claudecode) and the Service that owns prompts, validation, the repair loop and llm_calls accounting, so
// every adapter goes through the same checks (KICKOFF §6). Owned by the llm builder.
//
// Phase 0 skeleton: types and signatures are the contract; Service methods return ErrNotImplemented.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"smartcampaign/internal/content"
	"smartcampaign/internal/segment"
)

// ErrNotImplemented marks skeleton functions.
var ErrNotImplemented = errors.New("llm: not implemented")

// Purpose is recorded in llm_calls.purpose.
type Purpose string

const (
	PurposeSegment Purpose = "segment"
	PurposeContent Purpose = "content"
	PurposeRepair  Purpose = "repair"
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
}

// SegmentResult is a validated segment proposal.
type SegmentResult struct {
	Proposal segment.Proposal
	Rule     segment.ValidRule
	Warnings []segment.Issue
	Meta     Meta
}

// ContentInput is what the content prompt needs. The model never sees user rows, only aggregates.
type ContentInput struct {
	CampaignID     string
	Brief          string
	Language       string
	SegmentSummary []string // server rendering of the validated rule
	Audience       segment.Audience
	Channels       []string
	WorstCase      content.Vars
}

// ContentResult is validated content plus its report.
type ContentResult struct {
	Content content.Content
	Report  content.Report
	Meta    Meta
}

// Error is returned for terminal LLM failures; Code matches the API error codes
// (llm_refused, llm_invalid_output, llm_failed).
type Error struct {
	Code    string
	Message string
	Details []segment.Issue
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

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

// Service runs the two LLM tasks end to end.
type Service struct {
	provider Provider
	recorder Recorder
}

// NewService wires a provider and a recorder.
func NewService(p Provider, r Recorder) *Service { return &Service{provider: p, recorder: r} }

// Provider exposes the configured adapter (for /api/system/status).
func (s *Service) Provider() Provider { return s.provider }

// ProposeSegment turns a brief into a validated segment (non-streamed).
func (s *Service) ProposeSegment(ctx context.Context, campaignID, brief string) (SegmentResult, error) {
	return SegmentResult{}, ErrNotImplemented
}

// ProposeContent streams content generation; onDelta receives JSON fragments, onRepair is called before
// each repair attempt.
func (s *Service) ProposeContent(ctx context.Context, in ContentInput, onDelta func(Delta), onRepair func(RepairNotice)) (ContentResult, error) {
	return ContentResult{}, ErrNotImplemented
}

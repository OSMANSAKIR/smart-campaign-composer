// Package anthropic is the delivered LLM adapter: the Claude Messages API through the official Go SDK
// (KICKOFF §6.7). Owned by the llm builder; invoke the claude-api skill before changing SDK code.
package anthropic

import (
	"context"
	"errors"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"smartcampaign/internal/llm"
)

// Options configures the adapter.
type Options struct {
	APIKey          string // passed explicitly; the SDK must not pick up ambient credentials
	Model           string
	RefusalFallback string // off|default — off until smoke-live verifies it (need.md N-ORC-008)
	BaseURL         string // tests point this at an httptest server
}

// Adapter calls the Claude API.
type Adapter struct {
	client sdk.Client
	opts   Options
}

// New builds the adapter. SDK retries cover 429/5xx/529 (max 2, KICKOFF §6.7).
func New(o Options) (*Adapter, error) {
	if o.APIKey == "" {
		return nil, errors.New("anthropic: API key is empty")
	}
	reqOpts := []option.RequestOption{option.WithAPIKey(o.APIKey), option.WithMaxRetries(2)}
	if o.BaseURL != "" {
		reqOpts = append(reqOpts, option.WithBaseURL(o.BaseURL))
	}
	return &Adapter{client: sdk.NewClient(reqOpts...), opts: o}, nil
}

func (a *Adapter) Name() string  { return "anthropic" }
func (a *Adapter) Model() string { return a.opts.Model }

// Generate is not implemented yet (Phase 0 skeleton).
func (a *Adapter) Generate(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	return llm.Response{}, llm.ErrNotImplemented
}

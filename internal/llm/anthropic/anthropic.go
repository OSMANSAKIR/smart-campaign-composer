// Package anthropic is the delivered LLM adapter: the Claude Messages API through the official Go SDK
// (KICKOFF §6.7). Owned by the llm builder; invoke the claude-api skill before changing SDK code.
//
// Request shape (checked against the claude-api skill, 2026-11-08):
//   - structured outputs via output_config.format (json_schema with the catalog wire schema), never forced
//     tool_choice, which is a 400 on Sonnet 5.5;
//   - adaptive thinking at effort low (thinking cannot be disabled on Sonnet 5.5; need.md N-ORC-008);
//   - the stable system prompt carries cache_control;
//   - always streamed, so long answers never hit HTTP timeouts and content deltas reach the UI live;
//   - no beta header and no fallbacks parameter unless RefusalFallback == "default" (owner decision).
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"smartcampaign/internal/llm"
)

// RefusalFallbackBeta is the beta header for the server-side fallback in its "default" form.
const RefusalFallbackBeta = "server-side-fallback-2026-07-01"

// Options configures the adapter.
type Options struct {
	APIKey          string // passed explicitly; the SDK must not pick up ambient credentials
	Model           string
	RefusalFallback string       // off|default — off until smoke-live verifies it (need.md N-ORC-008)
	BaseURL         string       // tests point this at an httptest server
	MaxRetries      int          // SDK retries for 408/409/429/5xx/529; 0 means the default of 2
	HTTPClient      *http.Client // nil = a fresh client; the per-call context carries the timeout
}

// Adapter calls the Claude API.
type Adapter struct {
	client sdk.Client
	opts   Options
}

// New builds the adapter. SDK retries cover 429/5xx/529 (max 2, KICKOFF §6.7) and honour retry-after.
func New(o Options) (*Adapter, error) {
	if o.APIKey == "" {
		return nil, errors.New("anthropic: API key is empty")
	}
	if o.Model == "" {
		o.Model = "claude-sonnet-5-5"
	}
	retries := o.MaxRetries
	if retries <= 0 {
		retries = 2
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	// WithoutEnvironmentDefaults: only the key from config is used. The SDK would otherwise also read
	// ANTHROPIC_AUTH_TOKEN, ANTHROPIC_PROFILE, federation variables, profile files and custom headers from
	// the environment, which must never change what a reviewer's machine sends.
	reqOpts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithHTTPClient(hc),
		option.WithAPIKey(o.APIKey), option.WithMaxRetries(retries)}
	if o.BaseURL != "" {
		reqOpts = append(reqOpts, option.WithBaseURL(o.BaseURL))
	}
	return &Adapter{client: sdk.NewClient(reqOpts...), opts: o}, nil
}

func (a *Adapter) Name() string  { return "anthropic" }
func (a *Adapter) Model() string { return a.opts.Model }

// Params builds the Messages API request; exported for the contract tests.
func (a *Adapter) Params(req llm.Request) sdk.MessageNewParams {
	system := make([]sdk.TextBlockParam, 0, len(req.System))
	for _, b := range req.System {
		tb := sdk.TextBlockParam{Text: b.Text}
		if b.Cache {
			tb.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		system = append(system, tb)
	}
	msgs := make([]sdk.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			msgs = append(msgs, sdk.NewAssistantMessage(sdk.NewTextBlock(m.Text)))
		} else {
			msgs = append(msgs, sdk.NewUserMessage(sdk.NewTextBlock(m.Text)))
		}
	}
	return sdk.MessageNewParams{
		Model:     sdk.Model(a.opts.Model),
		MaxTokens: int64(req.MaxTokens),
		System:    system,
		Messages:  msgs,
		Thinking:  sdk.ThinkingConfigParamUnion{OfAdaptive: &sdk.ThinkingConfigAdaptiveParam{}},
		OutputConfig: sdk.OutputConfigParam{
			Effort: sdk.OutputConfigEffortLow,
			Format: sdk.JSONOutputFormatParam{Schema: req.Schema},
		},
	}
}

// Generate streams one call. Only text blocks form the answer; thinking blocks (empty under the default
// display) are ignored. Deltas are forwarded as they arrive.
func (a *Adapter) Generate(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	var reqOpts []option.RequestOption
	if a.opts.RefusalFallback == "default" {
		reqOpts = append(reqOpts, option.WithHeaderAdd("anthropic-beta", RefusalFallbackBeta), option.WithJSONSet("fallbacks", "default"))
	}
	stream := a.client.Messages.NewStreaming(ctx, a.Params(req), reqOpts...)
	defer func() { _ = stream.Close() }() // close errors after a finished read are not actionable

	var (
		resp      = llm.Response{Model: a.opts.Model}
		text      strings.Builder
		blockType = map[int64]string{}
		stopRaw   sdk.StopReason
	)
	for stream.Next() {
		switch ev := stream.Current().AsAny().(type) {
		case sdk.MessageStartEvent:
			if ev.Message.Model != "" {
				resp.Model = string(ev.Message.Model)
			}
			u := ev.Message.Usage
			resp.Usage = llm.Usage{InputTokens: int(u.InputTokens), OutputTokens: int(u.OutputTokens),
				CacheReadTokens: int(u.CacheReadInputTokens), CacheWriteTokens: int(u.CacheCreationInputTokens)}
		case sdk.ContentBlockStartEvent:
			blockType[ev.Index] = ev.ContentBlock.Type
		case sdk.ContentBlockDeltaEvent:
			if td, ok := ev.Delta.AsAny().(sdk.TextDelta); ok && blockType[ev.Index] == "text" {
				text.WriteString(td.Text)
				if onDelta != nil && td.Text != "" {
					onDelta(llm.Delta{JSONFragment: td.Text})
				}
			}
		case sdk.MessageDeltaEvent:
			stopRaw = ev.Delta.StopReason
			if stopRaw == sdk.StopReasonRefusal {
				resp.RefusalCategory = string(ev.Delta.StopDetails.Category)
			}
			// message_delta usage is cumulative; input and cache counts may be repeated or refined here.
			u := ev.Usage
			resp.Usage.OutputTokens = int(u.OutputTokens)
			resp.Usage.InputTokens = max(resp.Usage.InputTokens, int(u.InputTokens))
			resp.Usage.CacheReadTokens = max(resp.Usage.CacheReadTokens, int(u.CacheReadInputTokens))
			resp.Usage.CacheWriteTokens = max(resp.Usage.CacheWriteTokens, int(u.CacheCreationInputTokens))
		}
	}
	if err := stream.Err(); err != nil {
		// The SDK error's text embeds the response body, which can hold account identifiers. Hand the service
		// a typed error with the status and the body kept apart (F7); the service decides what is logged.
		var apiErr *sdk.Error
		if errors.As(err, &apiErr) {
			return resp, fmt.Errorf("anthropic: %w", &llm.StatusError{Status: apiErr.StatusCode, Body: apiErr.RawJSON(), Err: err})
		}
		return resp, fmt.Errorf("anthropic: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return resp, err
	}
	switch stopRaw {
	case sdk.StopReasonRefusal:
		resp.StopReason = llm.StopRefusal
	case sdk.StopReasonMaxTokens, "model_context_window_exceeded":
		resp.StopReason = llm.StopMaxTokens
	case "":
		return resp, errors.New("anthropic: stream ended without a stop reason")
	default:
		resp.StopReason = llm.StopEndTurn
	}
	resp.JSON = []byte(text.String())
	return resp, nil
}

// Package mock is the deterministic LLM adapter: scripted responses for tests and a keyword-based demo mode
// for running without an API key (KICKOFF §6.10). Owned by the llm builder.
package mock

import (
	"context"

	"smartcampaign/internal/llm"
)

// DemoModel is reported as the model name in demo mode.
const DemoModel = "mock-demo-v1"

// Demo is the keyword/catalog heuristic used by compose when no key is set. Skeleton: not implemented.
type Demo struct{}

// NewDemo returns the demo adapter.
func NewDemo() *Demo { return &Demo{} }

func (*Demo) Name() string  { return "mock" }
func (*Demo) Model() string { return DemoModel }

func (*Demo) Generate(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	return llm.Response{}, llm.ErrNotImplemented
}

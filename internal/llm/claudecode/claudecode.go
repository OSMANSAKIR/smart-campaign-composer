// Package claudecode is the dev-only LLM adapter that shells out to the owner's local Claude Code CLI
// (KICKOFF §6.9). Host only; never used in tests, CI, Docker or as a default. Owned by the llm builder.
package claudecode

import (
	"context"

	"smartcampaign/internal/llm"
)

// Options configures the CLI call.
type Options struct {
	Bin   string // CLAUDECODE_BIN
	Model string // CLAUDECODE_MODEL, e.g. "sonnet"
}

// Adapter runs `claude -p` with structured output.
type Adapter struct{ opts Options }

// New returns the adapter.
func New(o Options) *Adapter { return &Adapter{opts: o} }

func (a *Adapter) Name() string  { return "claudecode" }
func (a *Adapter) Model() string { return a.opts.Model }

// Generate is not implemented yet (Phase 0 skeleton).
func (a *Adapter) Generate(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	return llm.Response{}, llm.ErrNotImplemented
}

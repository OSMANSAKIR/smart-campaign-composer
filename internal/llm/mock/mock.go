// Package mock is the deterministic LLM adapter: scripted responses for tests and a keyword-based demo mode
// for running without an API key (KICKOFF §6.10). Owned by the llm builder.
package mock

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"smartcampaign/internal/llm"
)

// ScriptedModel is reported as the model name in scripted mode.
const ScriptedModel = "mock-scripted"

// Step is one canned answer. Chunks, when set, are streamed in order (they may split a rune on purpose);
// otherwise JSON is sent as one delta.
type Step struct {
	JSON            string
	Chunks          []string
	StopReason      llm.StopReason // default end_turn
	RefusalCategory string
	Err             error
	Usage           llm.Usage
	Model           string
	// BlockAfterChunks > 0 makes the step stop after that many chunks and wait until the context ends, like a
	// slow upstream; used to prove that cancellation stops the provider.
	BlockAfterChunks int
}

// Scripted replays a queue of steps, one per Generate call, and records every request.
type Scripted struct {
	mu        sync.Mutex
	steps     []Step
	requests  []llm.Request
	cancelled int
}

// NewScripted returns a scripted adapter.
func NewScripted(steps ...Step) *Scripted { return &Scripted{steps: steps} }

func (*Scripted) Name() string  { return "mock" }
func (*Scripted) Model() string { return ScriptedModel }

// ErrScriptExhausted is returned when more calls arrive than steps were scripted.
var ErrScriptExhausted = errors.New("mock: no scripted response left")

// Generate pops the next step.
func (s *Scripted) Generate(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	if len(s.steps) == 0 {
		s.mu.Unlock()
		return llm.Response{}, ErrScriptExhausted
	}
	st := s.steps[0]
	s.steps = s.steps[1:]
	s.mu.Unlock()

	if st.Err != nil {
		return llm.Response{}, st.Err
	}
	chunks := st.Chunks
	if chunks == nil {
		chunks = []string{st.JSON}
	}
	var out strings.Builder
	for i, c := range chunks {
		if st.BlockAfterChunks > 0 && i == st.BlockAfterChunks {
			<-ctx.Done()
			s.mu.Lock()
			s.cancelled++
			s.mu.Unlock()
			return llm.Response{}, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return llm.Response{}, err
		}
		out.WriteString(c)
		if onDelta != nil && c != "" {
			onDelta(llm.Delta{JSONFragment: c})
		}
	}
	if st.BlockAfterChunks > 0 { // block after the last chunk too
		<-ctx.Done()
		s.mu.Lock()
		s.cancelled++
		s.mu.Unlock()
		return llm.Response{}, ctx.Err()
	}
	body := out.String()
	if st.Chunks == nil {
		body = st.JSON
	}
	stop := st.StopReason
	if stop == "" {
		stop = llm.StopEndTurn
	}
	model := st.Model
	if model == "" {
		model = ScriptedModel
	}
	return llm.Response{JSON: []byte(body), StopReason: stop, RefusalCategory: st.RefusalCategory, Usage: st.Usage, Model: model}, nil
}

// Requests returns a copy of the requests received so far.
func (s *Scripted) Requests() []llm.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Cancelled reports how many calls ended because their context was cancelled while blocked.
func (s *Scripted) Cancelled() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled
}

// Remaining reports how many steps were not consumed.
func (s *Scripted) Remaining() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.steps)
}

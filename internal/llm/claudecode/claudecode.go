// Package claudecode is the dev-only LLM adapter that shells out to the owner's local Claude Code CLI
// (KICKOFF §6.9). Host only; never used in tests' happy path, CI, Docker or as a default. Owned by the llm
// builder.
//
// Isolation: an empty temp working directory, ANTHROPIC_API_KEY and ANTHROPIC_AUTH_TOKEN removed from the
// child environment (so the CLI uses the subscription login and never bills the API), no tools, no
// project settings, no MCP servers, no session persistence; at most 2 concurrent calls; 90 s timeout.
package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"smartcampaign/internal/llm"
)

// Limits from KICKOFF §6.9 (subscription limits).
const (
	MaxConcurrent = 2
	Timeout       = 90 * time.Second
)

// Options configures the CLI call.
type Options struct {
	Bin   string // CLAUDECODE_BIN
	Model string // CLAUDECODE_MODEL, e.g. "sonnet"
	// ExtraEnv is appended to the scrubbed environment (tests only).
	ExtraEnv []string
}

// Adapter runs `claude -p` with structured output.
type Adapter struct {
	opts Options
	sem  chan struct{}
}

// New returns the adapter.
func New(o Options) *Adapter {
	if o.Bin == "" {
		o.Bin = "claude"
	}
	if o.Model == "" {
		o.Model = "sonnet"
	}
	return &Adapter{opts: o, sem: make(chan struct{}, MaxConcurrent)}
}

func (a *Adapter) Name() string  { return "claudecode" }
func (a *Adapter) Model() string { return a.opts.Model }

// Args builds the CLI arguments; exported for tests.
func (a *Adapter) Args(req llm.Request) ([]string, error) {
	schema, err := json.Marshal(req.Schema)
	if err != nil {
		return nil, err
	}
	var system strings.Builder
	for i, b := range req.System {
		if i > 0 {
			system.WriteString("\n\n")
		}
		system.WriteString(b.Text)
	}
	return []string{"-p", "--model", a.opts.Model, "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--json-schema", string(schema), "--system-prompt", system.String(),
		"--tools", "", "--no-session-persistence", "--setting-sources", "", "--strict-mcp-config"}, nil
}

// Prompt flattens the conversation for stdin: `claude -p` takes one prompt, so repair turns are sent as a
// transcript of the earlier turns followed by the latest user message.
func Prompt(msgs []llm.Message) string {
	if len(msgs) == 1 {
		return msgs[0].Text
	}
	var b strings.Builder
	b.WriteString("Earlier turns of this conversation:\n")
	for _, m := range msgs[:len(msgs)-1] {
		fmt.Fprintf(&b, "<%s>\n%s\n</%s>\n", m.Role, m.Text, m.Role)
	}
	b.WriteString("\nLatest message:\n")
	b.WriteString(msgs[len(msgs)-1].Text)
	return b.String()
}

// Env returns the child environment: the parent's minus every API credential.
func Env(parent []string) []string {
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Generate runs one CLI call.
func (a *Adapter) Generate(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	args, err := a.Args(req)
	if err != nil {
		return llm.Response{}, err
	}
	dir, err := os.MkdirTemp("", "scc-claudecode-")
	if err != nil {
		return llm.Response{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	start := time.Now()
	cmd := exec.CommandContext(ctx, a.opts.Bin, args...)
	cmd.Dir = dir
	cmd.Env = append(Env(os.Environ()), a.opts.ExtraEnv...)
	cmd.Stdin = strings.NewReader(Prompt(req.Messages))
	cmd.WaitDelay = 2 * time.Second
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return llm.Response{}, err
	}
	if err := cmd.Start(); err != nil {
		return llm.Response{}, fmt.Errorf("claudecode: start %s: %w", a.opts.Bin, err)
	}
	resp, parseErr := Parse(stdout, onDelta)
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	resp.Latency = time.Since(start)
	if ctx.Err() != nil {
		return resp, ctx.Err()
	}
	if parseErr != nil {
		return resp, parseErr
	}
	if waitErr != nil {
		return resp, fmt.Errorf("claudecode: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return resp, nil
}

// Parse reads the CLI's stream-json output. Partial structured output arrives as input_json_delta events of
// the internal StructuredOutput tool and is normalized to llm.Delta; the final result line carries the
// complete structured_output, usage and cost.
func Parse(r io.Reader, onDelta func(llm.Delta)) (llm.Response, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	var resp llm.Response
	gotResult := false
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			continue // the CLI may print non-JSON noise; only typed lines matter
		}
		switch head.Type {
		case "stream_event":
			var se struct {
				Event struct {
					Type  string `json:"type"`
					Delta struct {
						Type        string `json:"type"`
						PartialJSON string `json:"partial_json"`
					} `json:"delta"`
				} `json:"event"`
			}
			if json.Unmarshal(line, &se) == nil && se.Event.Type == "content_block_delta" &&
				se.Event.Delta.Type == "input_json_delta" && onDelta != nil && se.Event.Delta.PartialJSON != "" {
				onDelta(llm.Delta{JSONFragment: se.Event.Delta.PartialJSON})
			}
		case "result":
			var res struct {
				IsError          bool            `json:"is_error"`
				Result           string          `json:"result"`
				StopReason       string          `json:"stop_reason"`
				StructuredOutput json.RawMessage `json:"structured_output"`
				TotalCostUSD     float64         `json:"total_cost_usd"`
				Usage            struct {
					InputTokens              int `json:"input_tokens"`
					OutputTokens             int `json:"output_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
				ModelUsage map[string]json.RawMessage `json:"modelUsage"`
			}
			if err := json.Unmarshal(line, &res); err != nil {
				return resp, fmt.Errorf("claudecode: bad result line: %w", err)
			}
			gotResult = true
			resp.Usage = llm.Usage{InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens,
				CacheReadTokens: res.Usage.CacheReadInputTokens, CacheWriteTokens: res.Usage.CacheCreationInputTokens}
			resp.CostUSD = res.TotalCostUSD
			for m := range res.ModelUsage {
				if resp.Model == "" || m < resp.Model { // deterministic when several models report usage
					resp.Model = m
				}
			}
			switch {
			case res.StopReason == "refusal":
				resp.StopReason = llm.StopRefusal
			case res.StopReason == "max_tokens" || head.Subtype == "error_max_tokens":
				resp.StopReason = llm.StopMaxTokens
			case head.Subtype != "success" || res.IsError:
				return resp, fmt.Errorf("claudecode: result %s: %s", head.Subtype, truncate(res.Result, 300))
			default:
				resp.StopReason = llm.StopEndTurn
			}
			if len(res.StructuredOutput) > 0 && string(res.StructuredOutput) != "null" {
				resp.JSON = res.StructuredOutput
			} else {
				resp.JSON = []byte(res.Result) // validation reports what is wrong with it
			}
		}
	}
	if err := sc.Err(); err != nil {
		return resp, fmt.Errorf("claudecode: read output: %w", err)
	}
	if !gotResult {
		return resp, errors.New("claudecode: output ended without a result line")
	}
	return resp, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// limitedBuffer keeps the first 4 KB of stderr for error messages.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := 4096 - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

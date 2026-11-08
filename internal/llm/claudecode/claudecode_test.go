package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/llm"
)

// The fixture is a real, sanitized capture of `claude -p` for the official brief (its first line says how it was
// made; need.md N-LLM-003). The CLI reports stop_reason tool_use for its StructuredOutput tool; Parse maps a
// successful result to end_turn.

func TestParse_ProbeFixture_RAI1(t *testing.T) {
	f, err := os.Open("testdata/probe_segment.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var deltas []string
	resp, err := Parse(f, func(d llm.Delta) { deltas = append(deltas, d.JSONFragment) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != llm.StopEndTurn || resp.Model != "claude-sonnet-5-5" || resp.CostUSD != 0.026199999999999998 {
		t.Fatalf("resp: stop %q, model %q, cost %v", resp.StopReason, resp.Model, resp.CostUSD)
	}
	if resp.Usage != (llm.Usage{InputTokens: 2, OutputTokens: 482, CacheWriteTokens: 5344}) {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	var p struct {
		Segment struct {
			Groups []struct {
				Conditions []struct{ Field string } `json:"conditions"`
			} `json:"groups"`
		} `json:"segment"`
	}
	if err := json.Unmarshal(resp.JSON, &p); err != nil || len(p.Segment.Groups) != 3 || p.Segment.Groups[0].Conditions[0].Field != "city" {
		t.Fatalf("structured output is not the official brief's rule: %v %s", err, resp.JSON)
	}
	var streamed, final any
	if err := json.Unmarshal([]byte(strings.Join(deltas, "")), &streamed); err != nil {
		t.Fatalf("input_json_delta fragments must form the JSON: %v", err)
	}
	_ = json.Unmarshal(resp.JSON, &final)
	if fmt.Sprint(streamed) != fmt.Sprint(final) || len(deltas) < 5 {
		t.Fatalf("streamed and final structured output differ (%d deltas)", len(deltas))
	}
	// The fixture must stay sanitized: no home directories or real session ids.
	raw, _ := os.ReadFile("testdata/probe_segment.jsonl")
	for _, bad := range []string{"/Users/", "/home/", "@"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("fixture contains %q", bad)
		}
	}
}

func TestParse_ErrorsAndRefusal_RAI1(t *testing.T) {
	tests := []struct {
		name, out string
		stop      llm.StopReason
		wantErr   bool
	}{
		{"no result line", `{"type":"system","subtype":"init"}` + "\n", "", true},
		{"error result", `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}` + "\n", "", true},
		{"refusal", `{"type":"result","subtype":"success","stop_reason":"refusal","result":""}` + "\n", llm.StopRefusal, false},
		{"text instead of structured output", "noise\n" + `{"type":"result","subtype":"success","result":"not json"}` + "\n", llm.StopEndTurn, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := Parse(strings.NewReader(tt.out), nil)
			if (err != nil) != tt.wantErr || resp.StopReason != tt.stop {
				t.Fatalf("resp = %+v, err = %v", resp, err)
			}
		})
	}
}

func TestArgs_IsolationFlags_RAI1(t *testing.T) {
	a := New(Options{})
	args, err := a.Args(llm.Request{System: []llm.SystemBlock{{Text: "sys", Cache: true}}, Schema: catalog.ContentWireSchema()})
	if err != nil {
		t.Fatal(err)
	}
	flag := func(name string) (string, bool) {
		i := slices.Index(args, name)
		if i < 0 || i+1 >= len(args) {
			return "", false
		}
		return args[i+1], true
	}
	for name, want := range map[string]string{"--tools": "", "--setting-sources": "", "--model": "sonnet", "--output-format": "stream-json", "--system-prompt": "sys"} {
		if got, ok := flag(name); !ok || got != want {
			t.Fatalf("%s = %q (present %v), want %q", name, got, ok, want)
		}
	}
	for _, f := range []string{"-p", "--strict-mcp-config", "--no-session-persistence", "--include-partial-messages", "--verbose"} {
		if !slices.Contains(args, f) {
			t.Fatalf("missing %s", f)
		}
	}
	if s, _ := flag("--json-schema"); !strings.Contains(s, `"additionalProperties":false`) {
		t.Fatalf("schema = %s", s)
	}
	env := Env([]string{"PATH=/bin", "ANTHROPIC_API_KEY=sk-x", "ANTHROPIC_AUTH_TOKEN=t", "HOME=/h"})
	if !slices.Equal(env, []string{"PATH=/bin", "HOME=/h"}) {
		t.Fatalf("env = %v", env)
	}
}

func TestPrompt_FlattensRepairTurns_RAI1(t *testing.T) {
	if got := Prompt([]llm.Message{{Role: "user", Text: "only"}}); got != "only" {
		t.Fatal(got)
	}
	got := Prompt([]llm.Message{{Role: "user", Text: "brief"}, {Role: "assistant", Text: "{}"}, {Role: "user", Text: "fix it"}})
	if !strings.Contains(got, "<assistant>\n{}\n</assistant>") || !strings.HasSuffix(got, "Latest message:\nfix it") {
		t.Fatal(got)
	}
}

// fakeCLI writes a shell wrapper that runs this test binary as the "claude" CLI.
func fakeCLI(t *testing.T, mode string) *Adapter {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("testdata/probe_segment.jsonl")
	bin := filepath.Join(t.TempDir(), "claude")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=TestHelperProcess -- \"$@\"\n", exe)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return New(Options{Bin: bin, ExtraEnv: []string{"SCC_FAKE_CLAUDE=" + mode, "SCC_FAKE_FIXTURE=" + fixture}})
}

// TestHelperProcess is the fake CLI; it does nothing in a normal run.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("SCC_FAKE_CLAUDE")
	if mode == "" {
		return
	}
	switch mode {
	case "fixture":
		b, _ := os.ReadFile(os.Getenv("SCC_FAKE_FIXTURE"))
		_, _ = os.Stdout.Write(b)
	case "check":
		stdin, _ := io.ReadAll(os.Stdin)
		cwd, _ := os.Getwd()
		entries, _ := os.ReadDir(cwd)
		_, hasKey := os.LookupEnv("ANTHROPIC_API_KEY")
		_, hasToken := os.LookupEnv("ANTHROPIC_AUTH_TOKEN")
		out, _ := json.Marshal(map[string]any{"cwd_entries": len(entries), "api_key": hasKey, "auth_token": hasToken, "stdin": string(stdin)})
		fmt.Printf(`{"type":"result","subtype":"success","structured_output":%s,"usage":{},"modelUsage":{"claude-sonnet-5-5":{}}}`+"\n", out)
	case "hang":
		fmt.Println(`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{"}}}`)
		select {} // until killed
	}
	os.Exit(0)
}

func TestGenerate_IsolatedChildProcess_RAI1(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-must-not-reach-the-cli")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "must-not-reach-the-cli")
	a := fakeCLI(t, "check")
	resp, err := a.Generate(context.Background(), llm.Request{Messages: []llm.Message{{Role: "user", Text: "<brief>x</brief>"}}, Schema: map[string]any{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		CwdEntries int    `json:"cwd_entries"`
		APIKey     bool   `json:"api_key"`
		AuthToken  bool   `json:"auth_token"`
		Stdin      string `json:"stdin"`
	}
	if err := json.Unmarshal(resp.JSON, &got); err != nil {
		t.Fatal(err)
	}
	if got.CwdEntries != 0 || got.APIKey || got.AuthToken || got.Stdin != "<brief>x</brief>" {
		t.Fatalf("child saw %+v", got)
	}
}

func TestGenerate_FixtureThroughService_RAI1(t *testing.T) {
	res, err := llm.NewService(fakeCLI(t, "fixture"), nil).ProposeSegment(context.Background(), "", llm.OfficialBrief)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Rule.Valid() || res.Meta.Provider != "claudecode" || res.Meta.Model != "claude-sonnet-5-5" || res.Meta.CostUSD != 0.026199999999999998 {
		t.Fatalf("meta = %+v", res.Meta)
	}
}

func TestGenerate_CancelKillsCLI_RFLOW2(t *testing.T) {
	defer goleak.VerifyNone(t)
	a := fakeCLI(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		_, err := a.Generate(ctx, llm.Request{Messages: []llm.Message{{Role: "user", Text: "x"}}, Schema: map[string]any{}},
			func(llm.Delta) { cancel() })
		errc <- err
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Generate did not return after cancel")
	}
}

package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"go.uber.org/goleak"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/llm"
)

// fakeAPI is an httptest stand-in for POST /v1/messages that records every request.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	bodies   []map[string]any
	headers  []http.Header
	paths    []string
	respond  func(w http.ResponseWriter, r *http.Request, n int) // n = 0-based request number
	srv      *httptest.Server
	client   *http.Client
	requests int
}

func newFakeAPI(t *testing.T, respond func(w http.ResponseWriter, r *http.Request, n int)) *fakeAPI {
	f := &fakeAPI{t: t, respond: respond}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		n := f.requests
		f.requests++
		f.bodies = append(f.bodies, body)
		f.headers = append(f.headers, r.Header.Clone())
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		f.respond(w, r, n)
	}))
	f.client = &http.Client{}
	t.Cleanup(func() {
		f.client.CloseIdleConnections()
		f.srv.Close()
	})
	return f
}

func (f *fakeAPI) adapter(t *testing.T, o Options) *Adapter {
	t.Helper()
	o.APIKey, o.BaseURL, o.HTTPClient = "test-key", f.srv.URL, f.client
	if o.Model == "" {
		o.Model = "claude-sonnet-5-5"
	}
	a, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func serveSSE(t *testing.T, name string) func(http.ResponseWriter, *http.Request, int) {
	body := fixture(t, name)
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

// apiError writes the documented error body (claude-api skill, shared/error-codes.md "Example error").
func apiError(w http.ResponseWriter, status int, typ string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("retry-after", "0")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"type":"error","error":{"type":"`+typ+`","message":"synthetic"},"request_id":"req_synthetic"}`)
}

func segmentRequest() llm.Request {
	return llm.Request{Purpose: llm.PurposeSegment, Task: llm.PurposeSegment,
		System:   []llm.SystemBlock{{Text: "stable system prompt", Cache: true}},
		Messages: []llm.Message{{Role: "user", Text: "<brief>b</brief>"}, {Role: "assistant", Text: "{}"}, {Role: "user", Text: "fix"}},
		Schema:   catalog.SegmentWireSchema(), MaxTokens: 8192, Stream: true}
}

func TestAnthropic_RequestShape_RAI1(t *testing.T) {
	// Ambient credentials and headers must not leak into the request.
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-token")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Ambient: 1")
	f := newFakeAPI(t, serveSSE(t, "stream_segment.sse"))
	if _, err := f.adapter(t, Options{RefusalFallback: "off"}).Generate(context.Background(), segmentRequest(), nil); err != nil {
		t.Fatal(err)
	}
	h, body := f.headers[0], f.bodies[0]
	if f.paths[0] != "/v1/messages" {
		t.Fatalf("path = %s", f.paths[0])
	}
	if h.Get("X-Api-Key") != "test-key" || h.Get("Authorization") != "" || h.Get("X-Ambient") != "" {
		t.Fatalf("auth headers: x-api-key=%q authorization=%q ambient=%q", h.Get("X-Api-Key"), h.Get("Authorization"), h.Get("X-Ambient"))
	}
	if h.Get("Anthropic-Version") == "" {
		t.Fatal("anthropic-version header missing")
	}
	if h.Get("Anthropic-Beta") != "" {
		t.Fatalf("no beta header expected by default (need.md N-ORC-008), got %q", h.Get("Anthropic-Beta"))
	}
	want := map[string]any{
		"model": "claude-sonnet-5-5", "max_tokens": float64(8192), "stream": true,
		"thinking":      map[string]any{"type": "adaptive"},
		"system":        []any{map[string]any{"type": "text", "text": "stable system prompt", "cache_control": map[string]any{"type": "ephemeral"}}},
		"output_config": map[string]any{"effort": "low", "format": map[string]any{"type": "json_schema", "schema": jsonRoundTrip(t, catalog.SegmentWireSchema())}},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "<brief>b</brief>"}}},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "{}"}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix"}}},
		},
	}
	for k, v := range want {
		if !reflect.DeepEqual(body[k], v) {
			got, _ := json.Marshal(body[k])
			exp, _ := json.Marshal(v)
			t.Fatalf("%s:\n got %s\nwant %s", k, got, exp)
		}
	}
	// The request stays as plain as possible: nothing beyond the expected keys.
	for k := range body {
		if _, ok := want[k]; !ok {
			t.Fatalf("unexpected request field %q", k)
		}
	}
}

func TestAnthropic_RefusalFallbackOptIn_RAI1(t *testing.T) {
	f := newFakeAPI(t, serveSSE(t, "stream_segment.sse"))
	if _, err := f.adapter(t, Options{RefusalFallback: "default"}).Generate(context.Background(), segmentRequest(), nil); err != nil {
		t.Fatal(err)
	}
	if got := f.headers[0].Get("Anthropic-Beta"); got != RefusalFallbackBeta {
		t.Fatalf("beta header = %q", got)
	}
	if f.bodies[0]["fallbacks"] != "default" {
		t.Fatalf("fallbacks = %v", f.bodies[0]["fallbacks"])
	}
}

func jsonRoundTrip(t *testing.T, v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func TestAnthropic_ReplaySSE_RAI1(t *testing.T) {
	golden := strings.TrimSpace(string(fixture(t, "golden_segment.json")))
	t.Run("text deltas, thinking ignored, usage", func(t *testing.T) {
		f := newFakeAPI(t, serveSSE(t, "stream_segment.sse"))
		var deltas []string
		resp, err := f.adapter(t, Options{}).Generate(context.Background(), segmentRequest(), func(d llm.Delta) { deltas = append(deltas, d.JSONFragment) })
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.JSON) != golden || strings.Join(deltas, "") != golden || len(deltas) != 20 {
			t.Fatalf("json %q, %d deltas", resp.JSON, len(deltas))
		}
		want := llm.Usage{InputTokens: 412, OutputTokens: 187, CacheReadTokens: 3021}
		if resp.StopReason != llm.StopEndTurn || resp.Usage != want || resp.Model != "claude-sonnet-5-5" {
			t.Fatalf("resp = %+v", resp)
		}
	})
	t.Run("through the service: valid segment, cost from price table", func(t *testing.T) {
		f := newFakeAPI(t, serveSSE(t, "stream_segment.sse"))
		res, err := llm.NewService(f.adapter(t, Options{}), nil).ProposeSegment(context.Background(), "", llm.OfficialBrief)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Rule.Valid() || res.Meta.Attempts != 1 || res.Meta.CacheReadTokens != 3021 {
			t.Fatalf("meta = %+v", res.Meta)
		}
		// 412×$2 + 187×$10 + 3021×$0.20 per MTok
		if want := (412*2 + 187*10 + 3021*0.20) / 1e6; res.Meta.CostUSD < want-1e-9 || res.Meta.CostUSD > want+1e-9 {
			t.Fatalf("cost = %v, want %v", res.Meta.CostUSD, want)
		}
		if f.bodies[0]["system"] == nil || f.count() != 1 {
			t.Fatal("expected one request with a system prompt")
		}
	})
	t.Run("refusal is surfaced, not repaired", func(t *testing.T) {
		f := newFakeAPI(t, serveSSE(t, "stream_refusal.sse"))
		resp, err := f.adapter(t, Options{}).Generate(context.Background(), segmentRequest(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StopReason != llm.StopRefusal || resp.RefusalCategory != "cyber" || resp.Usage.CacheWriteTokens != 3021 {
			t.Fatalf("resp = %+v", resp)
		}
		_, err = llm.NewService(f.adapter(t, Options{}), nil).ProposeSegment(context.Background(), "", "x")
		var le *llm.Error
		if !errors.As(err, &le) || le.Code != "llm_refused" || le.Details[0].Code != "cyber" || f.count() != 2 {
			t.Fatalf("err = %v, requests = %d", err, f.count())
		}
	})
	t.Run("max_tokens truncation", func(t *testing.T) {
		f := newFakeAPI(t, serveSSE(t, "stream_max_tokens.sse"))
		resp, err := f.adapter(t, Options{}).Generate(context.Background(), segmentRequest(), nil)
		if err != nil || resp.StopReason != llm.StopMaxTokens || !strings.HasPrefix(golden, string(resp.JSON)) {
			t.Fatalf("resp = %+v, err = %v", resp, err)
		}
	})
	t.Run("malformed JSON goes through repairs to 422", func(t *testing.T) {
		f := newFakeAPI(t, serveSSE(t, "stream_malformed.sse"))
		_, err := llm.NewService(f.adapter(t, Options{}), nil).ProposeSegment(context.Background(), "", "x")
		var le *llm.Error
		if !errors.As(err, &le) || le.Code != "llm_invalid_output" || le.Details[0].Code != "invalid_json" {
			t.Fatalf("err = %v", err)
		}
		if f.count() != 1+llm.MaxRepairs {
			t.Fatalf("requests = %d", f.count())
		}
	})
	t.Run("429 with retry-after is retried by the SDK", func(t *testing.T) {
		ok := serveSSE(t, "stream_segment.sse")
		f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int) {
			if n == 0 {
				apiError(w, http.StatusTooManyRequests, "rate_limit_error")
				return
			}
			ok(w, r, n)
		})
		if _, err := f.adapter(t, Options{}).Generate(context.Background(), segmentRequest(), nil); err != nil || f.count() != 2 {
			t.Fatalf("err = %v, requests = %d", err, f.count())
		}
	})
	t.Run("529 overloaded: two retries, then llm_failed", func(t *testing.T) {
		f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) { apiError(w, 529, "overloaded_error") })
		_, err := f.adapter(t, Options{}).Generate(context.Background(), segmentRequest(), nil)
		var apiErr *sdk.Error
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 529 || f.count() != 3 {
			t.Fatalf("err = %v, requests = %d", err, f.count())
		}
		if strings.Contains(err.Error(), "test-key") {
			t.Fatal("the API key must never appear in an error")
		}
		_, err = llm.NewService(f.adapter(t, Options{}), nil).ProposeSegment(context.Background(), "", "x")
		var le *llm.Error
		if !errors.As(err, &le) || le.Code != "llm_failed" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("400 is not retried", func(t *testing.T) {
		f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) { apiError(w, 400, "invalid_request_error") })
		if _, err := f.adapter(t, Options{}).Generate(context.Background(), segmentRequest(), nil); err == nil || f.count() != 1 {
			t.Fatalf("err = %v, requests = %d", err, f.count())
		}
	})
	t.Run("stream cut before a stop reason is an error", func(t *testing.T) {
		full := string(fixture(t, "stream_segment.sse"))
		cut := before(t, full, "event: message_delta")
		f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, cut)
		})
		if _, err := f.adapter(t, Options{}).Generate(context.Background(), segmentRequest(), nil); err == nil {
			t.Fatal("want an error for a truncated stream")
		}
	})
}

func TestAnthropic_CancelStopsStream_RFLOW2(t *testing.T) {
	defer goleak.VerifyNone(t)
	full := string(fixture(t, "stream_segment.sse"))
	head := before(t, full, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}")
	head = head[:max(strings.LastIndex(head, "event: content_block_delta"), 0)] // a few text deltas, then silence
	served := make(chan struct{})
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		defer close(served)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, head)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the upstream keeps the stream open until the client goes away
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := f.adapter(t, Options{})
	errc := make(chan error, 1)
	go func() {
		_, err := a.Generate(ctx, segmentRequest(), func(llm.Delta) { cancel() })
		errc <- err
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Generate did not return after cancel")
	}
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("server handler still running: the request was not cancelled upstream")
	}
	f.client.CloseIdleConnections()
	f.srv.Close()
}

func before(t *testing.T, s, sep string) string {
	t.Helper()
	i := strings.Index(s, sep)
	if i < 0 {
		t.Fatalf("fixture lacks %q", sep)
	}
	return s[:i]
}

func TestAnthropic_NewRequiresKey_RAI1(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("empty key must fail fast")
	}
}

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store/storetest"
)

// The generation stream through the real HTTP handler (R-FLOW-2): the service-level tests prove deltas,
// repairs and cancellation inside llm.Service; these prove the wire format and that a browser closing the
// request stops the upstream call.

const streamedContent = `{"language":"tr","sms":{"text":"Merhaba {{first_name}}, sepetin seni bekliyor!"},` +
	`"email":{"subject":"{{first_name}}, indirim","body":"Merhaba {{first_name}},\n\nİndirim seni bekliyor."},"notes":""}`

func streamServer(t *testing.T, p llm.Provider) (*httptest.Server, string) {
	t.Helper()
	pool := storetest.NewDB(t)
	srv := httptest.NewServer(New(Deps{Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(p, nil),
		ProviderURL: "http://127.0.0.1:1", Version: "test"}).Handler())
	t.Cleanup(srv.Close)
	code, _, body := do(t, http.MethodPost, srv.URL+"/api/campaigns", `{"brief":"İstanbul'a indirim duyurusu"}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var c struct{ ID string }
	_ = json.Unmarshal(body, &c)
	rule := `{"rule":{"groups":[{"conditions":[{"field":"city","op":"eq","value":"İstanbul"}]}]}}`
	if code, _, body := do(t, http.MethodPut, srv.URL+"/api/campaigns/"+c.ID+"/segment", rule, nil); code != http.StatusOK {
		t.Fatalf("segment: %d %s", code, body)
	}
	return srv, c.ID
}

type frame struct{ event, data string }

// frames reads SSE frames until the body ends or stop returns true.
func frames(r io.Reader, stop func(frame) bool) []frame {
	var out []frame
	var cur frame
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.event != "" {
				out = append(out, cur)
				if stop != nil && stop(cur) {
					return out
				}
			}
			cur = frame{}
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data += strings.TrimPrefix(line, "data: ")
		}
	}
	return out
}

func TestGenerateStream_MetaDeltasResult_RFLOW2(t *testing.T) {
	// Split inside a multibyte rune (İ is 2 bytes) on purpose: the stream must still carry whole runes.
	i := strings.Index(streamedContent, "İ") + 1
	chunks := []string{streamedContent[:20], streamedContent[20:i], streamedContent[i:]}
	srv, id := streamServer(t, mock.NewScripted(mock.Step{Chunks: chunks}))

	resp, err := http.Post(srv.URL+"/api/campaigns/"+id+"/content/generate", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("status %d, content-type %q", resp.StatusCode, ct)
	}
	fs := frames(resp.Body, nil)
	var names []string
	var text strings.Builder
	for _, f := range fs {
		names = append(names, f.event)
		if f.event == "delta" {
			var d struct{ Text string }
			if err := json.Unmarshal([]byte(f.data), &d); err != nil {
				t.Fatalf("delta is not JSON: %v", err)
			}
			text.WriteString(d.Text)
		}
	}
	if len(names) < 4 || names[0] != "meta" || names[len(names)-1] != "result" || strings.Count(strings.Join(names, ","), "delta") < 2 {
		t.Fatalf("events %v, want meta, ≥ 2 delta, result", names)
	}
	if text.String() != streamedContent {
		t.Fatalf("deltas joined = %q, want the model text", text.String())
	}
	var res struct {
		Content struct {
			Version    int `json:"version"`
			Validation struct {
				ValidFor map[string]bool `json:"valid_for"`
			} `json:"validation"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(fs[len(fs)-1].data), &res); err != nil || res.Content.Version != 1 ||
		!res.Content.Validation.ValidFor["sms"] || !res.Content.Validation.ValidFor["email"] {
		t.Fatalf("result = %+v (%v)", res, err)
	}
}

func TestGenerateStream_ClientDisconnectCancelsUpstream_RFLOW2(t *testing.T) {
	p := mock.NewScripted(mock.Step{Chunks: []string{streamedContent[:30], streamedContent[30:]}, BlockAfterChunks: 1})
	srv, id := streamServer(t, p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/campaigns/"+id+"/content/generate", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got := frames(resp.Body, func(f frame) bool { return f.event == "delta" })
	if len(got) == 0 || got[len(got)-1].event != "delta" {
		t.Fatalf("no delta before the upstream blocked: %v", got)
	}
	cancel() // the browser navigates away
	resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for p.Cancelled() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("upstream call not cancelled after the client left (cancelled = %d)", p.Cancelled())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Nothing half-written was saved as a content version.
	code, _, body := do(t, http.MethodGet, srv.URL+"/api/campaigns/"+id, "", nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"content":null`) {
		t.Fatalf("campaign after a cancelled stream: %d %s", code, body)
	}
}

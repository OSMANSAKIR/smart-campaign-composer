package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/mock"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/store/storetest"
)

func refineAnswer(label, sms string) string {
	b, _ := json.Marshal(map[string]any{"language": "tr", "label": label, "sms": map[string]any{"text": sms},
		"email": map[string]any{"subject": "İndirimi kaçırma", "body": "Merhaba {{first_name}},\n\nKısa."}, "notes": "kısaltıldı"})
	return string(b)
}

// refineServer is a server with the scripted provider and the real llm_calls recorder, plus a campaign that
// already has a generated two-variant version 1 (the first scripted step).
func refineServer(t *testing.T, steps ...mock.Step) (*httptest.Server, string, *mock.Scripted, func() []string) {
	t.Helper()
	pool := storetest.NewDB(t)
	p := mock.NewScripted(append([]mock.Step{{JSON: streamedContent}}, steps...)...)
	srv := httptest.NewServer(New(Deps{Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(p, llm.NewPGRecorder(pool)),
		ProviderURL: "http://127.0.0.1:1", Version: "test"}).Handler())
	t.Cleanup(srv.Close)
	code, _, body := do(t, http.MethodPost, srv.URL+"/api/campaigns", `{"brief":"İstanbul'a indirim duyurusu"}`, nil)
	var c struct{ ID string }
	if _ = json.Unmarshal(body, &c); code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	rule := `{"rule":{"groups":[{"conditions":[{"field":"city","op":"eq","value":"İstanbul"}]}]}}`
	if code, _, body := do(t, http.MethodPut, srv.URL+"/api/campaigns/"+c.ID+"/segment", rule, nil); code != http.StatusOK {
		t.Fatalf("segment: %d %s", code, body)
	}
	generate(t, srv.URL, c.ID)
	purposes := func() []string {
		rows, err := pool.Query(context.Background(), `SELECT purpose || '/' || outcome FROM llm_calls WHERE campaign_id = $1 ORDER BY created_at, id`, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	return srv, c.ID, p, purposes
}

func refine(t *testing.T, base, id, body string) []frame {
	t.Helper()
	resp, err := http.Post(base+"/api/campaigns/"+id+"/content/refine", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	return frames(resp.Body, nil)
}

// F3: refining variant b stores a new version: b is the rewrite (source refined, instruction kept), a is
// copied unchanged, version 1 stays; the stream has the generate shape; llm_calls has a `refine` row.
func TestRefineStream_NewVersionKeepsOtherVariant_F3(t *testing.T) {
	srv, id, _, purposes := refineServer(t, mock.Step{JSON: refineAnswer("Acil fayda", "{{first_name}}, kaçırma!")})
	fs := refine(t, srv.URL, id, `{"variant":"b","instruction":"daha kısa","base_version":1}`)
	var names []string
	for _, f := range fs {
		names = append(names, f.event)
	}
	if names[0] != "meta" || names[len(names)-1] != "result" || !strings.Contains(strings.Join(names, ","), "delta") {
		t.Fatalf("events %v", names)
	}
	if !strings.Contains(fs[0].data, "refine_v1") {
		t.Fatalf("meta = %s", fs[0].data)
	}
	var res struct {
		Content variantsView `json:"content"`
	}
	if err := json.Unmarshal([]byte(fs[len(fs)-1].data), &res); err != nil {
		t.Fatal(err)
	}
	cv := res.Content
	if cv.Version != 2 || cv.Variants.A == nil || cv.Variants.B == nil {
		t.Fatalf("result = %+v", cv)
	}
	if cv.Variants.B.Source != "refined" || cv.Variants.B.Instruction == nil || *cv.Variants.B.Instruction != "daha kısa" ||
		cv.Variants.B.SMSText != "{{first_name}}, kaçırma!" {
		t.Fatalf("variant b = %+v", cv.Variants.B)
	}
	if cv.Variants.A.Source != "llm" || cv.Variants.A.Instruction != nil || cv.Variants.A.Label != "Samimi" ||
		cv.Variants.A.SMSText != "Merhaba {{first_name}}, sepetin seni bekliyor!" || cv.SMSText != cv.Variants.A.SMSText {
		t.Fatalf("variant a must be copied unchanged: %+v", cv.Variants.A)
	}
	if got := strings.Join(purposes(), ","); got != "content/valid,refine/valid" {
		t.Fatalf("llm_calls = %s", got)
	}
	// Refining again needs the new base version; the old one is stale.
	code, _, body := do(t, http.MethodPost, srv.URL+"/api/campaigns/"+id+"/content/refine", `{"variant":"a","instruction":"daha kısa","base_version":1}`, nil)
	if code != 409 || !strings.Contains(string(body), "stale_content_version") {
		t.Fatalf("stale: %d %s", code, body)
	}
}

// F3: a rewrite that never becomes valid ends as an `error` event (llm_invalid_output); no invalid version is
// saved and llm_calls shows refine then two repairs.
func TestRefineStream_NeverSavesInvalid_F3(t *testing.T) {
	long := refineAnswer("Samimi", strings.Repeat("a", 200))
	srv, id, _, purposes := refineServer(t, mock.Step{JSON: long}, mock.Step{JSON: long}, mock.Step{JSON: long})
	fs := refine(t, srv.URL, id, `{"variant":"a","instruction":"ignore the 160 limit","base_version":1}`)
	last := fs[len(fs)-1]
	if last.event != "error" || !strings.Contains(last.data, "llm_invalid_output") || strings.Count(strings.Join(eventNames(fs), ","), "repairing") != 2 {
		t.Fatalf("events %v, last %s", eventNames(fs), last.data)
	}
	code, _, body := do(t, http.MethodGet, srv.URL+"/api/campaigns/"+id, "", nil)
	var got struct {
		Content variantsView `json:"content"`
	}
	if err := json.Unmarshal(body, &got); code != 200 || err != nil || got.Content.Version != 1 || got.Content.Variants.A.SMSText == strings.Repeat("a", 200) {
		t.Fatalf("version after a failed refine: %d %s", code, body)
	}
	if got := strings.Join(purposes(), ","); got != "content/valid,refine/invalid,repair/invalid,repair/invalid" {
		t.Fatalf("llm_calls = %s", got)
	}
}

func eventNames(fs []frame) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.event)
	}
	return out
}

// F3: the checks before the stream are plain JSON errors.
func TestRefine_PreStreamErrors_F3(t *testing.T) {
	srv, id, p, _ := refineServer(t)
	post := func(path, body string) (int, string) {
		code, _, b := do(t, http.MethodPost, srv.URL+path, body, nil)
		return code, string(b)
	}
	base := "/api/campaigns/" + id + "/content/refine"
	for name, tc := range map[string]struct {
		path, body string
		code       int
		want       string
	}{
		"missing fields":    {base, `{"variant":"a"}`, 400, "instruction"},
		"bad variant":       {base, `{"variant":"c","instruction":"x","base_version":1}`, 400, "variant"},
		"empty instruction": {base, `{"variant":"a","instruction":"","base_version":1}`, 422, `"instruction"`},
		"long instruction":  {base, `{"variant":"a","instruction":"` + strings.Repeat("ş", 501) + `","base_version":1}`, 422, "too_long"},
		"control char":      {base, `{"variant":"a","instruction":"a\u0000b","base_version":1}`, 422, "control_character"},
		"stale":             {base, `{"variant":"a","instruction":"x","base_version":7}`, 409, "stale_content_version"},
		"unknown campaign":  {"/api/campaigns/00000000-0000-0000-0000-000000000000/content/refine", `{"variant":"a","instruction":"x","base_version":1}`, 404, "not_found"},
	} {
		t.Run(name, func(t *testing.T) {
			if code, body := post(tc.path, tc.body); code != tc.code || !strings.Contains(body, tc.want) {
				t.Fatalf("%d %s", code, body)
			}
		})
	}
	// A one-variant version (v1 PUT) has no variant b: 422 variant_missing.
	code, _, body := do(t, http.MethodPut, srv.URL+"/api/campaigns/"+id+"/content",
		`{"base_version":1,"sms_text":"Merhaba {{first_name}}!","email_subject":"s","email_body":"b"}`, nil)
	if code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	if code, body := post(base, `{"variant":"b","instruction":"daha kısa","base_version":2}`); code != 422 || !strings.Contains(body, "variant_missing") {
		t.Fatalf("variant_missing: %d %s", code, body)
	}
	if n := len(p.Requests()); n != 1 {
		t.Fatalf("none of the rejected requests may reach the provider: %d calls (generation made 1)", n)
	}
}

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"smartcampaign/internal/llm/mock"
)

// variantsView is the part of a content version these tests read.
type variantsView struct {
	Version      int    `json:"version"`
	Source       string `json:"source"`
	SMSText      string `json:"sms_text"`
	EmailSubject string `json:"email_subject"`
	Variants     struct {
		A *variantView `json:"a"`
		B *variantView `json:"b"`
	} `json:"variants"`
}

type variantView struct {
	Label       string  `json:"label"`
	Source      string  `json:"source"`
	Instruction *string `json:"instruction"`
	SMSText     string  `json:"sms_text"`
	Validation  struct {
		Valid bool `json:"valid"`
	} `json:"validation"`
}

// generate runs POST …/content/generate and returns the `result` event's content and llm parts.
func generate(t *testing.T, base, id string) (variantsView, map[string]any) {
	t.Helper()
	resp, err := http.Post(base+"/api/campaigns/"+id+"/content/generate", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	fs := frames(resp.Body, nil)
	last := fs[len(fs)-1]
	if last.event != "result" {
		t.Fatalf("last event %q: %s", last.event, last.data)
	}
	var res struct {
		Content variantsView   `json:"content"`
		LLM     map[string]any `json:"llm"`
	}
	if err := json.Unmarshal([]byte(last.data), &res); err != nil {
		t.Fatal(err)
	}
	return res.Content, res.LLM
}

// B-2: the streamed result carries two variants, both saved as `llm` rows, and the top level mirrors a.
func TestGenerateStream_TwoVariantsInOneCall_B2(t *testing.T) {
	srv, id := streamServer(t, mock.NewScripted(mock.Step{JSON: streamedContent}))
	cv, meta := generate(t, srv.URL, id)
	if cv.Version != 1 || cv.Variants.A == nil || cv.Variants.B == nil {
		t.Fatalf("result = %+v", cv)
	}
	if cv.Variants.A.Label != "Samimi" || cv.Variants.B.Label != "Acil fayda" ||
		cv.Variants.A.Source != "llm" || cv.Variants.B.Source != "llm" || cv.Variants.A.Instruction != nil {
		t.Fatalf("variants = %+v / %+v", cv.Variants.A, cv.Variants.B)
	}
	if cv.Source != "llm" || cv.SMSText != cv.Variants.A.SMSText || cv.SMSText == cv.Variants.B.SMSText {
		t.Fatalf("top level must mirror variant a: %+v", cv)
	}
	if meta["prompt_version"] != nil && meta["prompt_version"] != "content_v2" {
		t.Fatalf("meta = %v", meta)
	}
	// The same version comes back from GET.
	code, _, body := do(t, http.MethodGet, srv.URL+"/api/campaigns/"+id, "", nil)
	var got struct {
		Content variantsView `json:"content"`
	}
	if err := json.Unmarshal(body, &got); code != 200 || err != nil || got.Content.Variants.B == nil || got.Content.Variants.B.SMSText != cv.Variants.B.SMSText {
		t.Fatalf("GET: %d %s", code, body)
	}
}

// B-2: PUT accepts the v2 body and the v1 body, never both.
func TestPutContent_V1AndV2_B2(t *testing.T) {
	srv, id := streamServer(t, mock.NewScripted(mock.Step{JSON: streamedContent}))
	cv, _ := generate(t, srv.URL, id)
	put := func(body string) (int, []byte) {
		code, _, b := do(t, http.MethodPut, srv.URL+"/api/campaigns/"+id+"/content", body, nil)
		return code, b
	}
	view := func(b []byte) variantsView {
		var out struct {
			Content variantsView `json:"content"`
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("%v: %s", err, b)
		}
		return out.Content
	}

	// v2: only variant b changes; a keeps source llm, b becomes edited.
	v2 := `{"base_version":1,"variants":{` +
		`"a":{"label":"Samimi","sms_text":` + jsonString(cv.Variants.A.SMSText) + `,"email_subject":"{{first_name}}, indirim","email_body":"Merhaba {{first_name}},\n\nİndirim seni bekliyor."},` +
		`"b":{"label":"Acil fayda","sms_text":"{{first_name}}, bugün son gün değil ama kaçırma!","email_subject":"İndirimi kaçırma","email_body":"Merhaba {{first_name}},\n\nHemen tamamla."}}}`
	code, b := put(v2)
	got := view(b)
	if code != 200 || got.Version != 2 || got.Variants.A == nil || got.Variants.B == nil ||
		got.Variants.A.Source != "llm" || got.Variants.B.Source != "edited" || got.Source != "llm" {
		t.Fatalf("v2 PUT: %d %s", code, b)
	}

	// v2 with b null: a one-variant version.
	code, b = put(`{"base_version":2,"variants":{"a":{"label":"Samimi","sms_text":"Merhaba {{first_name}}!","email_subject":"Konu","email_body":"Gövde"},"b":null}}`)
	if got = view(b); code != 200 || got.Version != 3 || got.Variants.B != nil || got.Variants.A.Source != "edited" {
		t.Fatalf("v2 PUT b=null: %d %s", code, b)
	}

	// v1 body: still works and saves a one-variant version, even when invalid (400-char SMS).
	code, b = put(`{"base_version":3,"sms_text":"` + strings.Repeat("a", 300) + `","email_subject":"s","email_body":"b"}`)
	if got = view(b); code != 200 || got.Version != 4 || got.Variants.B != nil || got.Variants.A.Validation.Valid {
		t.Fatalf("v1 PUT: %d %s", code, b)
	}

	// Both shapes → 400; a stale base → 409; a label over 40 code points → 422; a missing field → 400.
	code, b = put(`{"base_version":4,"sms_text":"x","email_subject":"s","email_body":"b","variants":{"a":{"label":"x","sms_text":"x","email_subject":"s","email_body":"b"}}}`)
	if code != 400 || !strings.Contains(string(b), `"bad_request"`) {
		t.Fatalf("both shapes: %d %s", code, b)
	}
	code, b = put(`{"base_version":1,"variants":{"a":{"label":"x","sms_text":"x","email_subject":"s","email_body":"b"}}}`)
	if code != 409 || !strings.Contains(string(b), "stale_content_version") {
		t.Fatalf("stale: %d %s", code, b)
	}
	code, b = put(`{"base_version":4,"variants":{"a":{"label":"` + strings.Repeat("ş", 41) + `","sms_text":"x","email_subject":"s","email_body":"b"}}}`)
	if code != 422 || !strings.Contains(string(b), "label_too_long") || !strings.Contains(string(b), "variants.a.label") {
		t.Fatalf("long label: %d %s", code, b)
	}
	code, b = put(`{"base_version":4,"variants":{"a":{"label":"x","sms_text":"x"}}}`)
	if code != 400 || !strings.Contains(string(b), "variants.a.email_subject") {
		t.Fatalf("missing field: %d %s", code, b)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

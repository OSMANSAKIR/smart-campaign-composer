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

// localizedAnswer is a scripted localize_v1 answer; sms overrides variant a's SMS text.
func localizedAnswer(sms string) string {
	b, _ := json.Marshal(map[string]any{"language": "en", "notes": "adapted", "variants": map[string]any{
		"a": map[string]any{"label": "Friendly", "sms": map[string]any{"text": sms},
			"email": map[string]any{"subject": "{{first_name}}, discount", "body": "Hi {{first_name}},\n\nThe discount is waiting for you."}},
		"b": map[string]any{"label": "Urgent benefit", "sms": map[string]any{"text": "{{first_name}}, don't miss the discount!"},
			"email": map[string]any{"subject": "Don't miss the discount", "body": "Hi {{first_name}},\n\nFinish now."}},
	}})
	return string(b)
}

const goodEN = "Hi {{first_name}}, your cart is waiting!"

// localizeServer is a server with the scripted provider, the real llm_calls recorder, 30 Istanbul users (10 en,
// 20 tr; every tenth has no SMS consent) and a campaign with a generated two-variant version 1.
func localizeServer(t *testing.T, rec func(llm.Recorder) llm.Recorder, steps ...mock.Step) (*httptest.Server, string, *mock.Scripted, func() []string) {
	t.Helper()
	pool := storetest.NewDB(t)
	if _, err := pool.Exec(context.Background(), `INSERT INTO users (seed_key, first_name, email, phone, city, locale, birth_date, signup_at,
			loyalty_tier, sms_consent, email_consent)
		SELECT 'b3-' || i, 'Ayşe' || i, 'b3' || i || '@example.com', '+90532' || lpad(i::text, 7, '0'), 'İstanbul',
		       CASE WHEN i % 3 = 0 THEN 'en' ELSE 'tr' END, date '1990-01-01', now(), 'bronze', i % 10 <> 0, true
		FROM generate_series(1, 30) AS i`); err != nil {
		t.Fatal(err)
	}
	p := mock.NewScripted(append([]mock.Step{{JSON: streamedContent}}, steps...)...)
	var recorder llm.Recorder = llm.NewPGRecorder(pool)
	if rec != nil {
		recorder = rec(recorder)
	}
	srv := httptest.NewServer(New(Deps{Pool: pool, Metrics: metrics.New(), LLM: llm.NewService(p, recorder),
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

func postLocalize(t *testing.T, base, id, body string) (int, []frame, string) {
	t.Helper()
	resp, err := http.Post(base+"/api/campaigns/"+id+"/content/localize", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, nil, sb.String()
	}
	return resp.StatusCode, frames(resp.Body, nil), ""
}

type localizedView struct {
	Content struct {
		Version  int `json:"version"`
		Variants struct {
			A struct {
				Locale    string `json:"locale"`
				SMSText   string `json:"sms_text"`
				Localized map[string]struct {
					Label      string `json:"label"`
					Source     string `json:"source"`
					SMSText    string `json:"sms_text"`
					Stale      bool   `json:"stale"`
					Validation struct {
						Valid bool `json:"valid"`
					} `json:"validation"`
				} `json:"localized"`
			} `json:"a"`
			B struct {
				Localized map[string]struct {
					SMSText string `json:"sms_text"`
				} `json:"localized"`
			} `json:"b"`
		} `json:"variants"`
	} `json:"content"`
	LLM struct {
		PromptVersion string `json:"prompt_version"`
		CostRecorded  bool   `json:"cost_recorded"`
	} `json:"llm"`
}

// B-3: localize streams like refine (meta, delta, result) and saves a new version with the adaptation; the
// primary copy is unchanged, the adaptation is current and valid; llm_calls has a `localize` row and the
// result says cost_recorded.
func TestLocalizeStream_NewVersionWithAdaptation_B3(t *testing.T) {
	srv, id, p, purposes := localizeServer(t, nil, mock.Step{JSON: localizedAnswer(goodEN)})
	code, fs, _ := postLocalize(t, srv.URL, id, `{"target_locale":"en"}`)
	names := eventNames(fs)
	if code != 200 || names[0] != "meta" || names[len(names)-1] != "result" || !strings.Contains(strings.Join(names, ","), "delta") || !strings.Contains(fs[0].data, "localize_v1") {
		t.Fatalf("status %d events %v meta %s", code, names, fs[0].data)
	}
	var res localizedView
	if err := json.Unmarshal([]byte(fs[len(fs)-1].data), &res); err != nil {
		t.Fatal(err)
	}
	a := res.Content.Variants.A
	en := a.Localized["en"]
	if res.Content.Version != 2 || a.Locale != "tr" || a.SMSText != "Merhaba {{first_name}}, sepetin seni bekliyor!" || en.SMSText != goodEN ||
		en.Stale || !en.Validation.Valid || en.Source != "llm" || en.Label != "Friendly" || res.Content.Variants.B.Localized["en"].SMSText == "" ||
		!res.LLM.CostRecorded || res.LLM.PromptVersion != "localize_v1" {
		t.Fatalf("result = %+v", res)
	}
	if got := strings.Join(purposes(), ","); got != "content/valid,localize/valid" {
		t.Fatalf("llm_calls = %s", got)
	}
	// The model saw the primary copy as data, the target language and the localize schema.
	req := p.Requests()[1]
	if req.Purpose != llm.PurposeLocalize || !strings.Contains(req.Messages[0].Text, "Target language: en") || !strings.Contains(req.Messages[0].Text, "<source_variants>") {
		t.Fatalf("request = %+v", req.Messages[0].Text)
	}
	// GET returns the same adaptation with the campaign.
	code, _, body := do(t, http.MethodGet, srv.URL+"/api/campaigns/"+id, "", nil)
	var got localizedView
	if err := json.Unmarshal(body, &got); code != 200 || err != nil || got.Content.Variants.A.Localized["en"].SMSText != goodEN {
		t.Fatalf("GET: %d %s", code, body)
	}
}

// B-3: a variable mismatch goes through the repair loop (repairing event, purpose repair) and ends valid; the
// repair budget is the content budget, and when it runs out the stream ends with llm_invalid_output and no
// version is saved.
func TestLocalizeStream_RepairAndInvalid_B3(t *testing.T) {
	t.Run("repaired", func(t *testing.T) {
		srv, id, _, purposes := localizeServer(t, nil, mock.Step{JSON: localizedAnswer("Hi, your cart is waiting!")}, mock.Step{JSON: localizedAnswer(goodEN)})
		_, fs, _ := postLocalize(t, srv.URL, id, `{"target_locale":"en"}`)
		names := strings.Join(eventNames(fs), ",")
		if strings.Count(names, "repairing") != 1 || fs[len(fs)-1].event != "result" {
			t.Fatalf("events %s", names)
		}
		if got := strings.Join(purposes(), ","); got != "content/valid,localize/invalid,repair/repaired" {
			t.Fatalf("llm_calls = %s", got)
		}
	})
	t.Run("never valid", func(t *testing.T) {
		bad := localizedAnswer("Hi, your cart is waiting!")
		srv, id, _, purposes := localizeServer(t, nil, mock.Step{JSON: bad}, mock.Step{JSON: bad}, mock.Step{JSON: bad})
		_, fs, _ := postLocalize(t, srv.URL, id, `{"target_locale":"en"}`)
		last := fs[len(fs)-1]
		if last.event != "error" || !strings.Contains(last.data, "llm_invalid_output") || !strings.Contains(last.data, "variable_mismatch") ||
			strings.Count(strings.Join(eventNames(fs), ","), "repairing") != 2 {
			t.Fatalf("events %v, last %s", eventNames(fs), last.data)
		}
		code, _, body := do(t, http.MethodGet, srv.URL+"/api/campaigns/"+id, "", nil)
		var got localizedView
		if err := json.Unmarshal(body, &got); code != 200 || err != nil || got.Content.Version != 1 || len(got.Content.Variants.A.Localized) != 0 {
			t.Fatalf("after a failed localize: %d %s", code, body)
		}
		if got := strings.Join(purposes(), ","); got != "content/valid,localize/invalid,repair/invalid,repair/invalid" {
			t.Fatalf("llm_calls = %s", got)
		}
	})
}

// B-3 / F7: a provider rejection (401) is mapped to llm_provider_rejected in the error event, without the
// provider's body.
func TestLocalizeStream_ProviderRejected_F7(t *testing.T) {
	srv, id, _, _ := localizeServer(t, nil, mock.Step{Err: &llm.StatusError{Status: 401, Body: "org_secret credit balance"}})
	_, fs, _ := postLocalize(t, srv.URL, id, `{"target_locale":"en"}`)
	last := fs[len(fs)-1]
	if last.event != "error" || !strings.Contains(last.data, "llm_provider_rejected") || strings.Contains(last.data, "org_secret") {
		t.Fatalf("last event %s %s", last.event, last.data)
	}
}

// B-3 / F6: with a failing llm_calls recorder the adaptation still succeeds and says cost_recorded=false.
func TestLocalizeStream_CostRecordedFalse_F6(t *testing.T) {
	srv, id, _, _ := localizeServer(t, func(llm.Recorder) llm.Recorder { return brokenRecorder{} }, mock.Step{JSON: localizedAnswer(goodEN)})
	_, fs, _ := postLocalize(t, srv.URL, id, `{"target_locale":"en"}`)
	var res localizedView
	if err := json.Unmarshal([]byte(fs[len(fs)-1].data), &res); fs[len(fs)-1].event != "result" || err != nil || res.LLM.CostRecorded {
		t.Fatalf("last %s %s", fs[len(fs)-1].event, fs[len(fs)-1].data)
	}
}

// B-3: the checks before the stream are plain JSON errors, and none of them reaches the provider.
func TestLocalize_PreStreamErrors_B3(t *testing.T) {
	srv, id, p, _ := localizeServer(t, nil)
	base := "/api/campaigns/" + id + "/content/localize"
	for name, tc := range map[string]struct {
		path, body string
		code       int
		want       string
	}{
		"missing target":   {base, `{}`, 400, "target_locale"},
		"bad json":         {base, `{`, 400, "bad_request"},
		"own language":     {base, `{"target_locale":"tr"}`, 422, "target_locale"},
		"unknown locale":   {base, `{"target_locale":"de"}`, 422, "validation_failed"},
		"unknown campaign": {"/api/campaigns/00000000-0000-0000-0000-000000000000/content/localize", `{"target_locale":"en"}`, 404, "not_found"},
		"malformed id":     {"/api/campaigns/nope/content/localize", `{"target_locale":"en"}`, 404, "not_found"},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, body := do(t, http.MethodPost, srv.URL+tc.path, tc.body, nil)
			if code != tc.code || !strings.Contains(string(body), tc.want) {
				t.Fatalf("%d %s", code, body)
			}
		})
	}
	code, _, body := do(t, http.MethodPut, srv.URL+"/api/campaigns/"+id+"/content",
		`{"base_version":1,"sms_text":"Merhaba {{first_name}}!","email_subject":"s","email_body":"b"}`, nil)
	if code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	if code, _, body := do(t, http.MethodPost, srv.URL+base, `{"target_locale":"en"}`, nil); code != 422 || !strings.Contains(string(body), "variant_missing") {
		t.Fatalf("one-variant version: %d %s", code, body)
	}
	if n := len(p.Requests()); n != 1 {
		t.Fatalf("none of the rejected requests may reach the provider: %d calls (generation made 1)", n)
	}
}

func approve(t *testing.T, base, id, key, body string) (int, string) {
	t.Helper()
	code, _, b := do(t, http.MethodPost, base+"/api/campaigns/"+id+"/approve", body, map[string]string{"Idempotency-Key": key})
	return code, string(b)
}

// B-3 contract: PUT content takes a copy per variant per locale (validated per locale), keeps the adaptation
// of a locale it does not mention, marks it stale after a primary edit; approve takes language_mode, answers
// 422 missing_localization with details until every locale has a current valid copy, and 202 with
// language_mode and the per-locale progress afterwards.
func TestLocalizeContract_PutApproveAndProgress_B3(t *testing.T) {
	srv, id, _, _ := localizeServer(t, nil, mock.Step{JSON: localizedAnswer(goodEN)})
	approveBody := func(ver int, mode string) string {
		b, _ := json.Marshal(map[string]any{"channels": []string{"sms", "email"}, "content_version": ver, "variant": "ab", "language_mode": mode})
		return string(b)
	}
	// No adaptation yet: 422 with details naming each missing copy; campaign mode is fine in principle.
	code, body := approve(t, srv.URL, id, "k0", approveBody(1, "recipient"))
	if code != 422 || !strings.Contains(body, `"missing_localization"`) || !strings.Contains(body, `"variants.a.localized.en"`) || !strings.Contains(body, `"missing"`) {
		t.Fatalf("approve without adaptation: %d %s", code, body)
	}
	if code, body := approve(t, srv.URL, id, "k0", approveBody(1, "everyone")); code != 400 || !strings.Contains(body, "language_mode") {
		t.Fatalf("bad language_mode: %d %s", code, body)
	}
	if _, fs, _ := postLocalize(t, srv.URL, id, `{"target_locale":"en"}`); fs[len(fs)-1].event != "result" {
		t.Fatalf("localize: %v", fs)
	}

	put := func(body string) (int, string) {
		code, _, b := do(t, http.MethodPut, srv.URL+"/api/campaigns/"+id+"/content", body, nil)
		return code, string(b)
	}
	view := func(s string) localizedView {
		var v localizedView
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("%v: %s", err, s)
		}
		return v
	}
	variant := func(sms string, extra string) string {
		return `{"label":"L","sms_text":"` + sms + `","email_subject":"s {{first_name}}","email_body":"b {{first_name}}"` + extra + `}`
	}
	// Editing a primary copy without mentioning locales: the en copy is carried over and stale.
	code, body = put(`{"base_version":2,"variants":{"a":` + variant("Merhaba {{first_name}}, yeni!", "") + `,"b":` + variant("{{first_name}} b", "") + `}}`)
	v3 := view(body)
	if code != 200 || v3.Content.Version != 3 || !v3.Content.Variants.A.Localized["en"].Stale || v3.Content.Variants.A.Localized["en"].SMSText != goodEN {
		t.Fatalf("primary edit: %d %s", code, body)
	}
	if code, body := approve(t, srv.URL, id, "k1", approveBody(3, "recipient")); code != 422 || !strings.Contains(body, `"stale"`) {
		t.Fatalf("approve with a stale adaptation: %d %s", code, body)
	}
	// Editing an adaptation per locale: validated with the locale's rules (too long for sms is saved, reported).
	long := strings.Repeat("x", 300)
	code, body = put(`{"base_version":3,"variants":{"a":` + variant("Merhaba {{first_name}}, yeni!", `,"localized":{"en":`+variant(long, "")+`}`) + `,"b":` + variant("{{first_name}} b", `,"localized":{"en":`+variant("{{first_name}} b", "")+`}`) + `}}`)
	v4 := view(body)
	if code != 200 || v4.Content.Variants.A.Localized["en"].Validation.Valid || v4.Content.Variants.A.Localized["en"].Stale || v4.Content.Variants.A.Localized["en"].Source != "edited" {
		t.Fatalf("localized edit: %d %s", code, body)
	}
	if code, body := approve(t, srv.URL, id, "k2", approveBody(4, "recipient")); code != 422 || !strings.Contains(body, `variants.a.localized.en.sms_text`) {
		t.Fatalf("approve with an invalid adaptation: %d %s", code, body)
	}
	// Bad bodies.
	for name, tc := range map[string]struct {
		body string
		code int
		want string
	}{
		"unknown locale":    {`{"base_version":4,"variants":{"a":` + variant("x", `,"localized":{"de":`+variant("y", "")+`}`) + `}}`, 422, "variants.a.localized.de"},
		"own language":      {`{"base_version":4,"variants":{"a":` + variant("x", `,"localized":{"tr":`+variant("y", "")+`}`) + `}}`, 422, "variants.a.localized.tr"},
		"missing field":     {`{"base_version":4,"variants":{"a":` + variant("x", `,"localized":{"en":{"label":"L"}}`) + `}}`, 400, "variants.a.localized.en.sms_text"},
		"null localization": {`{"base_version":4,"variants":{"a":` + variant("x", `,"localized":{"en":null}`) + `}}`, 400, "variants.a.localized.en"},
	} {
		t.Run(name, func(t *testing.T) {
			if code, body := put(tc.body); code != tc.code || !strings.Contains(body, tc.want) {
				t.Fatalf("%d %s", code, body)
			}
		})
	}
	// A valid adaptation: approved in recipient mode.
	code, body = put(`{"base_version":4,"variants":{"a":` + variant("Merhaba {{first_name}}, yeni!", `,"localized":{"en":`+variant("Hi {{first_name}}, new!", "")+`}`) + `,"b":` + variant("{{first_name}} b", "") + `}}`)
	v5 := view(body)
	if code != 200 || v5.Content.Version != 5 || v5.Content.Variants.A.Localized["en"].Stale {
		t.Fatalf("valid adaptation: %d %s", code, body)
	}
	code, body = approve(t, srv.URL, id, "k3", approveBody(5, "recipient"))
	var ap struct {
		Campaign struct {
			LanguageMode string `json:"language_mode"`
			Status       string `json:"status"`
		} `json:"campaign"`
		Enqueued map[string]int `json:"enqueued"`
	}
	if err := json.Unmarshal([]byte(body), &ap); code != 202 || err != nil || ap.Campaign.LanguageMode != "recipient" || ap.Enqueued["email"] != 30 || ap.Enqueued["sms"] != 27 {
		t.Fatalf("approve: %d %s", code, body)
	}
	if code, body2 := approve(t, srv.URL, id, "k3", approveBody(5, "recipient")); code != 202 || !strings.Contains(body2, `"language_mode":"recipient"`) {
		t.Fatalf("replay: %d %s", code, body2)
	}
	// The campaign carries the per-locale progress.
	code, _, b := do(t, http.MethodGet, srv.URL+"/api/campaigns/"+id, "", nil)
	var camp struct {
		LanguageMode string `json:"language_mode"`
		Progress     struct {
			Channels map[string]struct {
				Total   int `json:"total"`
				Locales map[string]struct {
					Total  int `json:"total"`
					Sent   int `json:"sent"`
					Failed int `json:"failed"`
				} `json:"locales"`
			} `json:"channels"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(b, &camp); code != 200 || err != nil || camp.LanguageMode != "recipient" {
		t.Fatalf("GET: %d %s", code, b)
	}
	for ch, want := range map[string][2]int{"email": {20, 10}, "sms": {18, 9}} {
		l := camp.Progress.Channels[ch].Locales
		if l["tr"].Total != want[0] || l["en"].Total != want[1] || l["tr"].Total+l["en"].Total != camp.Progress.Channels[ch].Total {
			t.Errorf("%s locales = %+v", ch, l)
		}
	}
}

// B-3: the audience of a stored segment reports consented counts per locale (api.md §9.10).
func TestSegmentAudience_ByLocale_B3(t *testing.T) {
	srv, id, _, _ := localizeServer(t, nil)
	code, _, body := do(t, http.MethodGet, srv.URL+"/api/campaigns/"+id, "", nil)
	var c struct {
		Segment struct {
			Audience struct {
				Total          int `json:"total"`
				SMSReachable   int `json:"sms_reachable"`
				EmailReachable int `json:"email_reachable"`
				ByLocale       map[string]struct {
					Total          int `json:"total"`
					SMSReachable   int `json:"sms_reachable"`
					EmailReachable int `json:"email_reachable"`
				} `json:"by_locale"`
			} `json:"audience"`
		} `json:"segment"`
	}
	if err := json.Unmarshal(body, &c); code != 200 || err != nil {
		t.Fatalf("%d %s", code, body)
	}
	a := c.Segment.Audience
	if a.Total != 30 || a.ByLocale["en"].Total != 10 || a.ByLocale["tr"].Total != 20 || a.ByLocale["en"].SMSReachable != 9 || a.ByLocale["tr"].SMSReachable != 18 ||
		a.ByLocale["en"].EmailReachable+a.ByLocale["tr"].EmailReachable != a.EmailReachable {
		t.Fatalf("audience = %+v", a)
	}
	// The editor's preview carries the same breakdown.
	code, _, body = do(t, http.MethodPost, srv.URL+"/api/segment/preview", `{"rule":{"groups":[{"conditions":[{"field":"city","op":"eq","value":"İstanbul"}]}]}}`, nil)
	if code != 200 || !strings.Contains(string(body), `"by_locale"`) || !strings.Contains(string(body), `"en":{"total":10`) {
		t.Fatalf("preview: %d %s", code, body)
	}
}

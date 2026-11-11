package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/segment"
)

// b3Campaign is a Turkish campaign over 30 synthetic Istanbul users: every third one reads English (10 en,
// 20 tr), every tenth has no SMS consent. It has a generated two-variant version 1 (the demo model).
func b3Campaign(t *testing.T) (*Service, *pgxpool.Pool, Campaign) {
	t.Helper()
	svc, pool := newService(t, 0)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (seed_key, first_name, email, phone, city, locale, birth_date, signup_at,
			loyalty_tier, sms_consent, email_consent)
		SELECT 'b3-' || i, 'Ayşe' || i, 'b3' || i || '@example.com', '+90532' || lpad(i::text, 7, '0'), 'İstanbul',
		       CASE WHEN i % 3 = 0 THEN 'en' ELSE 'tr' END, date '1990-01-01', now(), 'bronze', i % 10 <> 0, true
		FROM generate_series(1, 30) AS i`); err != nil {
		t.Fatal(err)
	}
	camp, err := svc.Create(ctx, "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder.")
	if err != nil {
		t.Fatal(err)
	}
	var r segment.Rule
	if err := json.Unmarshal([]byte(`{"groups":[{"conditions":[{"field":"city","op":"eq","value":"İstanbul"}]}]}`), &r); err != nil {
		t.Fatal(err)
	}
	if camp, err = svc.PutSegment(ctx, camp.ID, r); err != nil {
		t.Fatal(err)
	}
	in, err := svc.ContentInput(ctx, camp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GenerateContent(ctx, camp.ID, in, nil, nil); err != nil {
		t.Fatal(err)
	}
	camp, err = svc.Get(ctx, camp.ID)
	if err != nil {
		t.Fatal(err)
	}
	return svc, pool, camp
}

func localize(t *testing.T, svc *Service, id, target string) ContentVersion {
	t.Helper()
	plan, err := svc.PlanLocalize(context.Background(), id, target)
	if err != nil {
		t.Fatal(err)
	}
	cv, meta, err := svc.RunLocalize(context.Background(), plan, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta.PromptVersion != "localize_v1" || !meta.CostRecorded {
		t.Fatalf("meta = %+v", meta)
	}
	return cv
}

// B-3: localizing stores a NEW version with the primary rows copied and the adapted rows added; the earlier
// version stays as history; the adapted copy is valid, not stale and differs from the primary.
func TestLocalize_NewVersionKeepsPrimaryAndAddsLocale_B3(t *testing.T) {
	svc, pool, camp := b3Campaign(t)
	ctx := context.Background()
	before := camp.Content
	cv := localize(t, svc, camp.ID, "en")
	if cv.Version != before.Version+1 {
		t.Fatalf("version = %d, want %d", cv.Version, before.Version+1)
	}
	for _, key := range []string{"a", "b"} {
		p, o := cv.Variants.Get(key), before.Variants.Get(key)
		if p.Content() != o.Content() || p.Label != o.Label || p.Source != o.Source || p.Locale != "tr" {
			t.Errorf("variant %s primary changed: %+v vs %+v", key, p, o)
		}
		en := p.Localized["en"]
		if en == nil || en.Stale || !en.Validation.Valid || en.Source != "llm" || en.Content() == p.Content() || en.Label == "" {
			t.Errorf("variant %s en = %+v", key, en)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM campaign_content WHERE campaign_id = $1 AND version = $2`, camp.ID, cv.Version).Scan(&n); err != nil || n != 4 {
		t.Fatalf("rows of the new version = %d, %v; want 4 (a,b x tr,en)", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM campaign_content WHERE campaign_id = $1 AND version = $2`, camp.ID, before.Version).Scan(&n); err != nil || n != 2 {
		t.Fatalf("history rows = %d, %v; want the 2 of the earlier version", n, err)
	}
	got, err := svc.Get(ctx, camp.ID)
	if err != nil || got.Content.Version != cv.Version || got.Content.Variants.A.Localized["en"] == nil {
		t.Fatalf("loaded = %+v, %v", got.Content, err)
	}
	// Re-adapting replaces the row in yet another version.
	again := localize(t, svc, camp.ID, "en")
	if again.Version != cv.Version+1 || again.Variants.A.Localized["en"].Stale {
		t.Fatalf("second localize = %+v", again)
	}
}

// B-3: what a localize request is refused for, before the model is called.
func TestPlanLocalize_Refusals_B3(t *testing.T) {
	svc, _, camp := b3Campaign(t)
	ctx := context.Background()
	for name, tc := range map[string]struct{ id, target, code string }{
		"campaign's own language": {camp.ID, "tr", "validation_failed"},
		"unknown locale":          {camp.ID, "de", "validation_failed"},
		"empty":                   {camp.ID, "", "validation_failed"},
		"unknown campaign":        {"00000000-0000-0000-0000-000000000000", "en", "not_found"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.PlanLocalize(ctx, tc.id, tc.target); codeOf(err) != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
	// A one-variant version has nothing to pair b with.
	a := VariantEdit{Label: camp.Content.Variants.A.Label, SMSText: camp.Content.Variants.A.SMSText,
		EmailSubject: camp.Content.Variants.A.EmailSubject, EmailBody: camp.Content.Variants.A.EmailBody}
	if _, err := svc.SaveVariants(ctx, camp.ID, a, nil, camp.Content.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlanLocalize(ctx, camp.ID, "en"); codeOf(err) != "variant_missing" {
		t.Fatalf("one-variant version: %v", err)
	}
	fresh, _ := svc.Create(ctx, "bir brief")
	if _, err := svc.PlanLocalize(ctx, fresh.ID, "en"); codeOf(err) == "" {
		t.Fatal("a campaign without segment or content must be refused")
	}
}

// B-3: an edit made while the model wrote wins: saving the adaptation of a stale base version is a 409 and
// nothing is stored.
func TestRunLocalize_StaleBaseWins_B3(t *testing.T) {
	svc, pool, camp := b3Campaign(t)
	ctx := context.Background()
	plan, err := svc.PlanLocalize(ctx, camp.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	e := func(v *ContentVariant) VariantEdit {
		return VariantEdit{Label: v.Label, SMSText: v.SMSText, EmailSubject: v.EmailSubject, EmailBody: v.EmailBody}
	}
	b := e(camp.Content.Variants.B)
	if _, err := svc.SaveVariants(ctx, camp.ID, e(&camp.Content.Variants.A), &b, camp.Content.Version); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.RunLocalize(ctx, plan, nil, nil); codeOf(err) != "stale_content_version" {
		t.Fatalf("err = %v, want stale_content_version", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM campaign_content WHERE campaign_id = $1 AND adapted_from IS NOT NULL`, camp.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("adapted rows = %d, %v; want none", n, err)
	}
}

// B-3 staleness (D-22): editing the primary copy makes its adaptation stale, an untouched adaptation saved with
// it stays stale, hand-editing the adaptation vouches for the new primary, and refine marks the refined
// variant's adaptation stale while the other variant's stays current.
func TestLocalized_StalenessFollowsPrimary_B3(t *testing.T) {
	svc, _, camp := b3Campaign(t)
	ctx := context.Background()
	cv := localize(t, svc, camp.ID, "en")
	edit := func(v *ContentVariant) VariantEdit {
		return VariantEdit{Label: v.Label, SMSText: v.SMSText, EmailSubject: v.EmailSubject, EmailBody: v.EmailBody}
	}

	// A client that only knows primary copies edits variant a: the adaptations are carried over, a's is stale.
	a := edit(&cv.Variants.A)
	a.SMSText = "Merhaba {{first_name}}, yeni ve kısa bir metin!"
	b := edit(cv.Variants.B)
	v3, err := svc.SaveVariants(ctx, camp.ID, a, &b, cv.Version)
	if err != nil {
		t.Fatal(err)
	}
	enA, enB := v3.Variants.A.Localized["en"], v3.Variants.B.Localized["en"]
	if enA == nil || !enA.Stale || enB == nil || enB.Stale {
		t.Fatalf("after editing a: a.en = %+v, b.en = %+v", enA, enB)
	}
	if enA.SMSText != cv.Variants.A.Localized["en"].SMSText || enA.Source != "llm" {
		t.Fatalf("the adaptation must be carried over unchanged: %+v", enA)
	}

	// Sending the stale adaptation back untouched keeps it stale.
	a2, b2 := edit(&v3.Variants.A), edit(v3.Variants.B)
	a2.Localized = map[string]*VariantEdit{"en": {Label: enA.Label, SMSText: enA.SMSText, EmailSubject: enA.EmailSubject, EmailBody: enA.EmailBody}}
	v4, err := svc.SaveVariants(ctx, camp.ID, a2, &b2, v3.Version)
	if err != nil || !v4.Variants.A.Localized["en"].Stale {
		t.Fatalf("untouched adaptation must stay stale: %+v, %v", v4.Variants.A.Localized["en"], err)
	}

	// Hand-editing the adaptation vouches for it against the current primary.
	a3, b3 := edit(&v4.Variants.A), edit(v4.Variants.B)
	a3.Localized = map[string]*VariantEdit{"en": {Label: enA.Label, SMSText: "Hi {{first_name}}, a new short text!", EmailSubject: enA.EmailSubject, EmailBody: enA.EmailBody}}
	v5, err := svc.SaveVariants(ctx, camp.ID, a3, &b3, v4.Version)
	if err != nil {
		t.Fatal(err)
	}
	if en := v5.Variants.A.Localized["en"]; en.Stale || en.Source != "edited" || en.SMSText != "Hi {{first_name}}, a new short text!" {
		t.Fatalf("hand edit: %+v", en)
	}

	// Refine rewrites variant b: b's adaptation turns stale, a's stays current.
	plan, err := svc.PlanRefine(ctx, camp.ID, "b", "daha kısa", v5.Version)
	if err != nil {
		t.Fatal(err)
	}
	v6, _, err := svc.RunRefine(ctx, plan, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v6.Variants.A.Localized["en"].Stale || !v6.Variants.B.Localized["en"].Stale {
		t.Fatalf("after refining b: a.en stale = %v, b.en stale = %v", v6.Variants.A.Localized["en"].Stale, v6.Variants.B.Localized["en"].Stale)
	}
}

// B-3: PUT content validates every adaptation with its own locale's constraints and refuses a locale that is
// not a localized one.
func TestSaveVariants_PerLocaleValidation_B3(t *testing.T) {
	svc, _, camp := b3Campaign(t)
	ctx := context.Background()
	cv := localize(t, svc, camp.ID, "en")
	edit := func(v *ContentVariant) VariantEdit {
		return VariantEdit{Label: v.Label, SMSText: v.SMSText, EmailSubject: v.EmailSubject, EmailBody: v.EmailBody}
	}
	en := cv.Variants.A.Localized["en"]
	a, b := edit(&cv.Variants.A), edit(cv.Variants.B)
	a.Localized = map[string]*VariantEdit{"en": {Label: en.Label, SMSText: strings.Repeat("x", 300), EmailSubject: en.EmailSubject, EmailBody: en.EmailBody}}
	saved, err := svc.SaveVariants(ctx, camp.ID, a, &b, cv.Version)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Variants.A.Localized["en"]; got.Validation.ValidFor["sms"] || !got.Validation.ValidFor["email"] || !saved.Variants.A.Validation.Valid {
		t.Fatalf("the en copy is invalid for sms only, the tr primary stays valid: %+v", got.Validation)
	}
	for name, loc := range map[string]string{"unknown": "de", "campaign language": "tr"} {
		t.Run(name, func(t *testing.T) {
			x := edit(&saved.Variants.A)
			x.Localized = map[string]*VariantEdit{loc: {Label: "x", SMSText: "y", EmailSubject: "z", EmailBody: "w"}}
			y := edit(saved.Variants.B)
			if _, err := svc.SaveVariants(ctx, camp.ID, x, &y, saved.Version); codeOf(err) != "validation_failed" {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("long adapted label", func(t *testing.T) {
		x := edit(&saved.Variants.A)
		x.Localized = map[string]*VariantEdit{"en": {Label: strings.Repeat("ş", 41), SMSText: "y", EmailSubject: "z", EmailBody: "w"}}
		y := edit(saved.Variants.B)
		if _, err := svc.SaveVariants(ctx, camp.ID, x, &y, saved.Version); codeOf(err) != "validation_failed" {
			t.Fatalf("err = %v", err)
		}
	})
}

func approveMode(svc *Service, id, key, mode string, ver int) (ApproveResult, error) {
	return svc.ApproveWith(context.Background(), id, key, []string{"sms", "email"}, ver, ApproveOptions{Variant: "ab", LanguageMode: mode})
}

func issuePaths(err error) []string {
	var ce *Error
	var out []string
	if errors.As(err, &ce) {
		for _, d := range ce.Details {
			out = append(out, d.Path+"|"+d.Code)
		}
	}
	return out
}

// B-3 approve gating: 'recipient' needs, per sent variant, a copy for every locale among the recipients that
// exists, is current and is valid for the channel; otherwise 422 missing_localization lists what is wrong and
// nothing is enqueued. 'campaign' mode is not affected.
func TestApprove_RecipientModeGate_B3(t *testing.T) {
	svc, pool, camp := b3Campaign(t)
	ctx := context.Background()
	ver := camp.Content.Version

	_, err := approveMode(svc, camp.ID, "k1", "recipient", ver)
	want := []string{"variants.a.localized.en|missing", "variants.b.localized.en|missing"}
	if codeOf(err) != "missing_localization" || !equalStrings(issuePaths(err), want) {
		t.Fatalf("no adaptation: %v %v", err, issuePaths(err))
	}
	assertNothingEnqueued(t, pool, camp.ID)

	if _, err := approveMode(svc, camp.ID, "k1", "everyone", ver); codeOf(err) != "bad_request" {
		t.Fatalf("unknown mode: %v", err)
	}

	cv := localize(t, svc, camp.ID, "en")
	// Stale: edit variant a's primary.
	e := func(v *ContentVariant) VariantEdit {
		return VariantEdit{Label: v.Label, SMSText: v.SMSText, EmailSubject: v.EmailSubject, EmailBody: v.EmailBody}
	}
	a, b := e(&cv.Variants.A), e(cv.Variants.B)
	a.SMSText = "Merhaba {{first_name}}, değişti!"
	stale, err := svc.SaveVariants(ctx, camp.ID, a, &b, cv.Version)
	if err != nil {
		t.Fatal(err)
	}
	_, err = approveMode(svc, camp.ID, "k1", "recipient", stale.Version)
	if codeOf(err) != "missing_localization" || !equalStrings(issuePaths(err), []string{"variants.a.localized.en|stale"}) {
		t.Fatalf("stale: %v %v", err, issuePaths(err))
	}
	// Sending only variant b ignores variant a's stale copy.
	// (variant "b" mode checks just b)
	// Invalid: a hand-edited adaptation that is too long for SMS.
	a2, b2 := e(&stale.Variants.A), e(stale.Variants.B)
	en := stale.Variants.A.Localized["en"]
	a2.Localized = map[string]*VariantEdit{"en": {Label: en.Label, SMSText: strings.Repeat("x", 300), EmailSubject: en.EmailSubject, EmailBody: en.EmailBody}}
	invalid, err := svc.SaveVariants(ctx, camp.ID, a2, &b2, stale.Version)
	if err != nil {
		t.Fatal(err)
	}
	_, err = approveMode(svc, camp.ID, "k1", "recipient", invalid.Version)
	if codeOf(err) != "missing_localization" || len(issuePaths(err)) == 0 || !strings.HasPrefix(issuePaths(err)[0], "variants.a.localized.en.sms_text|") {
		t.Fatalf("invalid: %v %v", err, issuePaths(err))
	}
	assertNothingEnqueued(t, pool, camp.ID)

	// Campaign mode is untouched by a bad adaptation (the primary copy is valid).
	res, err := approveMode(svc, camp.ID, "k1", "campaign", invalid.Version)
	if err != nil || res.Campaign.LanguageMode != "campaign" {
		t.Fatalf("campaign mode: %+v %v", res.Campaign.LanguageMode, err)
	}
}

// B-3: with a valid, current adaptation 'recipient' mode is approved; the locale of every message is frozen
// from the user at enqueue (also in 'campaign' mode), the per-locale enqueued counts match the audience, and
// a repeated key replays the stored answer (idempotency is unchanged).
func TestApprove_RecipientModeEnqueuesLocale_B3(t *testing.T) {
	svc, pool, camp := b3Campaign(t)
	ctx := context.Background()
	cv := localize(t, svc, camp.ID, "en")
	res, err := approveMode(svc, camp.ID, "k-recipient", "recipient", cv.Version)
	if err != nil {
		t.Fatal(err)
	}
	if res.Campaign.LanguageMode != "recipient" || res.Campaign.Status != StatusSending || res.Enqueued["email"] != 30 || res.Enqueued["sms"] != 27 {
		t.Fatalf("result = %+v enqueued %v", res.Campaign.LanguageMode, res.Enqueued)
	}
	var mismatch, en, tr int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE m.locale <> u.locale), count(*) FILTER (WHERE m.locale = 'en'),
			count(*) FILTER (WHERE m.locale = 'tr') FROM messages m JOIN users u ON u.id = m.user_id WHERE m.campaign_id = $1 AND m.channel = 'email'`,
		camp.ID).Scan(&mismatch, &en, &tr); err != nil || mismatch != 0 || en != 10 || tr != 20 {
		t.Fatalf("email messages: %d locale mismatches, %d en, %d tr, %v; want 0, 10, 20", mismatch, en, tr, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE locale = 'en'), count(*) FILTER (WHERE locale = 'tr') FROM messages
		WHERE campaign_id = $1 AND channel = 'sms'`, camp.ID).Scan(&en, &tr); err != nil || en != 9 || tr != 18 {
		t.Fatalf("sms messages: %d en, %d tr, %v; want 9, 18 (consent applies per channel)", en, tr, err)
	}
	// The same key replays the answer even if the body differs; another key is already_approved.
	replay, err := approveMode(svc, camp.ID, "k-recipient", "campaign", cv.Version)
	if err != nil || replay.Campaign.LanguageMode != "recipient" {
		t.Fatalf("replay = %+v, %v", replay.Campaign.LanguageMode, err)
	}
	if _, err := approveMode(svc, camp.ID, "other-key", "recipient", cv.Version); codeOf(err) != "already_approved" {
		t.Fatalf("second key: %v", err)
	}
	// Progress reports sent/failed per locale per channel; the locales add up to the channel counters.
	p, err := svc.Progress(ctx, camp.ID)
	if err != nil {
		t.Fatal(err)
	}
	for ch, cp := range p.Channels {
		if cp.Locales["en"] == nil || cp.Locales["tr"] == nil || cp.Locales["en"].Total+cp.Locales["tr"].Total != cp.Total {
			t.Errorf("%s locales %+v do not add up to total %d", ch, cp.Locales, cp.Total)
		}
	}
	if p.Channels["email"].Locales["en"].Total != 10 || p.Channels["sms"].Locales["tr"].Total != 18 {
		t.Fatalf("per-locale totals: %+v / %+v", p.Channels["email"].Locales, p.Channels["sms"].Locales)
	}
}

// B-3: when no recipient reads the other locale, 'recipient' mode needs no adaptation: nothing is missing.
func TestApprove_RecipientModeWithOnlyCampaignLanguage_B3(t *testing.T) {
	svc, pool, camp := b3Campaign(t)
	if _, err := pool.Exec(context.Background(), `UPDATE users SET locale = 'tr'`); err != nil {
		t.Fatal(err)
	}
	if _, err := approveMode(svc, camp.ID, "k", "recipient", camp.Content.Version); err != nil {
		t.Fatalf("approve = %v", err)
	}
}

func assertNothingEnqueued(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM messages WHERE campaign_id = $1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("messages = %d, %v; a refused approve must enqueue nothing", n, err)
	}
}

func equalStrings(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

package campaign

import (
	"context"
	"strings"
	"testing"
)

type rowView struct {
	version     int
	variant     string
	label       string
	source      string
	instruction *string
}

func contentRows(t *testing.T, svc *Service, id string) []rowView {
	t.Helper()
	rows, err := svc.Pool.Query(context.Background(),
		`SELECT version, variant, label, source, instruction FROM campaign_content WHERE campaign_id = $1 ORDER BY version, variant`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []rowView
	for rows.Next() {
		var r rowView
		if err := rows.Scan(&r.version, &r.variant, &r.label, &r.source, &r.instruction); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// B-2: generation stores one row per variant in one version (source llm, label set), load returns both
// variants and the top level mirrors a.
func TestGenerateContent_TwoRowsAndMirror_B2(t *testing.T) {
	svc, _ := newService(t, 300)
	ctx := context.Background()
	camp := readyCampaign(t, svc, validContent) // version 1: one variant (v1 body)
	in, err := svc.ContentInput(ctx, camp.ID)
	if err != nil {
		t.Fatal(err)
	}
	cv, meta, err := svc.GenerateContent(ctx, camp.ID, in, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cv.Version != 2 || cv.Variants.B == nil || meta.PromptVersion != "content_v2" || !meta.CostRecorded {
		t.Fatalf("cv = %+v meta = %+v", cv, meta)
	}
	rows := contentRows(t, svc, camp.ID)
	if len(rows) != 3 || rows[0].variant != "a" || rows[1].version != 2 || rows[1].variant != "a" || rows[2].variant != "b" {
		t.Fatalf("rows = %+v", rows)
	}
	for _, r := range rows[1:] {
		if r.source != "llm" || r.label == "" || r.instruction != nil {
			t.Fatalf("row = %+v", r)
		}
	}
	got, err := svc.Get(ctx, camp.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := got.Content
	if c.Version != 2 || c.Variants.A.Label != cv.Variants.A.Label || c.Variants.B == nil || c.Variants.B.SMSText != cv.Variants.B.SMSText {
		t.Fatalf("loaded = %+v", c)
	}
	if c.SMSText != c.Variants.A.SMSText || c.Source != "llm" || c.EmailSubject != c.Variants.A.EmailSubject ||
		!c.Validation.Valid || !c.Variants.B.Validation.Valid || c.Variants.A.SMSText == c.Variants.B.SMSText {
		t.Fatalf("top level must mirror variant a: %+v", c)
	}
}

// B-2: SaveVariants keeps the source and instruction of a row whose copy and label did not change, marks a
// changed row as edited, and can save a one-variant version.
func TestSaveVariants_SourceTracking_B2(t *testing.T) {
	svc, _ := newService(t, 300)
	ctx := context.Background()
	camp := readyCampaign(t, svc, validContent)
	in, _ := svc.ContentInput(ctx, camp.ID)
	gen, _, err := svc.GenerateContent(ctx, camp.ID, in, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	edit := func(v *ContentVariant) VariantEdit {
		return VariantEdit{Label: v.Label, SMSText: v.SMSText, EmailSubject: v.EmailSubject, EmailBody: v.EmailBody}
	}

	b := edit(gen.Variants.B)
	b.SMSText = "Merhaba {{first_name}}, yeni metin!"
	cv, err := svc.SaveVariants(ctx, camp.ID, edit(&gen.Variants.A), &b, gen.Version)
	if err != nil {
		t.Fatal(err)
	}
	if cv.Version != 3 || cv.Variants.A.Source != "llm" || cv.Variants.B.Source != "edited" || cv.Variants.B.SMSText != b.SMSText {
		t.Fatalf("cv = %+v", cv)
	}

	// A label change alone also counts as an edit.
	a := edit(&cv.Variants.A)
	a.Label = "Yeni etiket"
	bk := edit(cv.Variants.B)
	if cv, err = svc.SaveVariants(ctx, camp.ID, a, &bk, cv.Version); err != nil || cv.Variants.A.Source != "edited" || cv.Variants.B.Source != "edited" {
		t.Fatalf("label edit: %+v %v", cv, err)
	}

	// An invalid text is saved (the report says why); b = nil saves one variant.
	a = edit(&cv.Variants.A)
	a.SMSText = strings.Repeat("x", 300)
	cv, err = svc.SaveVariants(ctx, camp.ID, a, nil, cv.Version)
	if err != nil || cv.Variants.B != nil || cv.Variants.A.Validation.Valid || cv.Validation.Valid {
		t.Fatalf("invalid one-variant save: %+v %v", cv, err)
	}
	got, _ := svc.Get(ctx, camp.ID)
	if got.Content.Variants.B != nil || got.Content.Version != cv.Version {
		t.Fatalf("loaded latest = %+v", got.Content)
	}

	for name, tc := range map[string]struct {
		a, b *VariantEdit
		base int
		code string
	}{
		"stale base":     {&a, nil, 1, "stale_content_version"},
		"long label":     {&VariantEdit{Label: strings.Repeat("ş", 41)}, nil, cv.Version, "validation_failed"},
		"newline label":  {&VariantEdit{Label: "a\nb"}, nil, cv.Version, "validation_failed"},
		"negative base":  {&a, nil, -1, "bad_request"},
		"b label length": {&a, &VariantEdit{Label: strings.Repeat("ş", 41)}, cv.Version, "validation_failed"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.SaveVariants(ctx, camp.ID, *tc.a, tc.b, tc.base); codeOf(err) != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

package campaign

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"smartcampaign/internal/content"
)

// mirrorA copies variant a into the top-level fields (the v1 shape of a content version).
func (cv *ContentVersion) mirrorA() {
	a := cv.Variants.A
	cv.Source, cv.SMSText, cv.EmailSubject, cv.EmailBody, cv.Validation = a.Source, a.SMSText, a.EmailSubject, a.EmailBody, a.Validation
}

// loadLatestContent returns the latest content version with all its variant rows, or nil when the campaign
// has no content yet.
func loadLatestContent(ctx context.Context, db DB, id string) (*ContentVersion, error) {
	rows, err := db.Query(ctx, `SELECT version, variant, label, source, instruction, sms_text, email_subject, email_body,
			validation, created_at
		FROM campaign_content
		WHERE campaign_id = $1 AND version = (SELECT max(version) FROM campaign_content WHERE campaign_id = $1)
		ORDER BY variant`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cv *ContentVersion
	for rows.Next() {
		var (
			version   int
			key       string
			v         ContentVariant
			valRaw    []byte
			createdAt time.Time
		)
		if err := rows.Scan(&version, &key, &v.Label, &v.Source, &v.Instruction, &v.SMSText, &v.EmailSubject, &v.EmailBody, &valRaw, &createdAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(valRaw, &v.Validation); err != nil {
			return nil, fmt.Errorf("stored validation: %w", err)
		}
		if cv == nil {
			cv = &ContentVersion{Version: version, CreatedAt: createdAt}
		}
		switch key {
		case "a":
			cv.Variants.A = v
		case "b":
			vb := v
			cv.Variants.B = &vb
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if cv != nil {
		cv.mirrorA()
	}
	return cv, nil
}

// VariantEdit is one variant of a v2 PUT …/content body.
type VariantEdit struct {
	Label        string
	SMSText      string
	EmailSubject string
	EmailBody    string
}

func (e VariantEdit) content() content.Content {
	return content.Content{SMSText: e.SMSText, EmailSubject: e.EmailSubject, EmailBody: e.EmailBody}
}

// SaveVariants stores a user-edited version of one or two variants (the v2 body of PUT …/content), even when
// a text is invalid (the reports say why). A row whose copy or label changed gets source "edited"; an
// untouched row keeps its source and instruction. baseVersion must be the latest version (0 when none).
// A label that is too long or has control characters is refused (422): unlike a text, it is not a state the
// marketer needs to keep working on.
func (s *Service) SaveVariants(ctx context.Context, id string, a VariantEdit, b *VariantEdit, baseVersion int) (ContentVersion, error) {
	if baseVersion < 0 {
		return ContentVersion{}, errorf("bad_request", "base_version must be 0 or greater.")
	}
	var labelIssues []Issue
	for _, e := range []struct {
		key  string
		edit *VariantEdit
	}{{"a", &a}, {"b", b}} {
		if e.edit == nil {
			continue
		}
		for _, is := range content.ValidateLabel("variants."+e.key+".label", e.edit.Label, true) {
			labelIssues = append(labelIssues, Issue(is))
		}
	}
	if len(labelIssues) > 0 {
		return ContentVersion{}, errorf("validation_failed", "A variant label is invalid.", labelIssues...)
	}
	camp, v, err := s.load(ctx, s.Pool, id, true)
	if err != nil {
		return ContentVersion{}, err
	}
	if !slices.Contains(contentEditable, camp.Status) {
		return ContentVersion{}, invalidState(camp.Status, "edit content")
	}
	wc, err := s.worstCase(ctx, s.Pool, v)
	if err != nil {
		return ContentVersion{}, err
	}
	cons := constraints(camp.Language, wc)
	// Provenance is carried over from the base version; a stale base is rejected by saveVersion anyway.
	var prev *ContentVersion
	if camp.Content != nil && camp.Content.Version == baseVersion {
		prev = camp.Content
	}
	input := func(key string, e VariantEdit) VariantInput {
		in := VariantInput{Label: e.Label, Source: "edited", Content: e.content(), Report: content.Validate(e.content(), cons)}
		if prev != nil {
			if p := prev.Variants.Get(key); p != nil && p.Label == e.Label && p.Content() == e.content() {
				in.Source, in.Instruction = p.Source, p.Instruction
			}
		}
		return in
	}
	ain := input("a", a)
	var bin *VariantInput
	if b != nil {
		x := input("b", *b)
		bin = &x
	}
	return s.saveVersion(ctx, id, ain, bin, baseVersion)
}

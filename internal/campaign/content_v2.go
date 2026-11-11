package campaign

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/content"
)

// carryLocalized turns the stored adaptations of a variant into inputs for the next version, keeping their
// provenance and the hash they were adapted from (a changed primary then shows them as stale).
func carryLocalized(v *ContentVariant) map[string]LocalizedInput {
	out := map[string]LocalizedInput{}
	for loc, l := range v.Localized {
		out[loc] = LocalizedInput{Label: l.Label, Source: l.Source, Content: l.Content(), Report: l.Validation, AdaptedFrom: l.adaptedFrom}
	}
	return out
}

// mirrorA copies variant a into the top-level fields (the v1 shape of a content version).
func (cv *ContentVersion) mirrorA() {
	a := cv.Variants.A
	cv.Source, cv.SMSText, cv.EmailSubject, cv.EmailBody, cv.Validation = a.Source, a.SMSText, a.EmailSubject, a.EmailBody, a.Validation
}

// loadLatestContent returns the latest content version with all its rows, or nil when the campaign has no
// content yet. language is the campaign's language: the rows of any other locale are localized copies of the
// primary row of their variant, and are stale when the primary changed since they were adapted (D-22).
func loadLatestContent(ctx context.Context, db DB, id, language string) (*ContentVersion, error) {
	rows, err := db.Query(ctx, `SELECT version, variant, locale, adapted_from, label, source, instruction, sms_text,
			email_subject, email_body, validation, created_at
		FROM campaign_content
		WHERE campaign_id = $1 AND version = (SELECT max(version) FROM campaign_content WHERE campaign_id = $1)
		ORDER BY variant, locale`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cv *ContentVersion
	type adapted struct {
		key, locale string
		copy        LocalizedCopy
	}
	var localized []adapted
	for rows.Next() {
		var (
			version     int
			key, locale string
			adaptedFrom *string
			v           ContentVariant
			valRaw      []byte
			createdAt   time.Time
		)
		if err := rows.Scan(&version, &key, &locale, &adaptedFrom, &v.Label, &v.Source, &v.Instruction, &v.SMSText, &v.EmailSubject, &v.EmailBody, &valRaw, &createdAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(valRaw, &v.Validation); err != nil {
			return nil, fmt.Errorf("stored validation: %w", err)
		}
		if cv == nil {
			cv = &ContentVersion{Version: version, CreatedAt: createdAt}
		}
		if adaptedFrom != nil {
			if locale != language { // an adaptation into the campaign's own language (the language changed) is not shown
				localized = append(localized, adapted{key, locale, LocalizedCopy{Label: v.Label, Source: v.Source,
					SMSText: v.SMSText, EmailSubject: v.EmailSubject, EmailBody: v.EmailBody, Validation: v.Validation, adaptedFrom: *adaptedFrom}})
			}
			continue
		}
		v.Locale, v.Localized = locale, map[string]*LocalizedCopy{}
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
	if cv == nil {
		return nil, nil
	}
	for _, l := range localized {
		prim := cv.Variants.Get(l.key)
		if prim == nil {
			continue
		}
		c := l.copy
		c.Stale = c.adaptedFrom != content.Hash(prim.Content())
		prim.Localized[l.locale] = &c
	}
	cv.mirrorA()
	return cv, nil
}

// VariantEdit is one variant of a v2 PUT …/content body.
type VariantEdit struct {
	Label        string
	SMSText      string
	EmailSubject string
	EmailBody    string
	// Localized replaces the adaptation of the named locales (api.md §9.9). Locales left out keep the copy of
	// the base version, so a client that only knows primary copies cannot lose adaptations by saving.
	Localized map[string]*VariantEdit
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
	var localeIssues []Issue
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
		for _, loc := range slices.Sorted(maps.Keys(e.edit.Localized)) {
			path := "variants." + e.key + ".localized." + loc
			if !slices.Contains(catalog.Locales, loc) {
				localeIssues = append(localeIssues, Issue{Path: path, Code: "invalid", Message: "Use tr or en."})
				continue
			}
			for _, is := range content.ValidateLabel(path+".label", e.edit.Localized[loc].Label, true) {
				labelIssues = append(labelIssues, Issue(is))
			}
		}
	}
	if len(localeIssues) > 0 {
		return ContentVersion{}, errorf("validation_failed", "A localized copy names an unknown locale.", localeIssues...)
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
	input := func(key string, e VariantEdit) (VariantInput, error) {
		in := VariantInput{Label: e.Label, Source: "edited", Content: e.content(), Report: content.Validate(e.content(), cons),
			Localized: map[string]LocalizedInput{}}
		var p *ContentVariant
		if prev != nil {
			if p = prev.Variants.Get(key); p != nil {
				in.Localized = carryLocalized(p)
				if p.Label == e.Label && p.Content() == e.content() {
					in.Source, in.Instruction = p.Source, p.Instruction
				}
			}
		}
		for _, loc := range slices.Sorted(maps.Keys(e.Localized)) {
			le := e.Localized[loc]
			if loc == camp.Language {
				return in, errorf("validation_failed", "The campaign's own language is the primary copy, not a localized one.",
					Issue{Path: "variants." + key + ".localized." + loc, Code: "invalid", Message: "Edit the primary copy instead."})
			}
			li := LocalizedInput{Label: le.Label, Source: "edited", Content: le.content(),
				Report: content.Validate(le.content(), constraints(loc, wc)), AdaptedFrom: content.Hash(e.content())}
			// An untouched row keeps its provenance and the hash it was adapted from: a primary edited since
			// then keeps it stale. Only a hand edit of the adaptation itself vouches for the new primary.
			if old, ok := in.Localized[loc]; ok && old.Label == le.Label && old.Content == le.content() {
				li.Source, li.AdaptedFrom = old.Source, old.AdaptedFrom
			}
			in.Localized[loc] = li
		}
		return in, nil
	}
	ain, err := input("a", a)
	if err != nil {
		return ContentVersion{}, err
	}
	var bin *VariantInput
	if b != nil {
		x, err := input("b", *b)
		if err != nil {
			return ContentVersion{}, err
		}
		bin = &x
	}
	return s.saveVersion(ctx, id, ain, bin, baseVersion)
}

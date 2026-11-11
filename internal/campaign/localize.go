package campaign

import (
	"context"
	"fmt"
	"slices"
	"time"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
)

// LocalizePlan is a checked localize request: everything RunLocalize needs, resolved before the event stream
// opens so 404/409/422 stay plain JSON errors (like RefinePlan).
type LocalizePlan struct {
	ID           string
	TargetLocale string
	Input        llm.LocalizeInput
	base         ContentVersion
	constraints  content.Constraints
}

// PlanLocalize checks that the latest content version can be adapted to targetLocale now: the campaign
// status, the target (a supported locale other than the campaign's own language) and the version (it must
// have both variants, the unit the model adapts). It reads; it does not call the model.
func (s *Service) PlanLocalize(ctx context.Context, id, targetLocale string) (LocalizePlan, error) {
	camp, v, err := s.load(ctx, s.Pool, id, true)
	if err != nil {
		return LocalizePlan{}, err
	}
	if !slices.Contains(catalog.Locales, targetLocale) || targetLocale == camp.Language {
		return LocalizePlan{}, errorf("validation_failed", "The target locale must be the language the campaign is not written in.",
			Issue{Path: "target_locale", Code: "invalid", Message: "the campaign language is " + camp.Language + "; use the other locale"})
	}
	if !slices.Contains(contentEditable, camp.Status) {
		return LocalizePlan{}, invalidState(camp.Status, "adapt content")
	}
	if camp.Content == nil {
		return LocalizePlan{}, errorf("variant_missing", "There is no content to adapt yet; write the copy first.",
			Issue{Path: "variants.a", Code: "variant_missing", Message: "no content version"})
	}
	if camp.Content.Variants.B == nil {
		return LocalizePlan{}, errorf("variant_missing", fmt.Sprintf("Version %d has only variant a; adapt a version with variants a and b.", camp.Content.Version),
			Issue{Path: "variants.b", Code: "variant_missing", Message: "this version has no variant b"})
	}
	wc, err := s.worstCase(ctx, s.Pool, v)
	if err != nil {
		return LocalizePlan{}, err
	}
	in := llm.LocalizeInput{ContentInput: buildContentInput(id, camp, wc), TargetLocale: targetLocale, Variants: map[string]llm.LocalizeVariant{}}
	for _, key := range content.VariantKeys {
		pv := camp.Content.Variants.Get(key)
		in.Variants[key] = llm.LocalizeVariant{Label: pv.Label, Content: pv.Content()}
	}
	return LocalizePlan{ID: id, TargetLocale: targetLocale, Input: in, base: *camp.Content, constraints: constraints(camp.Language, wc)}, nil
}

// RunLocalize executes a plan from PlanLocalize and saves a new content version: the primary rows copied from
// the base version (source and instruction kept, report recomputed for the current audience) plus the adapted
// rows for the target locale, each remembering the hash of the primary copy it was adapted from. Earlier
// versions stay as history. An invalid adaptation is never saved: it ends as llm_invalid_output.
func (s *Service) RunLocalize(ctx context.Context, plan LocalizePlan, onDelta func(llm.Delta), onRepair func(llm.RepairNotice)) (ContentVersion, llm.Meta, error) {
	res, err := s.LLM.LocalizeContent(ctx, plan.Input, onDelta, onRepair)
	if err != nil {
		return ContentVersion{}, llm.Meta{}, fromLLM(err)
	}
	// Like generation: saving must not depend on the request context.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	carry := func(key string) *VariantInput {
		o := plan.base.Variants.Get(key)
		if o == nil {
			return nil
		}
		in := VariantInput{Label: o.Label, Source: o.Source, Instruction: o.Instruction, Content: o.Content(),
			Report: content.Validate(o.Content(), plan.constraints), Localized: carryLocalized(o)}
		if r, ok := res.Variants[key]; ok {
			in.Localized[plan.TargetLocale] = LocalizedInput{Label: r.Label, Source: "llm", Content: r.Content, Report: r.Report,
				AdaptedFrom: content.Hash(o.Content())}
		}
		return &in
	}
	// base_version is checked again inside the transaction: an edit made while the model wrote wins (409).
	cv, err := s.saveVersion(saveCtx, plan.ID, *carry("a"), carry("b"), plan.base.Version)
	return cv, res.Meta, err
}

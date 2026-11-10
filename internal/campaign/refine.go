package campaign

import (
	"context"
	"fmt"
	"slices"
	"time"

	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
)

// RefinePlan is a checked refine request: everything RefineContent needs, resolved before the event stream
// opens so 404/409/422 stay plain JSON errors (like ContentInput for generation).
type RefinePlan struct {
	ID          string
	Variant     string
	Instruction string
	Input       llm.RefineInput
	base        ContentVersion
	language    string
	constraints content.Constraints
}

// PlanRefine checks that variant `variant` of the latest content version can be refined now: the instruction
// (1..500 code points, no control characters), the campaign status, base_version (409 stale_content_version)
// and the variant (422 variant_missing when it does not exist). It reads; it does not call the model.
func (s *Service) PlanRefine(ctx context.Context, id, variant, instruction string, baseVersion int) (RefinePlan, error) {
	camp, v, err := s.load(ctx, s.Pool, id, true)
	if err != nil {
		return RefinePlan{}, err
	}
	if variant != "a" && variant != "b" {
		return RefinePlan{}, errorf("bad_request", "variant must be a or b.", Issue{Path: "variant", Code: "invalid", Message: "Use a or b."})
	}
	if err := llm.CheckInstruction(instruction); err != nil {
		return RefinePlan{}, fromLLM(err)
	}
	if !slices.Contains(contentEditable, camp.Status) {
		return RefinePlan{}, invalidState(camp.Status, "refine content")
	}
	latest := 0
	if camp.Content != nil {
		latest = camp.Content.Version
	}
	if baseVersion != latest {
		return RefinePlan{}, errorf("stale_content_version", fmt.Sprintf("Version %d is not the latest (%d); reload and refine again.", baseVersion, latest),
			Issue{Path: "base_version", Code: "stale", Message: fmt.Sprintf("latest is %d", latest)})
	}
	var cur *ContentVariant
	if camp.Content != nil {
		cur = camp.Content.Variants.Get(variant)
	}
	if cur == nil {
		return RefinePlan{}, errorf("variant_missing", fmt.Sprintf("Variant %s does not exist in version %d.", variant, latest),
			Issue{Path: "variant", Code: "variant_missing", Message: "this version has no variant " + variant})
	}
	wc, err := s.worstCase(ctx, s.Pool, v)
	if err != nil {
		return RefinePlan{}, err
	}
	in := llm.RefineInput{ContentInput: buildContentInput(id, camp, wc), Variant: variant, Label: cur.Label,
		Current: cur.Content(), Instruction: instruction}
	return RefinePlan{ID: id, Variant: variant, Instruction: instruction, Input: in, base: *camp.Content,
		language: camp.Language, constraints: constraints(camp.Language, wc)}, nil
}

// RefineContent rewrites one variant with the model and saves a new version: the named variant is the
// rewrite (source "refined", the instruction stored) and the other variant is copied unchanged (source and
// instruction kept, report recomputed for the current audience). Earlier versions stay as history. An
// invalid rewrite is never saved: it ends as llm_invalid_output after the repair loop.
func (s *Service) RefineContent(ctx context.Context, id, variant, instruction string, baseVersion int,
	onDelta func(llm.Delta), onRepair func(llm.RepairNotice)) (ContentVersion, llm.Meta, error) {
	plan, err := s.PlanRefine(ctx, id, variant, instruction, baseVersion)
	if err != nil {
		return ContentVersion{}, llm.Meta{}, err
	}
	return s.RunRefine(ctx, plan, onDelta, onRepair)
}

// RunRefine executes a plan from PlanRefine.
func (s *Service) RunRefine(ctx context.Context, plan RefinePlan, onDelta func(llm.Delta), onRepair func(llm.RepairNotice)) (ContentVersion, llm.Meta, error) {
	res, err := s.LLM.RefineContent(ctx, plan.Input, onDelta, onRepair)
	if err != nil {
		return ContentVersion{}, llm.Meta{}, fromLLM(err)
	}
	// Like generation: saving must not depend on the request context.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	instr := plan.Instruction
	refined := VariantInput{Label: res.Label, Source: "refined", Instruction: &instr, Content: res.Content, Report: res.Report}
	keep := func(key string) *VariantInput {
		o := plan.base.Variants.Get(key)
		if o == nil {
			return nil
		}
		return &VariantInput{Label: o.Label, Source: o.Source, Instruction: o.Instruction, Content: o.Content(),
			Report: content.Validate(o.Content(), plan.constraints)}
	}
	var a, b *VariantInput
	if plan.Variant == "a" {
		a, b = &refined, keep("b")
	} else {
		a, b = keep("a"), &refined
	}
	// base_version is checked again inside the transaction: an edit made while the model wrote wins.
	cv, err := s.saveVersion(saveCtx, plan.ID, *a, b, plan.base.Version)
	return cv, res.Meta, err
}

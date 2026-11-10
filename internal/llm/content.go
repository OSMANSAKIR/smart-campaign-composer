package llm

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/content"
	"smartcampaign/internal/segment"
)

// MaxInstructionRunes bounds a refine instruction (docs/contracts/api.md §9.4).
const MaxInstructionRunes = 500

// contentChecker holds what every content validation (generation and refine) needs, so both paths apply the
// same rules: the limits of content.Validate, the RT-6 language check and the RT-8c NUL rule.
type contentChecker struct {
	requested string // language the copy must be in
	channels  []string
	cons      content.Constraints
}

func newContentChecker(in ContentInput) contentChecker {
	channels := normalizeChannels(in.Channels)
	requested := in.Language
	if !slices.Contains(catalog.Locales, requested) {
		requested = "tr" // same default as the prompt and content.Validate
	}
	return contentChecker{requested: requested, channels: channels,
		cons: content.Constraints{Locale: in.Language, WorstCase: in.WorstCase, WorstCaseByChannel: in.WorstCaseByChannel}}
}

// language is the RT-6 check: the copy must be in the language that was asked for; the model's own
// "language" field is otherwise never compared with the request, so wrong-language copy would reach the
// approver.
func (k contentChecker) language(got string) []segment.Issue {
	if got == k.requested {
		return nil
	}
	return []segment.Issue{{Path: "language", Code: "language_mismatch",
		Message: "language must be " + k.requested + " as requested; write the whole copy in that language and set language to " + k.requested}}
}

// variant validates one variant's copy and label. prefix is "" for a refine answer and "variants.a." /
// "variants.b." for a generation answer. The returned report keeps the unprefixed paths (it is stored per
// variant); the blocking issues carry the prefix.
func (k contentChecker) variant(prefix, label string, c content.Content) (content.Report, []segment.Issue) {
	report := content.Validate(c, k.cons)
	blocking := toSegmentIssues(content.ValidateLabel(prefix+"label", label, false))
	nul := content.NULFields(c)
	for _, e := range report.Errors {
		// RT-8c: NUL blocks even in a field of a channel that was not requested; it could never be stored.
		if slices.Contains(k.channels, channelOf(e.Path)) || slices.Contains(nul, e.Path) && e.Code == "control_character" {
			e.Path = prefix + e.Path
			blocking = append(blocking, segment.Issue(e))
		}
	}
	return report, blocking
}

// ProposeContent streams the generation of two content variants (prompt content_v2); onDelta receives JSON
// fragments (never a split rune), onRepair is called before each repair attempt so the client can clear its
// partial buffer. Either variant being invalid makes the whole answer invalid.
func (s *Service) ProposeContent(ctx context.Context, in ContentInput, onDelta func(Delta), onRepair func(RepairNotice)) (ContentResult, error) {
	if err := checkBrief(in.Brief); err != nil {
		return ContentResult{}, err
	}
	_, sys, err := systemPrompts()
	if err != nil {
		return ContentResult{}, fmt.Errorf("llm: prompts: %w", err)
	}
	chk := newContentChecker(in)
	var res ContentResult
	validate := func(raw []byte) []segment.Issue {
		p, issues := content.ParseProposalV2(raw)
		if len(issues) > 0 {
			return toSegmentIssues(issues)
		}
		blocking := chk.language(p.Language)
		next := ContentResult{Notes: p.Notes}
		for _, key := range content.VariantKeys {
			vp := p.Variant(key)
			c := vp.Content()
			report, bl := chk.variant("variants."+key+".", vp.Label, c)
			blocking = append(blocking, bl...)
			vr := VariantResult{Label: vp.Label, Content: c, Report: report}
			if key == "a" {
				next.A = vr
			} else {
				next.B = vr
			}
		}
		next.Content, next.Report = next.A.Content, next.A.Report
		res = next
		return blocking
	}
	meta, err := s.run(ctx, job{
		task: PurposeContent, campaignID: in.CampaignID, promptVersion: ContentPromptVersion,
		system: []SystemBlock{{Text: sys, Cache: true}}, user: contentUserMessage(in, chk.channels, worstLengths(in)),
		schema: catalog.ContentWireSchema(), maxTokens: DefaultContentMaxTokens, timeout: s.contentTimeout,
		stream: true, onDelta: onDelta, onRepair: onRepair, validate: validate,
	})
	if err != nil {
		return ContentResult{}, err
	}
	res.Meta = meta
	return res, nil
}

// CheckInstruction validates a refine instruction: 1..MaxInstructionRunes code points, valid UTF-8, no
// control characters other than line breaks and tabs (the same rule as the brief).
func CheckInstruction(instruction string) error {
	n := utf8.RuneCountInString(instruction)
	bad := func(code, msg string) error {
		return &Error{Code: "validation_failed", Message: "The instruction is invalid.",
			Details: []segment.Issue{{Path: "instruction", Code: code, Message: msg}}}
	}
	switch {
	case strings.TrimSpace(instruction) == "":
		return bad("required", "Write what should change in this variant.")
	case !utf8.ValidString(instruction):
		return bad("invalid_value", "The instruction contains invalid characters.")
	case n > MaxInstructionRunes:
		return bad("too_long", fmt.Sprintf("The instruction has %d characters; the limit is %d.", n, MaxInstructionRunes))
	}
	if strings.ContainsFunc(instruction, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return bad("control_character", "Remove invisible control characters.")
	}
	return nil
}

// RefineContent rewrites one variant according to the marketer's instruction (prompt refine_v1). The first
// call is recorded as `refine`, its repairs as `repair`. The answer goes through the same validation and
// repair loop as a generation, so an instruction can never relax a limit: it ends valid or as an error.
func (s *Service) RefineContent(ctx context.Context, in RefineInput, onDelta func(Delta), onRepair func(RepairNotice)) (RefineResult, error) {
	if err := checkBrief(in.Brief); err != nil {
		return RefineResult{}, err
	}
	if err := CheckInstruction(in.Instruction); err != nil {
		return RefineResult{}, err
	}
	sys, err := refineSystemPrompt()
	if err != nil {
		return RefineResult{}, fmt.Errorf("llm: prompts: %w", err)
	}
	chk := newContentChecker(in.ContentInput)
	var res RefineResult
	validate := func(raw []byte) []segment.Issue {
		p, issues := content.ParseRefine(raw)
		if len(issues) > 0 {
			return toSegmentIssues(issues)
		}
		blocking := chk.language(p.Language)
		c := p.Content()
		report, bl := chk.variant("", p.Label, c)
		res = RefineResult{Label: p.Label, Content: c, Report: report, Notes: p.Notes}
		return append(blocking, bl...)
	}
	meta, err := s.run(ctx, job{
		task: PurposeRefine, campaignID: in.CampaignID, promptVersion: RefinePromptVersion,
		system: []SystemBlock{{Text: sys, Cache: true}}, user: refineUserMessage(in, chk.channels, worstLengths(in.ContentInput)),
		schema: catalog.RefineWireSchema(), maxTokens: DefaultContentMaxTokens, timeout: s.contentTimeout,
		stream: true, onDelta: onDelta, onRepair: onRepair, validate: validate,
	})
	if err != nil {
		return RefineResult{}, err
	}
	res.Meta = meta
	return res, nil
}

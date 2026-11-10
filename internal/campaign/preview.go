package campaign

import (
	"context"
	"time"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/segment"
)

// Preview is the answer of POST /api/segment/preview (docs/contracts/api.md §9.2): what the rule editor shows
// while the marketer edits. It never saves anything.
type Preview struct {
	Valid    bool                `json:"valid"`
	Errors   []Issue             `json:"errors"`
	Warnings []Issue             `json:"warnings"`
	Rendered map[string][]string `json:"rendered"`
	Audience *AudienceCount      `json:"audience"`
}

// PreviewSegment validates a hand-edited rule with the validator used for model output and, when it is
// valid, renders and counts it. Invalid rules are a normal answer (valid: false), not an error, because the
// editor calls this on every pause in typing.
func (s *Service) PreviewSegment(ctx context.Context, rule segment.Rule) (Preview, error) {
	v, errs, warns := segment.Validate(rule)
	p := Preview{Valid: len(errs) == 0, Errors: orEmpty(errs), Warnings: orEmpty(warns), Rendered: map[string][]string{}}
	if !p.Valid {
		return p, nil
	}
	for _, loc := range catalog.Locales {
		p.Rendered[loc] = orEmpty(segment.Render(v, loc))
	}
	aud, err := segment.Count(ctx, s.Pool, v)
	if err != nil {
		return Preview{}, err
	}
	p.Audience = &AudienceCount{Audience: aud, CountedAt: time.Now().UTC()}
	return p, nil
}

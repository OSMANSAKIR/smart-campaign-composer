// Package eval runs the golden brief set (docs/eval/briefs.yaml) through the LLM service and measures it on
// data (KICKOFF §6.11): expected and actual rules are compiled and run on the seed DB, and their user-id
// sets are compared (exact match + Jaccard). Owned by the llm builder.
package eval

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v4"

	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/segment"
)

// Case is one brief of the golden set.
type Case struct {
	ID     string   `json:"id"`
	Lang   string   `json:"lang"`
	Brief  string   `json:"brief"`
	Pad    int      `json:"pad"`
	Tags   []string `json:"tags"`
	Expect Expect   `json:"expect"`
}

// Expect is what a good answer looks like; unset fields are not scored.
type Expect struct {
	Rule        *segment.Rule `json:"rule"`
	Channels    string        `json:"channels"`
	Unsupported *bool         `json:"unsupported"`
	ConsentNote bool          `json:"consent_note"`
	Error       string        `json:"error"`
}

// Load reads the YAML set. YAML is converted to JSON first so segment values decode exactly like model output.
func Load(path string) ([]Case, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	j, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var file struct {
		Briefs []Case `json:"briefs"`
	}
	if err := json.Unmarshal(j, &file); err != nil {
		return nil, err
	}
	for i := range file.Briefs {
		c := &file.Briefs[i]
		if c.Pad > 0 {
			c.Brief = pad(c.Brief, c.Pad)
		}
		if c.Expect.Rule != nil {
			if _, errs, _ := segment.Validate(*c.Expect.Rule); len(errs) > 0 {
				return nil, fmt.Errorf("eval case %s: expected rule is invalid: %v", c.ID, errs)
			}
		}
	}
	return file.Briefs, nil
}

// pad prepends neutral background text until the brief has n runes (very-long-brief case).
func pad(brief string, n int) string {
	const filler = "Arka plan: mağazamız yıllardır müşterilerine kaliteli ürünler sunuyor ve bu kampanya o geleneğin bir parçası. "
	var b strings.Builder
	for utf8.RuneCountInString(b.String())+utf8.RuneCountInString(brief)+utf8.RuneCountInString(filler) < n {
		b.WriteString(filler)
	}
	return b.String() + brief
}

// Result is the outcome of one case.
type Result struct {
	ID            string   `json:"id"`
	Error         string   `json:"error,omitempty"`
	ErrorOK       *bool    `json:"error_ok,omitempty"`
	Attempts      int      `json:"attempts"`
	FirstTryValid bool     `json:"first_try_valid"`
	Valid         bool     `json:"valid"`
	Exact         *bool    `json:"exact,omitempty"`
	Jaccard       *float64 `json:"jaccard,omitempty"`
	ExpectedUsers int      `json:"expected_users,omitempty"`
	ActualUsers   int      `json:"actual_users,omitempty"`
	ChannelsOK    *bool    `json:"channels_ok,omitempty"`
	UnsupportedOK *bool    `json:"unsupported_ok,omitempty"`
	ConsentOK     *bool    `json:"consent_ok,omitempty"`
	ContentValid  *bool    `json:"content_valid,omitempty"`
	ContentTries  int      `json:"content_attempts,omitempty"`
	LatencyMS     int64    `json:"latency_ms"`
	Tokens        int      `json:"tokens"`
	CostUSD       float64  `json:"cost_usd"`
	Rendered      []string `json:"rendered,omitempty"`
}

// Run evaluates every case. db must hold the seed users.
func Run(ctx context.Context, svc *llm.Service, db segment.Querier, cases []Case) ([]Result, error) {
	out := make([]Result, 0, len(cases))
	for _, c := range cases {
		r, err := runCase(ctx, svc, db, c)
		if err != nil {
			return out, fmt.Errorf("eval %s: %w", c.ID, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func runCase(ctx context.Context, svc *llm.Service, db segment.Querier, c Case) (Result, error) {
	r := Result{ID: c.ID}
	res, err := svc.ProposeSegment(ctx, "", c.Brief)
	var le *llm.Error
	if err != nil && !errors.As(err, &le) {
		return r, err
	}
	if le != nil {
		r.Error, r.Attempts = le.Code, le.Meta.Attempts
		r.LatencyMS, r.Tokens, r.CostUSD = le.Meta.LatencyMS, le.Meta.InputTokens+le.Meta.OutputTokens, le.Meta.CostUSD
		if c.Expect.Error != "" {
			ok := le.Code == c.Expect.Error
			r.ErrorOK = &ok
		}
		return r, nil
	}
	if c.Expect.Error != "" {
		ok := false
		r.ErrorOK = &ok
	}
	m := res.Meta
	r.Attempts, r.Valid, r.FirstTryValid = m.Attempts, true, m.Attempts == 1
	r.LatencyMS, r.Tokens, r.CostUSD = m.LatencyMS, m.InputTokens+m.OutputTokens, m.CostUSD
	r.Rendered = segment.Render(res.Rule, res.Proposal.Language)
	if c.Expect.Rule != nil {
		want, _, _ := segment.Validate(*c.Expect.Rule)
		a, err := userIDs(ctx, db, want)
		if err != nil {
			return r, err
		}
		b, err := userIDs(ctx, db, res.Rule)
		if err != nil {
			return r, err
		}
		exact, j := compare(a, b)
		r.Exact, r.Jaccard, r.ExpectedUsers, r.ActualUsers = &exact, &j, len(a), len(b)
	}
	if c.Expect.Channels != "" {
		ok := res.Proposal.ChannelsRequested == c.Expect.Channels
		r.ChannelsOK = &ok
	}
	if c.Expect.Unsupported != nil {
		ok := (len(res.Proposal.UnsupportedCriteria) > 0) == *c.Expect.Unsupported
		r.UnsupportedOK = &ok
	}
	if c.Expect.ConsentNote {
		ok := slices.ContainsFunc(res.Proposal.Assumptions, func(s string) bool {
			s = strings.ToLower(s)
			return strings.Contains(s, "izin") || strings.Contains(s, "consent")
		})
		r.ConsentOK = &ok
	}

	// Content for the proposed channels, with the real worst case of this audience.
	aud, err := segment.Count(ctx, db, res.Rule)
	if err != nil {
		return r, err
	}
	wc, err := content.WorstCaseVars(ctx, db, res.Rule)
	if err != nil {
		return r, err
	}
	channels := []string{"sms", "email"}
	if ch := res.Proposal.ChannelsRequested; ch == "sms" || ch == "email" {
		channels = []string{ch}
	}
	cres, err := svc.ProposeContent(ctx, llm.ContentInput{Brief: c.Brief, Language: res.Proposal.Language, SegmentSummary: r.Rendered,
		Audience: aud, Channels: channels, WorstCaseByChannel: wc}, nil, nil)
	ok := err == nil
	if err != nil && !errors.As(err, &le) {
		return r, err
	}
	if err == nil {
		for _, ch := range channels {
			ok = ok && cres.Report.ValidFor[ch]
		}
		r.ContentTries = cres.Meta.Attempts
		r.LatencyMS += cres.Meta.LatencyMS
		r.Tokens += cres.Meta.InputTokens + cres.Meta.OutputTokens
		r.CostUSD += cres.Meta.CostUSD
	} else {
		r.ContentTries = le.Meta.Attempts
	}
	r.ContentValid = &ok
	return r, nil
}

func userIDs(ctx context.Context, db segment.Querier, v segment.ValidRule) (map[int64]bool, error) {
	where, args, err := segment.Compile(v, 1)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, "SELECT u.id FROM users u WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func compare(a, b map[int64]bool) (bool, float64) {
	inter := 0
	for id := range a {
		if b[id] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return true, 1
	}
	return inter == union, float64(inter) / float64(union)
}

// Summary aggregates the KICKOFF §6.11 metrics.
type Summary struct {
	Cases, SegmentValid, FirstTryValid, Repaired, Errors int
	ExactScored, ExactMatches                            int
	MeanJaccard                                          float64
	UnsupportedScored, UnsupportedOK                     int
	ConsentScored, ConsentOK                             int
	ErrorScored, ErrorOK                                 int
	ContentScored, ContentValid                          int
	P50MS, P95MS                                         int64
	Tokens                                               int
	CostUSD                                              float64
}

// Summarize computes the summary.
func Summarize(rs []Result) Summary {
	s := Summary{Cases: len(rs)}
	var lat []int64
	var jac float64
	for _, r := range rs {
		lat = append(lat, r.LatencyMS)
		s.Tokens += r.Tokens
		s.CostUSD += r.CostUSD
		if r.Error != "" {
			s.Errors++
		}
		if r.Valid {
			s.SegmentValid++
			if r.FirstTryValid {
				s.FirstTryValid++
			} else {
				s.Repaired++
			}
		}
		count := func(p *bool, scored, ok *int) {
			if p != nil {
				*scored++
				if *p {
					*ok++
				}
			}
		}
		count(r.Exact, &s.ExactScored, &s.ExactMatches)
		count(r.UnsupportedOK, &s.UnsupportedScored, &s.UnsupportedOK)
		count(r.ConsentOK, &s.ConsentScored, &s.ConsentOK)
		count(r.ErrorOK, &s.ErrorScored, &s.ErrorOK)
		count(r.ContentValid, &s.ContentScored, &s.ContentValid)
		if r.Jaccard != nil {
			jac += *r.Jaccard
		}
	}
	if s.ExactScored > 0 {
		s.MeanJaccard = jac / float64(s.ExactScored)
	}
	slices.SortFunc(lat, cmp.Compare[int64])
	s.P50MS, s.P95MS = percentile(lat, 0.50), percentile(lat, 0.95)
	return s
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

func ratio(ok, n int) string {
	if n == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d (%.0f%%)", ok, n, 100*float64(ok)/float64(n))
}

// Markdown renders docs/eval/REPORT-<provider>-<model>-<date>.md.
func Markdown(provider, model string, at time.Time, rs []Result) string {
	s := Summarize(rs)
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval report: %s / %s / %s\n\n", provider, model, at.Format("2006-01-02"))
	fmt.Fprintf(&b, "Generated by `make eval PROVIDER=%s` from `docs/eval/briefs.yaml` (%d briefs) on a fresh seed DB. Prompts: %s, %s.\n\n",
		provider, s.Cases, llm.SegmentPromptVersion, llm.ContentPromptVersion)
	if provider == "mock" {
		b.WriteString("> Mock provider: this run checks the eval plumbing, not model quality. The demo heuristic's keywords were\n" +
			"> written with these briefs in view, so its scores say nothing about generalization.\n\n")
	}
	b.WriteString("| Metric | Value |\n|---|---|\n")
	rows := [][2]string{
		{"Segment schema-valid, first try", ratio(s.FirstTryValid, s.Cases-s.ErrorScored)},
		{"Segment valid after repair", ratio(s.SegmentValid, s.Cases-s.ErrorScored)},
		{"Repair rate", ratio(s.Repaired, s.Cases-s.ErrorScored)},
		{"Exact audience match (user-id sets)", ratio(s.ExactMatches, s.ExactScored)},
		{"Mean Jaccard", fmt.Sprintf("%.3f", s.MeanJaccard)},
		{"unsupported_criteria detection", ratio(s.UnsupportedOK, s.UnsupportedScored)},
		{"Consent note when asked to bypass consent", ratio(s.ConsentOK, s.ConsentScored)},
		{"Expected errors (e.g. empty brief)", ratio(s.ErrorOK, s.ErrorScored)},
		{"Content valid for requested channels", ratio(s.ContentValid, s.ContentScored)},
		{"Latency p50 / p95 (segment + content)", fmt.Sprintf("%d ms / %d ms", s.P50MS, s.P95MS)},
		{"Tokens (input + output)", fmt.Sprint(s.Tokens)},
		{"Cost", fmt.Sprintf("$%.4f (%s)", s.CostUSD, llm.PriceSource)},
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s |\n", r[0], r[1])
	}
	b.WriteString("\n## Per brief\n\n| id | result | attempts | exact | Jaccard | users exp/act | channels | unsupported | consent | content | rendering |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	mark := func(p *bool) string {
		switch {
		case p == nil:
			return "–"
		case *p:
			return "✓"
		}
		return "✗"
	}
	for _, r := range rs {
		result := "valid"
		if r.Error != "" {
			result = r.Error
		}
		j := "–"
		if r.Jaccard != nil {
			j = fmt.Sprintf("%.3f", *r.Jaccard)
		}
		users := "–"
		if r.Exact != nil {
			users = fmt.Sprintf("%d/%d", r.ExpectedUsers, r.ActualUsers)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %s | %s | %s | %s | %s | %s |\n", r.ID, result, r.Attempts, mark(r.Exact), j, users,
			mark(r.ChannelsOK), mark(r.UnsupportedOK), mark(r.ConsentOK), mark(r.ContentValid),
			strings.ReplaceAll(strings.Join(r.Rendered, " · "), "|", "/"))
	}
	return b.String()
}

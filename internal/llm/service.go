package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/content"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/segment"
)

// MaxRepairs is the number of repair turns after the first attempt (KICKOFF §6.4).
const MaxRepairs = 2

// Default output limits. Adaptive thinking at effort low shares max_tokens with the answer, so the limits
// leave room for it; a max_tokens stop doubles the limit for the next attempt.
const (
	DefaultSegmentMaxTokens = 8192
	DefaultContentMaxTokens = 12288
	maxTokensCeiling        = 32768
)

// Service runs the two LLM tasks end to end.
type Service struct {
	provider       Provider
	recorder       Recorder
	metrics        *metrics.Metrics
	log            *slog.Logger
	segmentTimeout time.Duration
	contentTimeout time.Duration
	now            func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithMetrics emits the llm_* metrics of docs/contracts/observability.md.
func WithMetrics(m *metrics.Metrics) Option { return func(s *Service) { s.metrics = m } }

// WithTimeouts sets the per-call timeouts (LLM_SEGMENT_TIMEOUT, LLM_CONTENT_TIMEOUT).
func WithTimeouts(segment, content time.Duration) Option {
	return func(s *Service) {
		if segment > 0 {
			s.segmentTimeout = segment
		}
		if content > 0 {
			s.contentTimeout = content
		}
	}
}

// WithLogger sets the logger; message bodies are never logged.
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.log = l } }

// WithNow injects the time source used for latency (tests).
func WithNow(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// NewService wires a provider and a recorder (nil = no persistence).
func NewService(p Provider, r Recorder, opts ...Option) *Service {
	s := &Service{provider: p, recorder: r, log: slog.Default(), segmentTimeout: 30 * time.Second,
		contentTimeout: 60 * time.Second, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Provider exposes the configured adapter (for /api/system/status).
func (s *Service) Provider() Provider { return s.provider }

// ProposeSegment turns a brief into a validated segment (not streamed to the client).
func (s *Service) ProposeSegment(ctx context.Context, campaignID, brief string) (SegmentResult, error) {
	if err := checkBrief(brief); err != nil {
		return SegmentResult{}, err
	}
	sys, _, err := systemPrompts()
	if err != nil {
		return SegmentResult{}, fmt.Errorf("llm: prompts: %w", err)
	}
	var res SegmentResult
	validate := func(raw []byte) []segment.Issue {
		p, issues := segment.Parse(raw)
		if len(issues) > 0 {
			return issues
		}
		vr, errs, warns := segment.ValidateProposal(p)
		if len(errs) > 0 {
			return errs
		}
		res = SegmentResult{Proposal: p, Rule: vr, Warnings: warns}
		return nil
	}
	meta, err := s.run(ctx, job{
		task: PurposeSegment, campaignID: campaignID, promptVersion: SegmentPromptVersion,
		system: []SystemBlock{{Text: sys, Cache: true}}, user: segmentUserMessage(brief),
		schema: catalog.SegmentWireSchema(), maxTokens: DefaultSegmentMaxTokens, timeout: s.segmentTimeout,
		validate: validate,
	})
	if err != nil {
		return SegmentResult{}, err
	}
	res.Meta = meta
	return res, nil
}

type job struct {
	task          Purpose
	campaignID    string
	promptVersion string
	system        []SystemBlock
	user          string
	schema        map[string]any
	maxTokens     int
	timeout       time.Duration
	stream        bool
	onDelta       func(Delta)
	onRepair      func(RepairNotice)
	validate      func(raw []byte) []segment.Issue // nil issues = valid
}

// run is the shared pipeline: call → stop reason → strict decode + validate → at most MaxRepairs repair turns
// with the structured errors. Rules are never relaxed; every call is recorded and measured.
func (s *Service) run(ctx context.Context, j job) (Meta, error) {
	meta := Meta{Provider: s.provider.Name(), Model: s.provider.Model(), PromptVersion: j.promptVersion, CostRecorded: true}
	// record writes the llm_calls row; a failed insert flips cost_recorded but never fails the generation (F6).
	record := func(c Call) {
		if !s.finish(ctx, c, j.task) {
			meta.CostRecorded = false
		}
	}
	msgs := []Message{{Role: "user", Text: j.user}}
	maxTokens := j.maxTokens
	start := s.now()
	var last []segment.Issue
	for attempt := 0; attempt <= MaxRepairs; attempt++ {
		purpose := j.task
		if attempt > 0 {
			purpose = PurposeRepair
			if j.onRepair != nil {
				errJSON, _ := json.Marshal(last)
				j.onRepair(RepairNotice{Attempt: attempt, Errors: errJSON})
			}
		}
		req := Request{Purpose: purpose, Task: j.task, System: j.system, Messages: slices.Clone(msgs), Schema: j.schema,
			MaxTokens: maxTokens, Stream: j.stream}
		resp, callErr := s.call(ctx, req, j)
		meta.Attempts++
		cost := s.costOf(resp)
		meta.InputTokens += resp.Usage.InputTokens
		meta.OutputTokens += resp.Usage.OutputTokens
		meta.CacheReadTokens += resp.Usage.CacheReadTokens
		meta.CacheWriteTokens += resp.Usage.CacheWriteTokens
		meta.CostUSD += cost
		if resp.Model != "" {
			meta.Model = resp.Model
		}
		call := Call{CampaignID: j.campaignID, Purpose: purpose, Provider: meta.Provider, Model: meta.Model,
			PromptVersion: j.promptVersion, Usage: resp.Usage, CostUSD: cost, Latency: resp.Latency, StopReason: resp.StopReason}
		meta.LatencyMS = s.now().Sub(start).Milliseconds()

		if callErr != nil {
			call.Outcome, call.Error = "error", truncateErr(callErr.Error())
			var se *StatusError
			if ctx.Err() == nil && errors.As(callErr, &se) && IsRejection(se.Status) {
				// F7: the provider rejected the request itself (key, credits, bad request). Retrying cannot
				// help, so the user gets a clear code. The raw body can hold account identifiers: it goes to
				// the warn log only, never to llm_calls or the client.
				s.log.Warn("llm provider rejected the request", "campaign_id", j.campaignID, "task", j.task,
					"status", se.Status, "body", segment.SafeText(se.Body, 500))
				call.Error = fmt.Sprintf("provider_rejected status=%d", se.Status)
				record(call)
				return meta, &Error{Code: "llm_provider_rejected", Meta: meta, Err: &StatusError{Status: se.Status},
					Message: "The AI provider rejected the request (check the API key or credits)."}
			}
			record(call)
			code, msg := "llm_failed", "The language model call failed. Try again."
			if ctx.Err() != nil {
				msg = "The request was cancelled."
			}
			return meta, &Error{Code: code, Message: msg, Meta: meta, Err: callErr}
		}

		var issues []segment.Issue
		switch resp.StopReason {
		case StopRefusal:
			call.Outcome = "refused"
			record(call)
			category := resp.RefusalCategory
			if category == "" {
				category = "unknown"
			}
			// A refusal is shown to the user and never repaired (KICKOFF §6.3, need.md N-ORC-008).
			return meta, &Error{Code: "llm_refused", Message: "The model declined this request.", Meta: meta,
				Details: []segment.Issue{{Path: "", Code: category, Message: "The model declined to answer (category: " + category + ")."}}}
		case StopMaxTokens:
			issues = []segment.Issue{{Path: "", Code: "max_tokens", Message: "The answer was cut off before it was complete."}}
		default:
			issues = j.validate(resp.JSON)
		}
		// RT-1: whatever the validators produced, at most MaxIssues go to the repair turn, the client
		// (Error.Details), the log and the llm_calls row.
		issues = segment.CapIssues(issues, segment.MaxIssues)

		if len(issues) == 0 {
			call.Outcome = "valid"
			if attempt > 0 {
				call.Outcome = "repaired"
				meta.Repaired = true
			}
			record(call)
			return meta, nil
		}
		call.Outcome = "invalid"
		call.Error = truncateErr(issueCodes(issues))
		record(call)
		if s.metrics != nil {
			for _, is := range issues {
				s.metrics.LLMValidationFails.WithLabelValues(is.Code).Inc()
			}
		}
		last = issues
		if attempt == MaxRepairs {
			break
		}
		if s.metrics != nil {
			s.metrics.LLMRepairs.Inc()
		}
		if resp.StopReason == StopMaxTokens {
			// Truncated output is not worth showing back; retry the same turn with more room.
			maxTokens = min(maxTokens*2, maxTokensCeiling)
			continue
		}
		msgs = append(msgs, Message{Role: "assistant", Text: assistantEcho(resp.JSON)}, Message{Role: "user", Text: repairMessage(issues)})
	}
	return meta, &Error{Code: "llm_invalid_output", Message: fmt.Sprintf("The model's answer was still invalid after %d repairs.", MaxRepairs),
		Details: last, Meta: meta}
}

// call runs one provider call under the per-call timeout, forwarding deltas rune-safely.
func (s *Service) call(ctx context.Context, req Request, j job) (Response, error) {
	callCtx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()
	var onDelta func(Delta)
	var rs *runeStream
	if j.onDelta != nil {
		rs = &runeStream{out: j.onDelta}
		onDelta = rs.write
	}
	t0 := s.now()
	resp, err := s.provider.Generate(callCtx, req, onDelta)
	if resp.Latency == 0 {
		resp.Latency = s.now().Sub(t0)
	}
	if rs != nil {
		rs.flush()
	}
	if err == nil && callCtx.Err() != nil {
		err = callCtx.Err() // a provider that returned without noticing the deadline
	}
	return resp, err
}

func (s *Service) costOf(r Response) float64 {
	if r.CostUSD > 0 {
		return r.CostUSD
	}
	if p, ok := PriceFor(r.Model); ok {
		return p.Cost(r.Usage)
	}
	if p, ok := PriceFor(s.provider.Model()); ok {
		return p.Cost(r.Usage)
	}
	return 0
}

// finish records the call (even when the client went away) and updates metrics. It reports whether the
// llm_calls row was stored (true when there is no recorder).
func (s *Service) finish(ctx context.Context, c Call, task Purpose) (recorded bool) {
	if s.metrics != nil {
		s.metrics.LLMRequests.WithLabelValues(string(c.Purpose), c.Provider, c.Model, c.Outcome).Inc()
		s.metrics.LLMTokens.WithLabelValues("input").Add(float64(c.Usage.InputTokens))
		s.metrics.LLMTokens.WithLabelValues("output").Add(float64(c.Usage.OutputTokens))
		s.metrics.LLMTokens.WithLabelValues("cache_read").Add(float64(c.Usage.CacheReadTokens))
		s.metrics.LLMTokens.WithLabelValues("cache_write").Add(float64(c.Usage.CacheWriteTokens))
		s.metrics.LLMCostUSD.Add(c.CostUSD)
		s.metrics.LLMLatency.WithLabelValues(string(c.Purpose)).Observe(c.Latency.Seconds())
	}
	s.log.Info("llm call", "campaign_id", c.CampaignID, "task", task, "purpose", c.Purpose, "provider", c.Provider,
		"model", c.Model, "outcome", c.Outcome, "stop_reason", c.StopReason, "latency_ms", c.Latency.Milliseconds(),
		"input_tokens", c.Usage.InputTokens, "output_tokens", c.Usage.OutputTokens, "cache_read_tokens", c.Usage.CacheReadTokens)
	if s.recorder == nil {
		return true
	}
	// Accounting must survive a client disconnect, but must not hang a request either.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.recorder.RecordCall(rctx, c); err != nil {
		// Losing one accounting row must not fail the user's generation; the log line keeps the data, and
		// cost_recorded=false tells the UI the cost is missing (F6).
		s.log.Error("llm_calls insert failed", "campaign_id", c.CampaignID, "purpose", c.Purpose, "err", err)
		return false
	}
	return true
}

func checkBrief(brief string) error {
	n := utf8.RuneCountInString(brief)
	switch {
	case strings.TrimSpace(brief) == "":
		return &Error{Code: "validation_failed", Message: "The brief is empty.",
			Details: []segment.Issue{{Path: "brief", Code: "required", Message: "Write a campaign brief first."}}}
	case n > catalog.MaxBriefRunes:
		return &Error{Code: "validation_failed", Message: "The brief is too long.",
			Details: []segment.Issue{{Path: "brief", Code: "too_long", Message: fmt.Sprintf("The brief has %d characters; the limit is %d.", n, catalog.MaxBriefRunes)}}}
	case !utf8.ValidString(brief):
		return &Error{Code: "validation_failed", Message: "The brief is not valid UTF-8.",
			Details: []segment.Issue{{Path: "brief", Code: "invalid_value", Message: "The brief contains invalid characters."}}}
	}
	return nil
}

func normalizeChannels(chs []string) []string {
	var out []string
	for _, c := range catalog.Channels {
		if len(chs) == 0 || slices.Contains(chs, c) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return slices.Clone(catalog.Channels)
	}
	return out
}

func channelOf(path string) string {
	if path == content.PathSMS {
		return "sms"
	}
	return "email"
}

func toSegmentIssues(in []content.Issue) []segment.Issue {
	out := make([]segment.Issue, len(in))
	for i, is := range in {
		out[i] = segment.Issue(is)
	}
	return out
}

// repairMessage sends the structured errors back, at most segment.MaxIssues of them. Messages are the
// validators' own text; the few that name model-chosen text (an unknown key or variable) quote at most 40
// sanitized runes (segment.SafeText), so a hostile value cannot use the repair turn, a user turn, to replay
// a long instruction.
func repairMessage(issues []segment.Issue) string {
	b, _ := json.Marshal(segment.CapIssues(issues, segment.MaxIssues))
	return "Your previous answer failed validation. The errors, as JSON [{path, code, message}]:\n" + string(b) +
		"\nReturn the complete corrected JSON object. Fix exactly these problems, keep the brief's intent, and follow every rule from the instructions."
}

// assistantEcho is the previous answer as an assistant turn; the API rejects empty text blocks.
func assistantEcho(raw []byte) string {
	s := strings.ToValidUTF8(string(raw), "�")
	if strings.TrimSpace(s) == "" {
		return "(empty answer)"
	}
	return s
}

func issueCodes(issues []segment.Issue) string {
	codes := make([]string, 0, len(issues))
	for _, i := range issues {
		codes = append(codes, i.Path+":"+i.Code)
	}
	return strings.Join(codes, ",")
}

// truncateErr makes s storable in llm_calls.error: valid UTF-8 without NUL (Postgres text rejects NUL, and an
// issue path can carry a model-chosen key), at most 500 bytes (RT-7).
func truncateErr(s string) string {
	s = pgText(s)
	if len(s) <= 500 {
		return s
	}
	return strings.ToValidUTF8(s[:500], "")
}

// runeStream forwards streamed text without ever cutting a multibyte rune: bytes are accumulated and only
// the prefix that ends on a rune boundary is emitted; an incomplete trailing sequence waits for the next
// fragment (KICKOFF §6.5 "Bytes and runes").
type runeStream struct {
	buf []byte
	out func(Delta)
}

func (r *runeStream) write(d Delta) {
	r.buf = append(r.buf, d.JSONFragment...)
	n := completePrefix(r.buf)
	if n == 0 {
		return
	}
	r.out(Delta{JSONFragment: strings.ToValidUTF8(string(r.buf[:n]), "�")})
	r.buf = append(r.buf[:0], r.buf[n:]...)
}

func (r *runeStream) flush() {
	if len(r.buf) > 0 {
		r.out(Delta{JSONFragment: strings.ToValidUTF8(string(r.buf), "�")})
		r.buf = r.buf[:0]
	}
}

// completePrefix returns the length of the longest prefix of b that does not end inside a rune.
func completePrefix(b []byte) int {
	for i := 1; i <= utf8.UTFMax && i <= len(b); i++ {
		if utf8.RuneStart(b[len(b)-i]) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return len(b) - i
			}
			return len(b)
		}
	}
	return len(b)
}

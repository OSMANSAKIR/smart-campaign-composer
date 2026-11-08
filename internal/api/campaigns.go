package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/segment"
)

// ContentPromptVersion is announced in the `meta` event before the model answers; it must match the prompt
// file the llm service uses (internal/llm/prompts/content_v1.tmpl).
const ContentPromptVersion = "content_v1"

// maxBody bounds JSON request bodies; the largest legitimate body is an email of 5000 runes.
const maxBody = 1 << 20

// pingEvery keeps idle SSE connections (and proxies in between) alive.
const pingEvery = 15 * time.Second

// statusOf maps API error codes to HTTP statuses (docs/contracts/api.md §1).
var statusOf = map[string]int{
	"bad_request": 400, "idempotency_key_required": 400, "not_found": 404,
	"invalid_state": 409, "stale_content_version": 409, "approve_in_progress": 409, "already_approved": 409,
	"validation_failed": 422, "llm_invalid_output": 422, "llm_refused": 422, "content_invalid": 422, "empty_audience": 422,
	"llm_failed": 502, "unavailable": 503,
}

func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Brief *string `json:"brief"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Brief == nil {
		writeError(w, 400, "bad_request", "Field `brief` is required.", []Issue{{Path: "brief", Code: "required", Message: "missing"}})
		return
	}
	c, err := s.campaigns.Create(r.Context(), *req.Brief)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request) {
	limit, ok := intParam(w, r, "limit", 20, 1, 100)
	if !ok {
		return
	}
	items, next, err := s.campaigns.List(r.Context(), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nullable(next)})
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) {
	c, err := s.campaigns.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) generateSegment(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.d.SegmentTimeout)
	defer cancel()
	c, meta, err := s.campaigns.GenerateSegment(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaign": c, "llm": meta})
}

func (s *Server) putSegment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule *segment.Rule `json:"rule"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Rule == nil {
		writeError(w, 400, "bad_request", "Field `rule` is required.", []Issue{{Path: "rule", Code: "required", Message: "missing"}})
		return
	}
	c, err := s.campaigns.PutSegment(r.Context(), r.PathValue("id"), *req.Rule)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaign": c})
}

// generateContent streams the model's JSON as SSE (docs/contracts/api.md §5). Errors before the stream starts
// are plain JSON; afterwards they are an `error` event. Closing the request cancels the model call.
func (s *Server) generateContent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, err := s.campaigns.ContentInput(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.d.ContentTimeout)
	defer cancel()
	sse := newSSE(w)
	p := s.d.LLM.Provider()
	sse.send("meta", map[string]string{"provider": p.Name(), "model": p.Model(), "prompt_version": ContentPromptVersion})
	cv, meta, err := s.campaigns.GenerateContent(ctx, id, in,
		func(d llm.Delta) { sse.send("delta", map[string]string{"text": d.JSONFragment}) },
		func(n llm.RepairNotice) {
			errs := n.Errors
			if len(errs) == 0 {
				errs = json.RawMessage("[]")
			}
			sse.send("repairing", map[string]any{"attempt": n.Attempt, "errors": errs})
		})
	if err != nil {
		code, msg, details := s.describe(r, err)
		sse.send("error", map[string]any{"error": map[string]any{"code": code, "message": msg, "details": details}})
		return
	}
	sse.send("result", map[string]any{"content": cv, "llm": meta})
}

func (s *Server) putContent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SMSText      *string `json:"sms_text"`
		EmailSubject *string `json:"email_subject"`
		EmailBody    *string `json:"email_body"`
		BaseVersion  *int    `json:"base_version"`
	}
	if !decode(w, r, &req) {
		return
	}
	var missing []Issue
	for name, v := range map[string]bool{"sms_text": req.SMSText == nil, "email_subject": req.EmailSubject == nil,
		"email_body": req.EmailBody == nil, "base_version": req.BaseVersion == nil} {
		if v {
			missing = append(missing, Issue{Path: name, Code: "required", Message: "missing"})
		}
	}
	if len(missing) > 0 {
		writeError(w, 400, "bad_request", "Fields sms_text, email_subject, email_body and base_version are required.", missing)
		return
	}
	cv, err := s.campaigns.SaveContent(r.Context(), r.PathValue("id"),
		content.Content{SMSText: *req.SMSText, EmailSubject: *req.EmailSubject, EmailBody: *req.EmailBody}, *req.BaseVersion)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": cv})
}

func (s *Server) validateContent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SMSText      string `json:"sms_text"`
		EmailSubject string `json:"email_subject"`
		EmailBody    string `json:"email_body"`
		CampaignID   string `json:"campaign_id"`
		Language     string `json:"language"`
	}
	if !decode(w, r, &req) {
		return
	}
	rep, err := s.campaigns.ValidateContent(r.Context(),
		content.Content{SMSText: req.SMSText, EmailSubject: req.EmailSubject, EmailBody: req.EmailBody}, req.CampaignID, req.Language)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channels       []string `json:"channels"`
		ContentVersion *int     `json:"content_version"`
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeError(w, 400, "idempotency_key_required", "Send an Idempotency-Key header with every approve.", nil)
		return
	}
	if !decode(w, r, &req) {
		return
	}
	if req.ContentVersion == nil {
		writeError(w, 400, "bad_request", "Field `content_version` is required.", []Issue{{Path: "content_version", Code: "required", Message: "missing"}})
		return
	}
	res, err := s.campaigns.Approve(r.Context(), r.PathValue("id"), key, req.Channels, *req.ContentVersion)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	limit, ok := intParam(w, r, "limit", 50, 1, 500)
	if !ok {
		return
	}
	q := r.URL.Query()
	items, next, err := s.campaigns.Messages(r.Context(), r.PathValue("id"), q.Get("status"), q.Get("channel"), limit, q.Get("cursor"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nullable(next)})
}

// events is the live progress stream (docs/contracts/api.md §7): a full snapshot first, then snapshots on
// change, recent attempts as `event`, and `done` once the campaign is terminal.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.campaigns.Get(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	sub, unsubscribe := s.hub.Subscribe(id) // before the first snapshot, so no change can fall in between
	defer unsubscribe()
	p, err := s.campaigns.Progress(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sse := newSSE(w)
	sse.send("snapshot", p)
	if campaign.Terminal(p.Status) {
		sse.send("done", map[string]string{"status": p.Status})
		return
	}
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.closing:
			return
		case <-ping.C:
			sse.comment("ping")
		case <-sub.C():
			u, ok := sub.Take()
			if !ok {
				continue
			}
			for _, e := range u.Events {
				sse.send("event", e)
			}
			if u.Snapshot != nil {
				sse.send("snapshot", u.Snapshot)
			}
			if u.Done {
				status := p.Status
				if u.Snapshot != nil {
					status = u.Snapshot.Status
				}
				sse.send("done", map[string]string{"status": status})
				return
			}
		}
	}
}

// fail writes err in the error model; unexpected errors are logged and reported as `internal`.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	code, msg, details := s.describe(r, err)
	status, ok := statusOf[code]
	if !ok {
		status = http.StatusInternalServerError
	}
	writeError(w, status, code, msg, details)
}

func (s *Server) describe(r *http.Request, err error) (code, msg string, details []Issue) {
	var ce *campaign.Error
	if errors.As(err, &ce) {
		for _, d := range ce.Details {
			details = append(details, Issue{Path: d.Path, Code: d.Code, Message: d.Message})
		}
		return ce.Code, ce.Message, orEmptyIssues(details)
	}
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return "internal", "The request was cancelled.", []Issue{}
	}
	reqID := strconv.FormatInt(time.Now().UnixNano(), 36)
	if s.d.Log != nil {
		s.d.Log.Error("request failed", "route", r.Pattern, "request_id", reqID, "campaign_id", r.PathValue("id"), "err", err)
	}
	return "internal", "Unexpected server error (request id " + reqID + ").", []Issue{}
}

func orEmptyIssues(in []Issue) []Issue {
	if in == nil {
		return []Issue{}
	}
	return in
}

// decode reads a JSON body; on failure it answers 400 and returns false.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		msg := "The request body is not valid JSON."
		if errors.Is(err, io.EOF) {
			msg = "The request body is empty."
		}
		writeError(w, 400, "bad_request", msg, []Issue{{Path: "", Code: "invalid_json", Message: err.Error()}})
		return false
	}
	if dec.More() {
		writeError(w, 400, "bad_request", "Trailing data after the JSON body.", nil)
		return false
	}
	return true
}

func intParam(w http.ResponseWriter, r *http.Request, name string, def, lo, hi int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		writeError(w, 400, "bad_request", fmt.Sprintf("`%s` must be an integer from %d to %d.", name, lo, hi),
			[]Issue{{Path: name, Code: "invalid", Message: raw}})
		return 0, false
	}
	return n, true
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// sseWriter writes server-sent events; it is safe for concurrent use because model callbacks may arrive on
// another goroutine.
type sseWriter struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

func newSSE(w http.ResponseWriter) *sseWriter {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &sseWriter{w: w, rc: http.NewResponseController(w)}
	_ = s.rc.Flush()
	return s
}

func (s *sseWriter) send(event string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		b = []byte(`{"error":{"code":"internal","message":"unencodable event","details":[]}}`)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// json.Marshal never emits raw newlines, so one data line per event is enough.
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b)
	_ = s.rc.Flush()
}

func (s *sseWriter) comment(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.w, ": %s\n\n", text)
	_ = s.rc.Flush()
}

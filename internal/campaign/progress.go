package campaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"smartcampaign/internal/catalog"
	"smartcampaign/internal/clock"
)

// ChannelProgress is one channel's counters in the progress object (docs/contracts/api.md §7).
type ChannelProgress struct {
	Total            int        `json:"total"`
	Queued           int        `json:"queued"`
	Sending          int        `json:"sending"`
	Sent             int        `json:"sent"`
	Retry            int        `json:"retry"`
	Failed           int        `json:"failed"`
	SkippedNoConsent int        `json:"skipped_no_consent"`
	ThroughputPerS   float64    `json:"throughput_per_s"`
	PausedUntil      *time.Time `json:"paused_until"`
}

// Progress is the full progress snapshot; every snapshot is complete so a reconnecting client replaces its state.
type Progress struct {
	CampaignID string                      `json:"campaign_id"`
	Status     string                      `json:"status"`
	Channels   map[string]*ChannelProgress `json:"channels"`
	Errors     map[string]int              `json:"errors"`
	ETASeconds *int                        `json:"eta_seconds"`
	StartedAt  *time.Time                  `json:"started_at"`
	UpdatedAt  time.Time                   `json:"updated_at"`
}

// Event is one recent delivery attempt for the live feed (best effort).
type Event struct {
	ID         int64     `json:"-"`
	At         time.Time `json:"at"`
	MessageID  string    `json:"message_id"`
	Channel    string    `json:"channel"`
	Attempt    int       `json:"attempt"`
	Outcome    string    `json:"outcome"` // sent | error
	HTTPStatus *int      `json:"http_status"`
	ErrorClass string    `json:"error_class"`
}

// throughputWindow is the look-back for throughput_per_s.
const throughputWindow = 5 * time.Second

// errorBuckets maps delivery_attempts.error_class to the dashboard's error breakdown.
var errorBuckets = map[string]string{
	"throttled": "rate_limited", "transient": "server_error", "ambiguous": "timeout", "unreachable": "timeout",
	"permanent": "permanent", "config": "auth_error", // 401/403 pause the channel and retry; not permanent
}

// Terminal reports whether a campaign status is final.
func Terminal(status string) bool {
	return status == StatusCompleted || status == StatusCompletedWithFailures
}

// Progress computes the snapshot with a few aggregate queries over the messages of one campaign.
func (s *Service) Progress(ctx context.Context, id string) (Progress, error) {
	p := Progress{CampaignID: id, Channels: map[string]*ChannelProgress{},
		Errors: map[string]int{"rate_limited": 0, "server_error": 0, "timeout": 0, "permanent": 0, "auth_error": 0}}
	for _, ch := range catalog.Channels {
		p.Channels[ch] = &ChannelProgress{}
	}
	var now time.Time
	err := s.Pool.QueryRow(ctx, `SELECT status, approved_at, now() FROM campaigns WHERE id = $1`, id).Scan(&p.Status, &p.StartedAt, &now)
	if err != nil {
		return Progress{}, notFound(err)
	}
	p.UpdatedAt = now.UTC()

	rows, err := s.Pool.Query(ctx, `SELECT channel, status, count(*),
			count(*) FILTER (WHERE sent_at > now() - make_interval(secs => $2))
		FROM messages WHERE campaign_id = $1 GROUP BY channel, status`, id, throughputWindow.Seconds())
	if err != nil {
		return Progress{}, err
	}
	for rows.Next() {
		var ch, st string
		var n, recent int
		if err := rows.Scan(&ch, &st, &n, &recent); err != nil {
			rows.Close()
			return Progress{}, err
		}
		cp := p.Channels[ch]
		if cp == nil {
			continue
		}
		cp.Total += n
		switch st {
		case "queued":
			cp.Queued = n
		case "sending":
			cp.Sending = n
		case "sent":
			cp.Sent = n
			cp.ThroughputPerS = math.Round(float64(recent)/throughputWindow.Seconds()*10) / 10
		case "retry":
			cp.Retry = n
		case "failed":
			cp.Failed = n
		case "skipped_no_consent":
			cp.SkippedNoConsent = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Progress{}, err
	}

	rows, err = s.Pool.Query(ctx, `SELECT da.error_class, count(*) FROM delivery_attempts da
		JOIN messages m ON m.id = da.message_id WHERE m.campaign_id = $1 AND da.error_class <> 'success'
		GROUP BY da.error_class`, id)
	if err != nil {
		return Progress{}, err
	}
	for rows.Next() {
		var class string
		var n int
		if err := rows.Scan(&class, &n); err != nil {
			rows.Close()
			return Progress{}, err
		}
		if b, ok := errorBuckets[class]; ok {
			p.Errors[b] += n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Progress{}, err
	}

	rows, err = s.Pool.Query(ctx, `SELECT channel, max(paused_until) FROM worker_heartbeats
		WHERE paused_until > now() AND at > now() - interval '10 seconds' GROUP BY channel`)
	if err != nil {
		return Progress{}, err
	}
	for rows.Next() {
		var ch string
		var until time.Time
		if err := rows.Scan(&ch, &until); err != nil {
			rows.Close()
			return Progress{}, err
		}
		if cp := p.Channels[ch]; cp != nil {
			u := until.UTC()
			cp.PausedUntil = &u
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Progress{}, err
	}

	remaining, rate := 0, 0.0
	for _, cp := range p.Channels {
		remaining += cp.Queued + cp.Sending + cp.Retry
		rate += cp.ThroughputPerS
	}
	switch {
	case remaining == 0 && p.StartedAt != nil:
		eta := 0
		p.ETASeconds = &eta
	case rate > 0:
		eta := int(math.Ceil(float64(remaining) / rate))
		p.ETASeconds = &eta
	}
	return p, nil
}

// Events returns delivery attempts of the campaign after the given attempt id, oldest first.
func (s *Service) Events(ctx context.Context, id string, after int64, limit int) ([]Event, error) {
	rows, err := s.Pool.Query(ctx, `SELECT da.id, da.at, da.message_id::text, m.channel, da.attempt, da.http_status, da.error_class
		FROM delivery_attempts da JOIN messages m ON m.id = da.message_id
		WHERE m.campaign_id = $1 AND da.id > $2 ORDER BY da.id LIMIT $3`, id, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		err := r.Scan(&e.ID, &e.At, &e.MessageID, &e.Channel, &e.Attempt, &e.HTTPStatus, &e.ErrorClass)
		e.Outcome = "error"
		if e.ErrorClass == "success" {
			e.Outcome = "sent"
		}
		return e, err
	})
}

func (s *Service) lastEventID(ctx context.Context, id string) (int64, error) {
	var last int64
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(max(da.id), 0) FROM delivery_attempts da
		JOIN messages m ON m.id = da.message_id WHERE m.campaign_id = $1`, id).Scan(&last)
	return last, err
}

// Update is what a subscriber receives: the newest snapshot (if it changed), new events, and whether the
// campaign reached a terminal status.
type Update struct {
	Snapshot *Progress
	Events   []Event
	Done     bool
}

// Hub polls each watched campaign once per interval and fans the result out to all its subscribers, so many
// open dashboards cost one set of queries (need.md N-ORC-006). LISTEN/NOTIFY is the next step if needed.
type Hub struct {
	svc      *Service
	clock    clock.Clock
	interval time.Duration
	log      func(msg string, args ...any)

	mu    sync.Mutex
	feeds map[string]*feed
	wg    sync.WaitGroup
}

type feed struct {
	subs   map[*Sub]struct{}
	cancel context.CancelFunc
}

// Sub is one subscriber. Pending updates are merged, never queued, so a slow client cannot block the hub.
type Sub struct {
	mu      sync.Mutex
	pending Update
	has     bool
	notify  chan struct{}
}

// maxPendingEvents caps the events kept for a slow subscriber; the feed is best effort.
const maxPendingEvents = 100

// C is signalled when an update is pending.
func (s *Sub) C() <-chan struct{} { return s.notify }

// Take returns and clears the pending update.
func (s *Sub) Take() (Update, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.pending, s.has
	s.pending, s.has = Update{}, false
	return u, ok
}

func (s *Sub) push(u Update) {
	s.mu.Lock()
	if u.Snapshot != nil {
		s.pending.Snapshot = u.Snapshot
	}
	s.pending.Events = append(s.pending.Events, u.Events...)
	if n := len(s.pending.Events); n > maxPendingEvents {
		s.pending.Events = s.pending.Events[n-maxPendingEvents:]
	}
	s.pending.Done = s.pending.Done || u.Done
	s.has = true
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// NewHub returns a hub polling every interval.
func NewHub(svc *Service, c clock.Clock, interval time.Duration) *Hub {
	h := &Hub{svc: svc, clock: c, interval: interval, feeds: map[string]*feed{}, log: func(string, ...any) {}}
	if svc.Log != nil {
		h.log = svc.Log.Warn
	}
	return h
}

// Subscribe starts watching a campaign; call the returned function to stop.
func (h *Hub) Subscribe(id string) (*Sub, func()) {
	sub := &Sub{notify: make(chan struct{}, 1)}
	h.mu.Lock()
	f := h.feeds[id]
	if f == nil {
		ctx, cancel := context.WithCancel(context.Background())
		f = &feed{subs: map[*Sub]struct{}{}, cancel: cancel}
		h.feeds[id] = f
		h.wg.Go(func() { h.run(ctx, id, f) })
	}
	f.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(f.subs, sub)
		if len(f.subs) == 0 && h.feeds[id] == f {
			delete(h.feeds, id)
			f.cancel()
		}
	}
}

// Close stops every feed and waits for the pollers to exit.
func (h *Hub) Close() {
	h.mu.Lock()
	for id, f := range h.feeds {
		f.cancel()
		delete(h.feeds, id)
	}
	h.mu.Unlock()
	h.wg.Wait()
}

func (h *Hub) broadcast(f *feed, u Update) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range f.subs {
		s.push(u)
	}
}

func (h *Hub) run(ctx context.Context, id string, f *feed) {
	lastEvent, err := h.svc.lastEventID(ctx, id)
	if err != nil && ctx.Err() == nil {
		h.log("progress feed", "campaign_id", id, "err", err)
	}
	var lastJSON []byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.clock.After(h.interval):
		}
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		p, err := h.svc.Progress(qctx, id)
		var events []Event
		if err == nil {
			events, err = h.svc.Events(qctx, id, lastEvent, maxPendingEvents)
		}
		cancel()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
				h.log("progress feed", "campaign_id", id, "err", err)
			}
			continue // transient DB trouble: keep the last state, try again next tick
		}
		if len(events) > 0 {
			lastEvent = events[len(events)-1].ID
		}
		u := Update{Events: events, Done: Terminal(p.Status)}
		stamp := p.UpdatedAt
		p.UpdatedAt = time.Time{}
		cur, _ := json.Marshal(p)
		p.UpdatedAt = stamp
		if !bytes.Equal(cur, lastJSON) || u.Done {
			lastJSON = cur
			u.Snapshot = &p
		}
		if u.Snapshot != nil || len(u.Events) > 0 || u.Done {
			h.broadcast(f, u)
		}
		if u.Done {
			return
		}
	}
}

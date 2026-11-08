package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/clock"
	"smartcampaign/internal/metrics"
)

// lockKeyBase namespaces the per-channel advisory locks (distinct from the migration lock).
const lockKeyBase = 7_261_108_100

// LockKey is the advisory-lock key of a channel's single active dispatcher.
func LockKey(ch Channel) int64 {
	if ch == Email {
		return lockKeyBase + 2
	}
	return lockKeyBase + 1
}

// Dispatcher runs the send loop for one channel (KICKOFF §7.2): it holds the channel's advisory lock on a
// dedicated connection (replicas are hot standbys), claims due messages with leases, paces every send, retry
// included, through one sliding-window limiter, and records each outcome.
//
// Only Channel, Pool, Client and Metrics are required; zero values of the other fields take the defaults
// noted on them.
type Dispatcher struct {
	Channel Channel
	Pool    *pgxpool.Pool
	Client  ProviderClient
	Metrics *metrics.Metrics
	Log     *slog.Logger

	Limiter        Limiter        // default: sliding window of RateLimit per second+WindowGuard
	RateLimit      int            // default 100
	WindowGuard    time.Duration  // added to the 1 s window so arrival jitter cannot push the receiver over the limit; default 25 ms
	Policy         RetryPolicy    // default DefaultRetryPolicy
	Clock          clock.Clock    // default clock.Real
	WorkerID       string         // default hostname-pid
	MaxInFlight    int            // default 32
	Lease          time.Duration  // default 30 s
	DrainTimeout   time.Duration  // default 10 s
	PollInterval   time.Duration  // idle wait between empty claims; default 200 ms
	ConfigPause    time.Duration  // pause after 401/403; default 30 s
	HeartbeatEvery time.Duration  // default 2 s
	StandbyPoll    time.Duration  // how often a standby retries the advisory lock; default 1 s
	Rand           func() float64 // jitter source; default math/rand/v2

	once   sync.Once
	queue  *Queue
	tmpl   sync.Map // "campaign/version" -> Template (immutable once approved)
	holds  atomic.Bool
	paused atomic.Int64 // unix nanos until which the channel is paused (0 = not paused)
	loop   atomic.Int64 // unix nanos of the last pass through the main loop (readiness)
}

func (d *Dispatcher) setup() {
	d.once.Do(func() {
		if d.Clock == nil {
			d.Clock = clock.Real{}
		}
		if d.Metrics == nil {
			d.Metrics = metrics.New()
		}
		if d.Log == nil {
			d.Log = slog.New(slog.DiscardHandler)
		}
		d.Log = d.Log.With("channel", string(d.Channel))
		if d.RateLimit <= 0 {
			d.RateLimit = 100
		}
		if d.WindowGuard < 0 {
			d.WindowGuard = 0
		} else if d.WindowGuard == 0 {
			d.WindowGuard = 25 * time.Millisecond
		}
		if d.Limiter == nil {
			d.Limiter = NewSlidingWindowLimiter(d.RateLimit, time.Second+d.WindowGuard, d.Clock)
		}
		if d.Policy.MaxAttempts == 0 {
			d.Policy = DefaultRetryPolicy
		}
		if d.WorkerID == "" {
			host, _ := os.Hostname()
			d.WorkerID = host + "-" + strconv.Itoa(os.Getpid())
		}
		d.MaxInFlight = defaultInt(d.MaxInFlight, 32)
		d.Lease = defaultDur(d.Lease, 30*time.Second)
		d.DrainTimeout = defaultDur(d.DrainTimeout, 10*time.Second)
		d.PollInterval = defaultDur(d.PollInterval, 200*time.Millisecond)
		d.ConfigPause = defaultDur(d.ConfigPause, 30*time.Second)
		d.HeartbeatEvery = defaultDur(d.HeartbeatEvery, 2*time.Second)
		d.StandbyPoll = defaultDur(d.StandbyPoll, time.Second)
		d.StandbyPoll = defaultDur(d.StandbyPoll, time.Second)
		if d.Rand == nil {
			d.Rand = rand.Float64
		}
		d.queue = &Queue{Pool: d.Pool, Channel: d.Channel, Lease: d.Lease}
		InitMetrics(d.Metrics)
	})
}

func defaultInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func defaultDur(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}

// HoldsLock reports whether this dispatcher is the active one for its channel.
func (d *Dispatcher) HoldsLock() bool { return d.holds.Load() }

// PausedUntil returns when the channel's pause ends, or the zero time when it is not paused.
func (d *Dispatcher) PausedUntil() time.Time {
	n := d.paused.Load()
	if n == 0 {
		return time.Time{}
	}
	if t := time.Unix(0, n); t.After(d.nowOrReal()) {
		return t
	}
	return time.Time{}
}

// LastLoop is when the main loop last ran (also while standing by); /readyz requires it to be fresh.
func (d *Dispatcher) LastLoop() time.Time {
	n := d.loop.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

func (d *Dispatcher) nowOrReal() time.Time {
	if d.Clock == nil {
		return time.Now()
	}
	return d.Clock.Now()
}

func (d *Dispatcher) touch() { d.loop.Store(time.Now().UnixNano()) }

// sleep waits for dur on the dispatcher clock; false means ctx ended. It keeps the readiness beat fresh
// while waiting.
func (d *Dispatcher) sleep(ctx context.Context, dur time.Duration) bool {
	for dur > 0 {
		d.touch()
		step := min(dur, time.Second)
		select {
		case <-ctx.Done():
			return false
		case <-d.Clock.After(step):
		}
		dur -= step
	}
	return ctx.Err() == nil
}

// Run blocks until ctx is cancelled. On cancel it stops claiming, lets in-flight sends finish (at most
// DrainTimeout) and hands back the leases of messages it had claimed but not yet sent (KICKOFF §7.5).
func (d *Dispatcher) Run(ctx context.Context) error {
	d.setup()
	if d.Pool == nil || d.Client == nil {
		<-ctx.Done()
		return nil
	}
	var hb sync.WaitGroup
	hbCtx, stopHB := context.WithCancel(context.WithoutCancel(ctx))
	hb.Go(func() { d.heartbeatLoop(hbCtx) })
	defer func() {
		stopHB()
		hb.Wait()
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = d.queue.DropHeartbeat(dctx, d.WorkerID)
	}()

	backoff := 250 * time.Millisecond
	for ctx.Err() == nil {
		d.touch()
		conn, got, err := d.tryLock(ctx)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				d.Log.Warn("dispatcher: database unavailable, backing off", "err", err, "retry_in", backoff)
			}
			d.sleep(ctx, backoff)
			backoff = min(backoff*2, 5*time.Second)
			continue
		case !got:
			backoff = 250 * time.Millisecond
			d.sleep(ctx, d.StandbyPoll) // standby: try again shortly
			continue
		}
		backoff = 250 * time.Millisecond
		d.holds.Store(true)
		d.Log.Info("dispatcher: lock acquired", "worker_id", d.WorkerID)
		err = d.active(ctx, conn)
		d.holds.Store(false)
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = conn.Close(closeCtx) // closing the session releases the advisory lock
		cancel()
		if err != nil && ctx.Err() == nil {
			d.Log.Warn("dispatcher: lost the database connection, re-acquiring", "err", err)
			d.sleep(ctx, backoff)
		}
	}
	return nil
}

// tryLock opens the dedicated lock connection and tries pg_try_advisory_lock. The connection stays open
// only when the lock was obtained.
func (d *Dispatcher) tryLock(ctx context.Context) (*pgx.Conn, bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(cctx, d.Pool.Config().ConnConfig)
	if err != nil {
		return nil, false, err
	}
	var got bool
	if err := conn.QueryRow(cctx, "SELECT pg_try_advisory_lock($1)", LockKey(d.Channel)).Scan(&got); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, false, err
	}
	if !got {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, false, nil
	}
	return conn, true, nil
}

// active is the claim loop of the lock holder. It returns nil on shutdown and an error when the lock
// connection died (the lock is gone then, so the caller must stop claiming and re-acquire).
func (d *Dispatcher) active(ctx context.Context, lock *pgx.Conn) error {
	var inflight sync.WaitGroup
	sem := make(chan struct{}, d.MaxInFlight)
	lockCheck, lastReclaim := time.Now(), time.Time{}
	dbBackoff := 100 * time.Millisecond
	defer d.drain(&inflight)

	// Sends and their bookkeeping must survive a shutdown signal long enough to finish (drain), so they run
	// on a context that ignores cancellation; only the pre-send waits watch ctx.
	bg := context.WithoutCancel(ctx)

	for ctx.Err() == nil {
		d.touch()
		if time.Since(lockCheck) >= time.Second {
			pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err := lock.Exec(pctx, "SELECT 1")
			cancel()
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("lock connection: %w", err)
			}
			lockCheck = time.Now()
		}
		if until := d.PausedUntil(); !until.IsZero() {
			d.Metrics.ChannelPaused.WithLabelValues(string(d.Channel)).Set(1)
			d.sleep(ctx, min(until.Sub(d.Clock.Now()), time.Second))
			continue
		}
		d.Metrics.ChannelPaused.WithLabelValues(string(d.Channel)).Set(0)

		if time.Since(lastReclaim) >= time.Second {
			if err := d.reclaim(ctx); err != nil && ctx.Err() == nil {
				d.Log.Warn("reclaim failed", "err", err)
			}
			lastReclaim = time.Now()
		}

		// Take slots first so we never claim more than we can start: a claimed row's lease is ticking.
		n := d.acquireSlots(ctx, sem)
		if n == 0 {
			break
		}
		rows, err := d.queue.Claim(ctx, n)
		for range n - len(rows) {
			<-sem
		}
		if err != nil {
			if ctx.Err() == nil {
				d.Log.Warn("claim failed, backing off", "err", err, "retry_in", dbBackoff)
			}
			d.sleep(ctx, dbBackoff)
			dbBackoff = min(dbBackoff*2, 5*time.Second)
			continue
		}
		dbBackoff = 100 * time.Millisecond
		for _, c := range rows {
			if c.Skipped {
				<-sem // no send for this row, give the slot back
				d.Metrics.Messages.WithLabelValues(string(d.Channel), StatusSkippedNC).Inc()
				d.Log.Info("skipped: consent revoked", "campaign_id", c.CampaignID, "message_id", c.ID)
				continue
			}
			inflight.Go(func() {
				defer func() { <-sem }()
				d.handle(ctx, bg, c)
			})
		}
		if len(rows) == 0 {
			d.sleep(ctx, d.PollInterval)
		}
	}
	return nil
}

// acquireSlots reserves one in-flight slot (blocking) plus any other free ones, and returns how many it got;
// 0 means ctx ended.
func (d *Dispatcher) acquireSlots(ctx context.Context, sem chan struct{}) int {
	for {
		select {
		case sem <- struct{}{}:
			n := 1
			for n < cap(sem) {
				select {
				case sem <- struct{}{}:
					n++
				default:
					return n
				}
			}
			return n
		case <-ctx.Done():
			return 0
		case <-d.Clock.After(time.Second): // keep the readiness beat fresh while every slot is busy
			d.touch()
		}
	}
}

// drain waits for in-flight work, bounded by DrainTimeout.
func (d *Dispatcher) drain(wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d.DrainTimeout):
		d.Log.Warn("drain timeout: unfinished sends keep their lease and will be reclaimed")
	}
}

func (d *Dispatcher) reclaim(ctx context.Context) error {
	requeued, failed, err := d.queue.ReclaimExpired(ctx, d.Policy.MaxAttempts)
	if requeued+failed > 0 {
		d.Log.Warn("reclaimed expired leases", "requeued", requeued, "failed", failed)
		d.Metrics.Messages.WithLabelValues(string(d.Channel), StatusFailed).Add(float64(failed))
	}
	return err
}

// handle sends one claimed message and records the outcome. runCtx ends at shutdown (pre-send waits abort
// and release the lease); bg does not (a started send is allowed to finish).
func (d *Dispatcher) handle(runCtx, bg context.Context, c Claimed) {
	ch := string(d.Channel)
	log := d.Log.With("campaign_id", c.CampaignID, "message_id", c.ID, "attempt", c.Attempts)

	tmpl, err := d.template(bg, c)
	if errors.Is(err, pgx.ErrNoRows) {
		d.finish(bg, log, c, Outcome{Status: StatusFailed, AttemptDelta: 0, LastError: "content_missing", Class: ClassPermanent}, StatusFailed)
		return
	}
	if err != nil { // a database hiccup is not the message's fault
		log.Warn("template lookup failed, releasing", "err", err)
		d.release(bg, log, c, time.Second)
		return
	}

	msg, tooLong := RenderMessage(d.Channel, tmpl, c.Recipient)
	if tooLong {
		// Validation should have made this impossible (R-CNT-1); it is a permanent failure and an alert.
		d.Metrics.ContentTooLong.WithLabelValues(ch).Inc()
		log.Error("rendered sms exceeds the limit; failing permanently")
		d.finish(bg, log, c, Outcome{Status: StatusFailed, LastError: "content_too_long", Class: ClassPermanent}, StatusFailed)
		return
	}

	// Every send, retries included, passes the same limiter (R-SND-2).
	waitStart := time.Now()
	if err := d.Limiter.Wait(runCtx); err != nil {
		d.release(bg, log, c, 0) // shutting down: hand the unsent message back
		return
	}
	d.Metrics.LimiterWait.WithLabelValues(ch).Observe(time.Since(waitStart).Seconds())
	if until := d.PausedUntil(); !until.IsZero() {
		// Another send was throttled while we waited: do not add to the pressure, come back after the pause.
		d.release(bg, log, c, until.Sub(d.Clock.Now()))
		return
	}

	res, sendErr := d.Client.Send(bg, SendRequest{
		Channel: d.Channel, IdempotencyKey: c.ID, To: msg.To, Text: msg.Text, Subject: msg.Subject, HTML: msg.HTML,
		CampaignID: c.CampaignID, UserID: c.UserID, MessageID: c.ID,
	})
	d.Metrics.SendLatency.WithLabelValues(ch).Observe(res.Latency.Seconds())
	d.Metrics.ProviderReplies.WithLabelValues(ch, ResponseCode(res.Status, sendErr)).Inc()
	d.afterSend(bg, log, c, res, sendErr)
}

// afterSend applies the retry policy (KICKOFF §7.3) to a provider answer.
func (d *Dispatcher) afterSend(ctx context.Context, log *slog.Logger, c Claimed, res SendResult, sendErr error) {
	ch := string(d.Channel)
	dec := d.Policy.Classify(res.Status, res.Header, sendErr)
	var status *int
	if res.Status != 0 {
		status = &res.Status
	}
	out := Outcome{HTTPStatus: status, Class: dec.Class, Latency: res.Latency, LastError: errDescription(res.Status, sendErr)}

	switch dec.Class {
	case ClassSuccess:
		out.Status, out.LastError, out.ProviderMessageID = StatusSent, "", res.ProviderMessageID
		d.finish(ctx, log, c, out, StatusSent)

	case ClassPermanent:
		out.Status = StatusFailed
		d.finish(ctx, log, c, out, StatusFailed)

	case ClassConfig:
		// Credentials/permissions problem: nothing will work until a human fixes it, so stop hammering the
		// provider. The message stays queued and does not use up an attempt.
		d.pause(d.ConfigPause)
		log.Error("provider rejected our credentials; channel paused", "http_status", res.Status, "pause", d.ConfigPause)
		out.Status, out.AttemptDelta, out.Delay = StatusRetry, -1, d.ConfigPause
		d.finish(ctx, log, c, out, StatusRetry)

	case ClassThrottled:
		throttles := c.Throttled + 1
		d.pause(dec.RetryAfter)
		out.ThrottleDelta, out.AttemptDelta = 1, -1 // a throttled try never counts as an attempt
		if d.Policy.Exhausted(dec, c.Attempts, throttles) {
			out.Status, out.LastError = StatusFailed, "too_many_throttles"
			d.finish(ctx, log, c, out, StatusFailed)
			return
		}
		d.Metrics.Retries.WithLabelValues(ch, string(ClassThrottled)).Inc()
		out.Status, out.Delay = StatusRetry, dec.RetryAfter
		d.finish(ctx, log, c, out, StatusRetry)

	default: // transient, ambiguous
		if d.Policy.Exhausted(dec, c.Attempts, c.Throttled) {
			out.Status = StatusFailed
			out.LastError = fmt.Sprintf("%s (gave up after %d attempts)", out.LastError, c.Attempts)
			d.finish(ctx, log, c, out, StatusFailed)
			return
		}
		d.Metrics.Retries.WithLabelValues(ch, string(dec.Class)).Inc()
		out.Status, out.Delay = StatusRetry, d.Policy.Backoff(c.Attempts, d.Rand)
		d.finish(ctx, log, c, out, StatusRetry)
	}
}

// pause stops claiming and sending on the channel for dur (never shortening an existing pause).
func (d *Dispatcher) pause(dur time.Duration) {
	until := d.Clock.Now().Add(dur).UnixNano()
	for {
		cur := d.paused.Load()
		if cur >= until || d.paused.CompareAndSwap(cur, until) {
			break
		}
	}
	d.Metrics.ChannelPaused.WithLabelValues(string(d.Channel)).Set(1)
}

// finish writes an outcome, retrying briefly through a database outage: after a successful send the result
// must not be lost lightly. If it still fails, the lease expires and the idempotent retry fixes it.
func (d *Dispatcher) finish(ctx context.Context, log *slog.Logger, c Claimed, o Outcome, metricStatus string) {
	delay := 100 * time.Millisecond
	deadline := time.Now().Add(d.Lease / 2)
	for {
		fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		changed, err := d.queue.Finish(fctx, c, o)
		cancel()
		if err == nil {
			if !changed {
				log.Warn("outcome dropped: the lease was lost to another dispatcher", "status", o.Status)
				return
			}
			d.Metrics.Messages.WithLabelValues(string(d.Channel), metricStatus).Inc()
			if o.Status == StatusFailed {
				log.Warn("message failed", "error", o.LastError, "class", string(o.Class))
			} else {
				log.Debug("message outcome", "status", o.Status, "class", string(o.Class))
			}
			return
		}
		if time.Now().After(deadline) {
			log.Error("could not record outcome; the lease will expire and the send will be retried idempotently", "err", err)
			return
		}
		log.Warn("recording outcome failed, retrying", "err", err)
		<-d.Clock.After(delay)
		delay = min(delay*2, 2*time.Second)
	}
}

func (d *Dispatcher) release(ctx context.Context, log *slog.Logger, c Claimed, delay time.Duration) {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := d.queue.Release(rctx, c, max(delay, 0)); err != nil {
		log.Warn("release failed; the lease will expire", "err", err)
	}
}

func (d *Dispatcher) template(ctx context.Context, c Claimed) (Template, error) {
	key := c.CampaignID + "/" + strconv.Itoa(c.ContentVersion)
	if v, ok := d.tmpl.Load(key); ok {
		return v.(Template), nil
	}
	t, err := d.queue.Template(ctx, c.CampaignID, c.ContentVersion)
	if err != nil {
		return Template{}, err
	}
	d.tmpl.Store(key, t)
	return t, nil
}

// heartbeatLoop writes the worker_heartbeats row and samples queue_depth every HeartbeatEvery.
func (d *Dispatcher) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(d.HeartbeatEvery)
	defer t.Stop()
	ch := string(d.Channel)
	for {
		hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		holds := d.HoldsLock()
		if err := d.queue.Heartbeat(hctx, d.WorkerID, holds, d.PausedUntil()); err != nil && ctx.Err() == nil {
			d.Log.Warn("heartbeat failed", "err", err)
		}
		if holds {
			if depth, err := d.queue.Depth(hctx); err == nil {
				for _, s := range []string{StatusQueued, StatusRetry, StatusSending} {
					d.Metrics.QueueDepth.WithLabelValues(ch, s).Set(float64(depth[s]))
				}
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

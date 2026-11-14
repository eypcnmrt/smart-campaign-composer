// Package worker is the dispatcher: it claims due deliveries from the queue, paces them to the
// per-channel rate limit, sends them to the provider and records the outcome.
//
// Shape (per channel, one dispatcher goroutine):
//
//	take the channel's advisory lock (another worker holding it: wait and retry)
//	  -> circuit breaker allows? -> wait for a rate-limit token -> claim one due delivery -> hand it to a send goroutine
//
// The lock keeps the per-channel cap when more than one worker runs: only the lock holder sends on a channel. The
// circuit breaker (breaker.go) stops the claiming while the provider looks unreachable and gives the attempts of the
// requests that run into the outage back, so an outage does not turn deliveries into failures. The lock holder also
// reports the circuit state and a heartbeat to worker_channel_status.
//
// Claiming after the token, one row at a time, means nothing sits claimed while it waits for its
// turn, and the consent flag read by the claim is the one in force a moment before the request is made.
// Sends run concurrently (bounded by MaxInFlight) so a slow provider reply does not stall the pacing.
//
// A single maintenance goroutine takes back expired leases and completes finished campaigns.
//
// Shutdown: when the context ends, the dispatchers stop claiming, the sends already started finish
// (each bounded by RequestTimeout) and record their result, then Run returns.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"smartcampaign/internal/queue"
	"smartcampaign/internal/ratelimit"
	"smartcampaign/internal/retry"
)

// Config tunes the worker. Zero fields take the defaults of withDefaults.
type Config struct {
	// Channels to serve; default sms and email.
	Channels []string
	// RatePerSecond is the cap per channel (default 100), Burst the token-bucket burst (default 1) and
	// RateMargin the safety margin of the sliding-window guard (default ratelimit.DefaultMargin).
	RatePerSecond float64
	Burst         int
	RateMargin    time.Duration
	// MaxInFlight bounds the concurrent sends per channel (default 256).
	MaxInFlight int
	// RequestTimeout bounds one provider call (default 5s).
	RequestTimeout time.Duration
	// Lease is how long a claimed delivery belongs to this worker (default RequestTimeout + 30s). It
	// must exceed RequestTimeout, or a slow request would lose its lease while still running.
	Lease time.Duration
	// PollInterval is the wait after finding the queue empty (default 200ms); MaintenanceInterval is the
	// period of lease recovery and campaign completion (default 1s).
	PollInterval        time.Duration
	MaintenanceInterval time.Duration
	// LockRetryInterval is how often a worker that does not hold a channel's lock (another worker serves the
	// channel) tries to take it (default 5s).
	LockRetryInterval time.Duration
	// HeartbeatInterval is how often the worker that serves a channel writes its circuit state to
	// worker_channel_status (default 2s); it also writes at once whenever the state changes.
	HeartbeatInterval time.Duration
	Retry             retry.Policy
	// Breaker tunes the circuit breaker each channel has (see breaker).
	Breaker BreakerConfig
	// Observer is told what the worker does, for metrics; the worker does not depend on any metrics library. Nil means
	// nobody is told.
	Observer Observer
	// Jitter returns a number in [0, 1) for the backoff; default math/rand/v2.
	Jitter func() float64
	Logger *slog.Logger
}

func (c Config) withDefaults() Config {
	if len(c.Channels) == 0 {
		c.Channels = []string{"sms", "email"}
	}
	if c.RatePerSecond == 0 {
		c.RatePerSecond = 100
	}
	if c.Burst == 0 {
		c.Burst = 1
	}
	if c.RateMargin == 0 {
		c.RateMargin = ratelimit.DefaultMargin
	}
	if c.MaxInFlight == 0 {
		c.MaxInFlight = 256
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = 5 * time.Second
	}
	if c.Lease == 0 {
		c.Lease = c.RequestTimeout + 30*time.Second
	}
	if c.PollInterval == 0 {
		c.PollInterval = 200 * time.Millisecond
	}
	if c.MaintenanceInterval == 0 {
		c.MaintenanceInterval = time.Second
	}
	if c.LockRetryInterval == 0 {
		c.LockRetryInterval = 5 * time.Second
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 2 * time.Second
	}
	c.Breaker = c.Breaker.withDefaults()
	if c.Observer == nil {
		c.Observer = nopObserver{}
	}
	if c.Retry.MaxAttempts == 0 {
		c.Retry = retry.DefaultPolicy()
	}
	if c.Jitter == nil {
		c.Jitter = rand.Float64
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return c
}

func (c Config) validate() error {
	// The lease must outlive the request AND the write of its result, or a normal result arrives after the
	// lease expired and is dropped, and the message is sent again.
	if c.Lease <= c.RequestTimeout+dbTimeout {
		return fmt.Errorf("lease (%v) must be longer than the request timeout (%v) plus the database timeout (%v)", c.Lease, c.RequestTimeout, dbTimeout)
	}
	for _, ch := range c.Channels {
		if ch != "sms" && ch != "email" {
			return fmt.Errorf("unknown channel %q (want sms or email)", ch)
		}
	}
	if err := c.Retry.Validate(); err != nil {
		return fmt.Errorf("retry policy: %w", err)
	}
	if c.MaxInFlight < 1 {
		return errors.New("max in flight must be at least 1")
	}
	if c.LockRetryInterval < 0 {
		return errors.New("lock retry interval must not be negative")
	}
	if c.HeartbeatInterval < 0 {
		return errors.New("heartbeat interval must not be negative")
	}
	if err := c.Breaker.validate(); err != nil {
		return fmt.Errorf("circuit breaker: %w", err)
	}
	return nil
}

// Counters are totals since the worker was created.
type Counters struct {
	Sent, Retried, Failed, Skipped, LeaseLost int64
	// Refunded counts attempts that ran into a known outage (the circuit was not closed) and were given back to their
	// delivery; they are not in Retried.
	Refunded int64
	// RecordErrors counts outcomes that could not be written to the database even after retrying; the
	// deliveries concerned are picked up again when their lease expires.
	RecordErrors int64
	// HeartbeatErrors counts failed writes of worker_channel_status; while they last, the dashboard sees the worker as
	// not running.
	HeartbeatErrors int64
	// MaintenanceErrors counts failed tasks of the maintenance loop (lease recovery, campaign completion). While they
	// last, expired leases stay "sending" and finished campaigns are not completed.
	MaintenanceErrors int64
}

// Worker is the dispatcher. Create it with New and run it with Run.
type Worker struct {
	cfg    Config
	q      *queue.Queue
	sender Sender
	now    func() time.Time // the breakers' clock; tests of the worker leave it alone

	sent, retried, failed, skipped, leaseLost, refunded, recordErrors, heartbeatErrors, maintenanceErrors atomic.Int64
}

// New returns a worker.
func New(q *queue.Queue, sender Sender, cfg Config) (*Worker, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Worker{cfg: cfg, q: q, sender: sender, now: time.Now}, nil
}

// Counters returns a snapshot of the totals.
func (w *Worker) Counters() Counters {
	return Counters{
		Sent: w.sent.Load(), Retried: w.retried.Load(), Failed: w.failed.Load(),
		Skipped: w.skipped.Load(), LeaseLost: w.leaseLost.Load(), Refunded: w.refunded.Load(),
		RecordErrors: w.recordErrors.Load(), HeartbeatErrors: w.heartbeatErrors.Load(), MaintenanceErrors: w.maintenanceErrors.Load(),
	}
}

// channelState is what one channel keeps for as long as the worker runs, across tenures of its lock: the pacing, the
// send slots and the circuit breaker. Sends still in flight when the lock is lost keep counting, and the pacing does
// not restart when it is taken again.
type channelState struct {
	name  string
	lim   *ratelimit.Limiter
	br    *breaker
	slots chan struct{}
}

// dbTimeout bounds every queue call; they are short statements.
const dbTimeout = 10 * time.Second

// recordTries is how often the outcome of a send is written before the worker leaves it to lease recovery.
const recordTries = 4

// Run serves until ctx ends and every started send has been recorded. It returns an error only for a
// setup problem; failures while running are logged and retried.
func (w *Worker) Run(ctx context.Context) error {
	channels := make([]*channelState, 0, len(w.cfg.Channels))
	for _, name := range w.cfg.Channels {
		l, err := ratelimit.New(w.cfg.RatePerSecond, w.cfg.Burst, w.cfg.RateMargin)
		if err != nil {
			return fmt.Errorf("rate limiter for %s: %w", name, err)
		}
		channels = append(channels, &channelState{
			name: name, lim: l, br: newBreaker(w.cfg.Breaker, w.now), slots: make(chan struct{}, w.cfg.MaxInFlight),
		})
	}

	var sends sync.WaitGroup // started sends: waited for before Run returns
	var loops sync.WaitGroup // dispatchers and the maintenance loop
	loops.Go(func() { w.maintain(ctx) })
	for _, ch := range channels {
		loops.Go(func() { w.dispatch(ctx, ch, &sends) })
	}
	loops.Wait() // no new sends can start after this
	sends.Wait() // let the ones in flight finish and record their outcome
	// The maintenance loop stopped before the last sends were recorded; one more pass completes a campaign
	// whose final deliveries were in flight at shutdown.
	w.maintainOnce(ctx)
	return nil
}

// dispatch is the loop of one channel. Only the worker that holds the channel's advisory lock sends on it, so the
// per-channel cap holds with several workers; the others wait and try again every LockRetryInterval.
func (w *Worker) dispatch(ctx context.Context, ch *channelState, sends *sync.WaitGroup) {
	log := w.cfg.Logger.With("channel", ch.name)
	waiting := false
	for ctx.Err() == nil {
		lockCtx, cancel := context.WithTimeout(ctx, dbTimeout)
		lock, err := w.q.LockChannel(lockCtx, ch.name)
		cancel()
		switch {
		case errors.Is(err, queue.ErrChannelLocked):
			if !waiting { // say it once per wait, not every retry
				log.Info("another worker serves this channel, waiting for its lock")
				waiting = true
			}
			w.pause(ctx, w.cfg.LockRetryInterval)
			continue
		case err != nil:
			if ctx.Err() == nil {
				log.Error("taking the channel lock failed", "err", err)
			}
			w.pause(ctx, w.cfg.LockRetryInterval)
			continue
		}
		waiting = false

		log.Info("serving the channel")
		w.cfg.Observer.LockHeld(ch.name, true)
		// The heartbeat belongs to the tenure of the lock: only the worker that serves the channel reports on it.
		hbCtx, stopHeartbeat := context.WithCancel(ctx)
		var heartbeat sync.WaitGroup
		heartbeat.Go(func() { w.heartbeat(hbCtx, ch) })
		serveErr := w.serve(ctx, ch, lock, sends)
		stopHeartbeat()
		heartbeat.Wait()
		w.cfg.Observer.LockHeld(ch.name, false)
		if err := lock.Release(ctx); err != nil {
			log.Error("releasing the channel lock failed", "err", err)
		}
		if serveErr != nil {
			// The connection that holds the lock may be gone, and the lock with it. Take it again from scratch.
			log.Error("serving stopped, the channel lock is given up and taken again", "err", serveErr)
			w.pause(ctx, time.Second)
		}
	}
}

// serve claims and sends on one channel while the lock is held and ctx lives. It returns nil when ctx ended, and the
// error of the first failed claim otherwise.
func (w *Worker) serve(ctx context.Context, ch *channelState, lock *queue.ChannelLock, sends *sync.WaitGroup) error {
	log := w.cfg.Logger.With("channel", ch.name)
	for ctx.Err() == nil {
		select {
		case ch.slots <- struct{}{}: // a free send slot
		case <-ctx.Done():
			return nil
		}
		release := func() { <-ch.slots }

		// An open circuit means the provider looks unreachable: claim nothing, so no delivery spends an attempt on it.
		// After the cooldown exactly one request (the probe) is let through.
		permit, allowed := ch.br.Allow()
		if !allowed {
			release()
			w.pause(ctx, w.cfg.PollInterval)
			continue
		}

		if err := ch.lim.Wait(ctx); err != nil {
			ch.br.Cancel(permit)
			release()
			return nil // only the context ends a wait
		}

		// The claim is not tied to ctx: if it commits we must handle the delivery, not strand it
		// until its lease expires.
		claimCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
		d, ok, err := lock.Claim(claimCtx, w.cfg.Lease)
		cancel()
		switch {
		case err != nil && !ok:
			ch.br.Cancel(permit)
			release()
			return err
		case err != nil:
			// Claimed but unusable (corrupt campaign content). Waiting for the lease to expire would repeat this
			// for every attempt and leave a misleading "lease expired" in the row, so fail it now with the cause.
			ch.br.Cancel(permit) // no request is made
			log.Error("claimed delivery is unusable", "delivery", d.ID, "err", err)
			sends.Go(func() {
				defer release()
				w.record(context.WithoutCancel(ctx), log.With("delivery", d.ID), d,
					queue.Outcome{Status: queue.StatusFailed, Reason: "unusable campaign content: " + err.Error()}, &w.failed, "failed")
			})
			continue
		case !ok:
			ch.br.Cancel(permit)
			release()
			w.pause(ctx, w.cfg.PollInterval)
			continue
		}

		sends.Go(func() {
			defer release()
			w.process(context.WithoutCancel(ctx), ch.br, permit, d)
		})
	}
	return nil
}

// heartbeat reports the channel's circuit to worker_channel_status every HeartbeatInterval and whenever it changes,
// until ctx ends. A failed write is logged and counted and does not stop the sending: if the writes keep failing the row
// gets old, which the dashboard shows as a worker that is not running.
func (w *Worker) heartbeat(ctx context.Context, ch *channelState) {
	log := w.cfg.Logger.With("channel", ch.name)
	t := time.NewTicker(w.cfg.HeartbeatInterval)
	defer t.Stop()
	failing := false
	for {
		state, lastError := ch.br.Snapshot()
		w.cfg.Observer.Circuit(ch.name, state.String())
		writeCtx, cancel := context.WithTimeout(ctx, dbTimeout)
		err := w.q.SetChannelStatus(writeCtx, ch.name, state.String(), lastError)
		cancel()
		switch {
		case err != nil && ctx.Err() == nil:
			w.heartbeatErrors.Add(1)
			w.cfg.Observer.HeartbeatError(ch.name)
			if !failing { // once per streak, not every tick
				log.Error("writing the heartbeat failed", "err", err)
				failing = true
			}
		case err == nil:
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-ch.br.Changes():
		}
	}
}

// process sends one claimed delivery and records the outcome. The circuit breaker sees the raw result first: a
// failure that comes back while the circuit is not closed is the outage it already knows about, so the attempt is
// given back and the delivery returns to the queue as if it had never been tried.
func (w *Worker) process(ctx context.Context, br *breaker, p permit, d queue.Delivery) {
	log := w.cfg.Logger.With("delivery", d.ID, "campaign", d.CampaignID, "channel", d.Channel, "attempt", d.Attempt)

	if !d.Consent {
		br.Cancel(p) // no request is made, so there is nothing to tell the breaker
		w.record(ctx, log, d, queue.Outcome{Status: queue.StatusSkipped, Reason: "consent withdrawn before sending"}, &w.skipped, "skipped")
		return
	}

	req := buildRequest(d)
	sendCtx, cancel := context.WithTimeout(ctx, w.cfg.RequestTimeout)
	res := w.sender.Send(sendCtx, req)
	cancel()

	if br.Record(p, classify(res), retry.Describe(res)) {
		reason := retry.Describe(res)
		log.Info("the provider is unreachable, the attempt is not counted", "reason", reason, "probe", p.probe)
		w.record(ctx, log, d, queue.Outcome{Status: queue.StatusQueued, Reason: reason, Refund: true}, &w.refunded, "refunded")
		return
	}

	decision := w.cfg.Retry.Decide(d.Attempt, res, w.cfg.Jitter())
	switch decision.Action {
	case retry.Done:
		w.record(ctx, log, d, queue.Outcome{Status: queue.StatusSent}, &w.sent, "sent")
	case retry.Retry:
		log.Info("will retry", "reason", decision.Reason, "delay", decision.Delay)
		w.record(ctx, log, d, queue.Outcome{Status: queue.StatusQueued, Delay: decision.Delay, Reason: decision.Reason}, &w.retried, "retried")
	default:
		log.Warn("delivery failed", "reason", decision.Reason)
		w.record(ctx, log, d, queue.Outcome{Status: queue.StatusFailed, Reason: decision.Reason}, &w.failed, "failed")
	}
}

// record writes the outcome. A lost lease is expected now and then and harmless (the provider
// de-duplicates); any other error leaves the delivery in sending, and its lease expiry will requeue it.
func (w *Worker) record(ctx context.Context, log *slog.Logger, d queue.Delivery, o queue.Outcome, counter *atomic.Int64, result string) {
	var err error
	for try := 1; try <= recordTries; try++ {
		attemptCtx, cancel := context.WithTimeout(ctx, dbTimeout)
		err = w.q.Finish(attemptCtx, d, o)
		cancel()
		if err == nil || errors.Is(err, queue.ErrLeaseLost) {
			break
		}
		// A database blip must not cost the delivery an attempt: the provider may already have it.
		log.Warn("recording the outcome failed, trying again", "outcome", o.Status, "try", try, "err", err)
		w.pause(ctx, time.Duration(try)*500*time.Millisecond)
	}
	switch {
	case err == nil:
		counter.Add(1)
		w.cfg.Observer.Attempt(d.Channel, result)
	case errors.Is(err, queue.ErrLeaseLost):
		w.leaseLost.Add(1)
		w.cfg.Observer.Attempt(d.Channel, "lease_lost")
		log.Warn("result dropped, the lease was lost", "outcome", o.Status)
	default:
		w.recordErrors.Add(1)
		w.cfg.Observer.Attempt(d.Channel, "record_error")
		log.Error("recording the outcome failed; the lease will expire and the delivery will be retried", "outcome", o.Status, "err", err)
	}
}

// buildRequest renders the frozen template for this recipient.
func buildRequest(d queue.Delivery) SendRequest {
	c := d.Content.Render(d.Vars)
	req := SendRequest{Channel: d.Channel, Key: d.Key(), To: d.Recipient}
	if d.Channel == "sms" {
		req.Body = c.SMS
	} else {
		req.Subject, req.Body = c.EmailSubject, c.EmailBody
	}
	return req
}

// maintain takes back expired leases and completes finished campaigns on a timer.
func (w *Worker) maintain(ctx context.Context) {
	t := time.NewTicker(w.cfg.MaintenanceInterval)
	defer t.Stop()
	for {
		w.maintainOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Worker) maintainOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
	defer cancel()
	log := w.cfg.Logger
	rec, err := w.q.RecoverLeases(ctx, w.cfg.Retry.MaxTotalAttempts())
	if err != nil {
		w.maintenanceErrors.Add(1)
		w.cfg.Observer.MaintenanceError(TaskRecoverLeases)
		log.Error("recovering leases failed", "err", err)
	} else if rec.Requeued+rec.Failed > 0 {
		log.Warn("took back expired leases", "requeued", rec.Requeued, "failed", rec.Failed)
	}
	if n, err := w.q.CompleteFinished(ctx); err != nil {
		w.maintenanceErrors.Add(1)
		w.cfg.Observer.MaintenanceError(TaskCompleteCampaigns)
		log.Error("completing campaigns failed", "err", err)
	} else if n > 0 {
		log.Info("campaigns completed", "count", n)
	}
}

// pause waits for d or until ctx ends.
func (w *Worker) pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

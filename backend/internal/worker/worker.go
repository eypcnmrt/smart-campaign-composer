// Package worker is the dispatcher: it claims due deliveries from the queue, paces them to the
// per-channel rate limit, sends them to the provider and records the outcome.
//
// Shape (per channel, one dispatcher goroutine):
//
//	wait for a rate-limit token -> claim one due delivery -> hand it to a send goroutine
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
	Retry               retry.Policy
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
	if c.Lease <= c.RequestTimeout {
		return fmt.Errorf("lease (%v) must be longer than the request timeout (%v)", c.Lease, c.RequestTimeout)
	}
	if c.MaxInFlight < 1 {
		return errors.New("max in flight must be at least 1")
	}
	return nil
}

// Counters are totals since the worker was created.
type Counters struct {
	Sent, Retried, Failed, Skipped, LeaseLost int64
}

// Worker is the dispatcher. Create it with New and run it with Run.
type Worker struct {
	cfg    Config
	q      *queue.Queue
	sender Sender

	sent, retried, failed, skipped, leaseLost atomic.Int64
}

// New returns a worker.
func New(q *queue.Queue, sender Sender, cfg Config) (*Worker, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Worker{cfg: cfg, q: q, sender: sender}, nil
}

// Counters returns a snapshot of the totals.
func (w *Worker) Counters() Counters {
	return Counters{
		Sent: w.sent.Load(), Retried: w.retried.Load(), Failed: w.failed.Load(),
		Skipped: w.skipped.Load(), LeaseLost: w.leaseLost.Load(),
	}
}

// dbTimeout bounds every queue call; they are short statements.
const dbTimeout = 10 * time.Second

// Run serves until ctx ends and every started send has been recorded. It returns an error only for a
// setup problem; failures while running are logged and retried.
func (w *Worker) Run(ctx context.Context) error {
	limiters := make(map[string]*ratelimit.Limiter, len(w.cfg.Channels))
	for _, ch := range w.cfg.Channels {
		l, err := ratelimit.New(w.cfg.RatePerSecond, w.cfg.Burst, w.cfg.RateMargin)
		if err != nil {
			return fmt.Errorf("rate limiter for %s: %w", ch, err)
		}
		limiters[ch] = l
	}

	var sends sync.WaitGroup // started sends: waited for before Run returns
	var loops sync.WaitGroup // dispatchers and the maintenance loop
	loops.Go(func() { w.maintain(ctx) })
	for _, ch := range w.cfg.Channels {
		loops.Go(func() { w.dispatch(ctx, ch, limiters[ch], &sends) })
	}
	loops.Wait() // no new sends can start after this
	sends.Wait() // let the ones in flight finish and record their outcome
	return nil
}

// dispatch is the loop of one channel.
func (w *Worker) dispatch(ctx context.Context, channel string, lim *ratelimit.Limiter, sends *sync.WaitGroup) {
	log := w.cfg.Logger.With("channel", channel)
	slots := make(chan struct{}, w.cfg.MaxInFlight)
	for ctx.Err() == nil {
		select {
		case slots <- struct{}{}: // a free send slot
		case <-ctx.Done():
			return
		}
		release := func() { <-slots }

		if err := lim.Wait(ctx); err != nil {
			release()
			return // only the context ends a wait
		}

		// The claim is not tied to ctx: if it commits we must handle the delivery, not strand it
		// until its lease expires.
		claimCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
		d, ok, err := w.q.Claim(claimCtx, channel, w.cfg.Lease)
		cancel()
		switch {
		case err != nil && !ok:
			release()
			log.Error("claim failed", "err", err)
			w.pause(ctx, time.Second)
			continue
		case err != nil:
			// Claimed but unusable (corrupt content): the lease expires and recovery handles it.
			release()
			log.Error("claimed delivery is unusable", "delivery", d.ID, "err", err)
			continue
		case !ok:
			release()
			w.pause(ctx, w.cfg.PollInterval)
			continue
		}

		sends.Go(func() {
			defer release()
			w.process(context.WithoutCancel(ctx), d)
		})
	}
}

// process sends one claimed delivery and records the outcome.
func (w *Worker) process(ctx context.Context, d queue.Delivery) {
	log := w.cfg.Logger.With("delivery", d.ID, "campaign", d.CampaignID, "channel", d.Channel, "attempt", d.Attempt)

	if !d.Consent {
		w.record(ctx, log, d, queue.Outcome{Status: queue.StatusSkipped, Reason: "consent withdrawn before sending"}, &w.skipped)
		return
	}

	req := buildRequest(d)
	sendCtx, cancel := context.WithTimeout(ctx, w.cfg.RequestTimeout)
	res := w.sender.Send(sendCtx, req)
	cancel()

	decision := w.cfg.Retry.Decide(d.Attempt, res, w.cfg.Jitter())
	switch decision.Action {
	case retry.Done:
		w.record(ctx, log, d, queue.Outcome{Status: queue.StatusSent}, &w.sent)
	case retry.Retry:
		log.Info("will retry", "reason", decision.Reason, "delay", decision.Delay)
		w.record(ctx, log, d, queue.Outcome{Status: queue.StatusQueued, Delay: decision.Delay, Reason: decision.Reason}, &w.retried)
	default:
		log.Warn("delivery failed", "reason", decision.Reason)
		w.record(ctx, log, d, queue.Outcome{Status: queue.StatusFailed, Reason: decision.Reason}, &w.failed)
	}
}

// record writes the outcome. A lost lease is expected now and then and harmless (the provider
// de-duplicates); any other error leaves the delivery in sending, and its lease expiry will requeue it.
func (w *Worker) record(ctx context.Context, log *slog.Logger, d queue.Delivery, o queue.Outcome, counter *atomic.Int64) {
	ctx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	switch err := w.q.Finish(ctx, d, o); {
	case err == nil:
		counter.Add(1)
	case errors.Is(err, queue.ErrLeaseLost):
		w.leaseLost.Add(1)
		log.Warn("result dropped, the lease was lost", "outcome", o.Status)
	default:
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
	rec, err := w.q.RecoverLeases(ctx, w.cfg.Retry.MaxAttempts)
	if err != nil {
		log.Error("recovering leases failed", "err", err)
	} else if rec.Requeued+rec.Failed > 0 {
		log.Warn("took back expired leases", "requeued", rec.Requeued, "failed", rec.Failed)
	}
	if n, err := w.q.CompleteFinished(ctx); err != nil {
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

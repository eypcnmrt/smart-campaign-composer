package worker

import (
	"errors"
	"sync"
	"time"

	"smartcampaign/internal/queue"
	"smartcampaign/internal/retry"
)

// BreakerConfig tunes the circuit breaker of a channel. Zero fields take the defaults.
type BreakerConfig struct {
	// Threshold is how many requests in a row must end without an answer or with a 5xx before the circuit opens
	// (default 10).
	Threshold int
	// Cooldown is how long an open circuit stays shut before one probe request is allowed (default 10s).
	Cooldown time.Duration
	// Disabled turns the breaker off: every request is allowed and every failure counts against its delivery. For tests
	// that need failures to use up attempts (give-up behaviour); production leaves it on.
	Disabled bool
}

func (c BreakerConfig) withDefaults() BreakerConfig {
	if c.Threshold == 0 {
		c.Threshold = 10
	}
	if c.Cooldown == 0 {
		c.Cooldown = 10 * time.Second
	}
	return c
}

func (c BreakerConfig) validate() error {
	if c.Threshold < 1 {
		return errors.New("breaker threshold must be at least 1")
	}
	if c.Cooldown <= 0 {
		return errors.New("breaker cooldown must be positive")
	}
	return nil
}

// circuitState is the state of a breaker.
type circuitState int

const (
	// circuitClosed: the provider answers; requests flow.
	circuitClosed circuitState = iota
	// circuitOpen: the provider looks unreachable; nothing is claimed until the cooldown is over.
	circuitOpen
	// circuitHalfOpen: the cooldown is over and exactly one probe request decides whether the circuit closes again.
	circuitHalfOpen
)

// String is the value stored in worker_channel_status.circuit.
func (s circuitState) String() string {
	switch s {
	case circuitOpen:
		return queue.CircuitOpen
	case circuitHalfOpen:
		return queue.CircuitHalfOpen
	}
	return queue.CircuitClosed
}

// sendOutcome is what a request tells the breaker about the provider.
type sendOutcome int

const (
	// outcomeSuccess: the provider answered and accepted, or answered with a 4xx. It is reachable.
	outcomeSuccess sendOutcome = iota
	// outcomeFailure: no answer (timeout, connection error) or a 5xx. It may be down.
	outcomeFailure
	// outcomeNeutral: a 429. The provider is reachable but busy; its Retry-After is honoured by the retry policy, so
	// it neither resets nor advances the count of failures in a row.
	outcomeNeutral
)

// classify maps a classified attempt to what it says about the provider.
func classify(res retry.Result) sendOutcome {
	switch res.Kind {
	case retry.Delivered, retry.Permanent:
		return outcomeSuccess
	case retry.Transient:
		return outcomeFailure
	}
	return outcomeNeutral
}

// permit is the right to make one request. Allow hands it out; it must be given back through Record (the request was
// made) or Cancel (it was not).
type permit struct{ probe bool }

// breaker is a circuit breaker for one channel. It protects deliveries from an outage of the provider: after a run of
// failures it opens, the dispatcher stops claiming, and no attempt is used up while it is open, so a provider that is
// away for minutes (or hours) does not push every delivery into failed.
//
//	closed --Threshold failures in a row--> open --Cooldown over--> half-open (one probe)
//	  ^                                       ^                          |
//	  +------------- probe answered ----------+---- probe failed --------+
//
// The breaker is safe for concurrent use. The clock is injected so every transition is a table test.
type breaker struct {
	cfg BreakerConfig
	now func() time.Time

	mu        sync.Mutex
	state     circuitState
	failures  int       // in a row, while closed
	openedAt  time.Time // when the circuit last opened
	probing   bool      // the half-open probe is out
	lastError string    // how the latest failure looked

	// changed gets a signal (without blocking) whenever the state changes, so the heartbeat can report it at once.
	changed chan struct{}
}

func newBreaker(cfg BreakerConfig, now func() time.Time) *breaker {
	return &breaker{cfg: cfg, now: now, changed: make(chan struct{}, 1)}
}

// Changes receives a value after every state change; values that nobody read yet are merged into one.
func (b *breaker) Changes() <-chan struct{} { return b.changed }

func (b *breaker) notify() {
	select {
	case b.changed <- struct{}{}:
	default:
	}
}

// Snapshot returns the state and, while the circuit is not closed, the last error.
func (b *breaker) Snapshot() (circuitState, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == circuitClosed {
		return circuitClosed, ""
	}
	return b.state, b.lastError
}

// Allow says whether a request may be made now. A closed circuit always allows. An open one allows nothing until the
// cooldown is over, then lets exactly one probe through (the permit says so) and stays half-open until the probe is
// recorded.
func (b *breaker) Allow() (permit, bool) {
	if b.cfg.Disabled {
		return permit{}, true
	}
	b.mu.Lock()
	var (
		p       permit
		allowed bool
		changed bool
	)
	switch b.state {
	case circuitClosed:
		allowed = true
	case circuitOpen:
		if b.now().Sub(b.openedAt) >= b.cfg.Cooldown {
			b.state, b.probing, changed = circuitHalfOpen, true, true
			p, allowed = permit{probe: true}, true
		}
	case circuitHalfOpen:
		if !b.probing {
			b.probing = true
			p, allowed = permit{probe: true}, true
		}
	}
	b.mu.Unlock()
	if changed {
		b.notify()
	}
	return p, allowed
}

// Cancel gives a permit back when no request was made with it (nothing was due, the claim failed, the user had
// withdrawn consent). Without it a half-open breaker would wait forever for a probe that never ran.
func (b *breaker) Cancel(p permit) {
	if !p.probe || b.cfg.Disabled {
		return
	}
	b.mu.Lock()
	b.probing = false
	b.mu.Unlock()
}

// Record folds the result of a request into the breaker and returns whether the attempt must be given back to its
// delivery. detail is the short text of a failure.
//
// Only a request made while the circuit was closed counts towards opening it. A failure that comes back after the
// circuit is already open (a request that was in flight) or from the probe is the outage the breaker already knows
// about, so it is refunded: the delivery keeps its attempt. The failure that itself opens the circuit is not refunded;
// that bounds the attempts an outage can burn to Threshold per channel. Results of in-flight requests do not decide
// the half-open state; only the probe does.
func (b *breaker) Record(p permit, o sendOutcome, detail string) (refund bool) {
	if b.cfg.Disabled {
		return false
	}
	b.mu.Lock()
	before := b.state
	changed := false
	switch {
	case p.probe:
		b.probing = false
		if o == outcomeFailure {
			b.state, b.openedAt, b.lastError = circuitOpen, b.now(), detail
		} else { // an answer, even a 429, proves the provider is reachable
			b.state, b.failures, b.lastError = circuitClosed, 0, ""
		}
		changed = b.state != before
	case before == circuitClosed:
		switch o {
		case outcomeSuccess:
			b.failures = 0
		case outcomeFailure:
			b.failures++
			b.lastError = detail
			if b.failures >= b.cfg.Threshold {
				b.state, b.openedAt, changed = circuitOpen, b.now(), true
			}
		}
	}
	refund = o == outcomeFailure && (p.probe || before != circuitClosed)
	b.mu.Unlock()
	if changed {
		b.notify()
	}
	return refund
}

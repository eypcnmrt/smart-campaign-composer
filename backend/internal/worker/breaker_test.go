package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"smartcampaign/internal/retry"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestBreaker(threshold int, cooldown time.Duration) (*breaker, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	return newBreaker(BreakerConfig{Threshold: threshold, Cooldown: cooldown}, clock.now), clock
}

func mustAllow(t *testing.T, b *breaker) permit {
	t.Helper()
	p, ok := b.Allow()
	if !ok {
		t.Fatalf("Allow refused; state %v", stateOf(b))
	}
	return p
}

func stateOf(b *breaker) circuitState {
	s, _ := b.Snapshot()
	return s
}

func TestClassify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		res  retry.Result
		want sendOutcome
	}{
		{"delivered", retry.Result{Kind: retry.Delivered, Status: 200}, outcomeSuccess},
		{"4xx: the provider answered", retry.Result{Kind: retry.Permanent, Status: 400}, outcomeSuccess},
		{"5xx", retry.Result{Kind: retry.Transient, Status: 503}, outcomeFailure},
		{"timeout", retry.Result{Kind: retry.Transient, Err: context.DeadlineExceeded}, outcomeFailure},
		{"connection error", retry.Result{Kind: retry.Transient, Err: errors.New("connection refused")}, outcomeFailure},
		{"429 is neutral", retry.Result{Kind: retry.RateLimited, Status: 429}, outcomeNeutral},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classify(tt.res); got != tt.want {
				t.Fatalf("classify = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBreaker_opensAfterThresholdFailuresInARow(t *testing.T) {
	t.Parallel()
	b, _ := newTestBreaker(5, 10*time.Second)
	var permits []permit
	for range 7 { // seven requests are in flight before the first answer comes back
		permits = append(permits, mustAllow(t, b))
	}
	for i, p := range permits[:4] {
		if refund := b.Record(p, outcomeFailure, "http 503"); refund {
			t.Fatalf("failure %d was refunded while the circuit was closed", i+1)
		}
		if got := stateOf(b); got != circuitClosed {
			t.Fatalf("after %d failures the circuit is %v, want closed", i+1, got)
		}
	}
	// the fifth failure opens the circuit; it counts (bounds the attempts an outage burns to the threshold)
	if refund := b.Record(permits[4], outcomeFailure, "http 503"); refund {
		t.Fatal("the failure that opens the circuit must count against its delivery")
	}
	if got := stateOf(b); got != circuitOpen {
		t.Fatalf("circuit = %v, want open", got)
	}
	// the two that were still in flight run into the outage the breaker already knows about
	for i, p := range permits[5:] {
		if refund := b.Record(p, outcomeFailure, "http 503"); !refund {
			t.Fatalf("in-flight failure %d after the circuit opened was not refunded", i+1)
		}
	}
	if _, ok := b.Allow(); ok {
		t.Fatal("an open circuit must not allow a request")
	}
	if state, last := b.Snapshot(); state != circuitOpen || last != "http 503" {
		t.Fatalf("snapshot = %v, %q, want open and the last error", state, last)
	}
}

func TestBreaker_successResetsAndA429DoesNotCount(t *testing.T) {
	t.Parallel()
	b, _ := newTestBreaker(3, time.Second)
	record := func(o sendOutcome) { t.Helper(); b.Record(mustAllow(t, b), o, "x") }

	record(outcomeFailure)
	record(outcomeFailure)
	record(outcomeSuccess) // an answer resets the run
	record(outcomeFailure)
	record(outcomeFailure)
	if got := stateOf(b); got != circuitClosed {
		t.Fatalf("circuit = %v: failures were not consecutive", got)
	}
	record(outcomeNeutral) // 429: neither resets nor adds
	if got := stateOf(b); got != circuitClosed {
		t.Fatalf("circuit = %v after a 429", got)
	}
	record(outcomeFailure) // third in a row, the 429 in between did not interrupt the run
	if got := stateOf(b); got != circuitOpen {
		t.Fatalf("circuit = %v, want open: a 429 must not reset the count", got)
	}
}

func TestBreaker_halfOpenLetsExactlyOneProbeThrough(t *testing.T) {
	t.Parallel()
	b, clock := newTestBreaker(1, 10*time.Second)
	b.Record(mustAllow(t, b), outcomeFailure, "timeout")

	clock.advance(9 * time.Second)
	if _, ok := b.Allow(); ok {
		t.Fatal("allowed before the cooldown was over")
	}
	clock.advance(time.Second)
	probe, ok := b.Allow()
	if !ok || !probe.probe {
		t.Fatalf("Allow after the cooldown = %+v, %v, want the probe permit", probe, ok)
	}
	if got := stateOf(b); got != circuitHalfOpen {
		t.Fatalf("circuit = %v, want half-open", got)
	}
	if _, ok := b.Allow(); ok {
		t.Fatal("a second request was allowed while the probe is out")
	}

	// the probe was not used (nothing was due): the slot comes back, or the breaker would wait for it forever
	b.Cancel(probe)
	again, ok := b.Allow()
	if !ok || !again.probe {
		t.Fatalf("Allow after Cancel = %+v, %v, want a new probe permit", again, ok)
	}
}

func TestBreaker_probeDecides(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		outcome    sendOutcome
		wantState  circuitState
		wantRefund bool
	}{
		{"answered: the circuit closes", outcomeSuccess, circuitClosed, false},
		{"a 429 is an answer: the circuit closes", outcomeNeutral, circuitClosed, false},
		{"failed: the circuit opens again and the attempt is given back", outcomeFailure, circuitOpen, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, clock := newTestBreaker(1, 10*time.Second)
			b.Record(mustAllow(t, b), outcomeFailure, "http 500")
			clock.advance(10 * time.Second)
			probe := mustAllow(t, b)

			if refund := b.Record(probe, tt.outcome, "http 502"); refund != tt.wantRefund {
				t.Fatalf("refund = %v, want %v", refund, tt.wantRefund)
			}
			state, last := b.Snapshot()
			if state != tt.wantState {
				t.Fatalf("circuit = %v, want %v", state, tt.wantState)
			}
			if tt.wantState == circuitClosed && last != "" {
				t.Fatalf("last error %q is shown for a closed circuit", last)
			}
			if tt.wantState == circuitOpen {
				if last != "http 502" {
					t.Fatalf("last error = %q, want the probe's", last)
				}
				// the cooldown starts over from the failed probe
				clock.advance(9 * time.Second)
				if _, ok := b.Allow(); ok {
					t.Fatal("allowed before the new cooldown was over")
				}
				clock.advance(time.Second)
				if p, ok := b.Allow(); !ok || !p.probe {
					t.Fatal("no probe after the new cooldown")
				}
			} else if _, ok := b.Allow(); !ok {
				t.Fatal("a closed circuit must allow")
			}
		})
	}
}

// A request that was already in flight when the circuit left "closed" does not decide anything, only the probe does.
func TestBreaker_inFlightResultsDoNotDecideTheHalfOpenState(t *testing.T) {
	t.Parallel()
	b, clock := newTestBreaker(1, time.Second)
	straggler := mustAllow(t, b) // started while closed
	b.Record(mustAllow(t, b), outcomeFailure, "x")
	clock.advance(time.Second)
	probe := mustAllow(t, b)

	if refund := b.Record(straggler, outcomeSuccess, ""); refund {
		t.Fatal("a success was refunded")
	}
	if got := stateOf(b); got != circuitHalfOpen {
		t.Fatalf("circuit = %v: a straggler's success must not close the circuit", got)
	}
	if refund := b.Record(straggler, outcomeFailure, "late"); !refund {
		t.Fatal("a straggler's failure during the half-open state must be refunded")
	}
	if got := stateOf(b); got != circuitHalfOpen {
		t.Fatalf("circuit = %v: a straggler's failure must not reopen the circuit", got)
	}
	b.Record(probe, outcomeSuccess, "")
	if got := stateOf(b); got != circuitClosed {
		t.Fatalf("circuit = %v, want closed after the probe", got)
	}
}

func TestBreaker_changesAreSignalled(t *testing.T) {
	t.Parallel()
	b, clock := newTestBreaker(1, time.Second)
	signalled := func() bool {
		select {
		case <-b.Changes():
			return true
		default:
			return false
		}
	}
	if signalled() {
		t.Fatal("signal before any change")
	}
	b.Record(mustAllow(t, b), outcomeFailure, "x") // closed -> open
	if !signalled() {
		t.Fatal("no signal when the circuit opened")
	}
	clock.advance(time.Second)
	probe := mustAllow(t, b) // open -> half-open
	if !signalled() {
		t.Fatal("no signal when the circuit went half-open")
	}
	b.Record(probe, outcomeSuccess, "") // half-open -> closed
	if !signalled() {
		t.Fatal("no signal when the circuit closed")
	}
	b.Record(mustAllow(t, b), outcomeSuccess, "") // no change
	if signalled() {
		t.Fatal("signal without a change")
	}
}

func TestBreaker_disabledNeverGetsInTheWay(t *testing.T) {
	t.Parallel()
	b := newBreaker(BreakerConfig{Threshold: 1, Cooldown: time.Second, Disabled: true}, time.Now)
	for range 20 {
		p, ok := b.Allow()
		if !ok {
			t.Fatal("a disabled breaker refused a request")
		}
		if b.Record(p, outcomeFailure, "x") {
			t.Fatal("a disabled breaker refunded an attempt: every failure must count")
		}
	}
	if got := stateOf(b); got != circuitClosed {
		t.Fatalf("circuit = %v, want closed", got)
	}
}

func TestBreakerConfig(t *testing.T) {
	t.Parallel()
	c := BreakerConfig{}.withDefaults()
	if c.Threshold != 10 || c.Cooldown != 10*time.Second {
		t.Fatalf("defaults = %+v, want 10 failures and a 10s cooldown", c)
	}
	for name, bad := range map[string]BreakerConfig{
		"negative threshold": {Threshold: -1, Cooldown: time.Second},
		"negative cooldown":  {Threshold: 1, Cooldown: -time.Second},
	} {
		if err := bad.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := c.validate(); err != nil {
		t.Errorf("defaults rejected: %v", err)
	}
}

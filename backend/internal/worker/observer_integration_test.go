//go:build integration

package worker_test

import (
	"sync"
	"testing"
	"time"

	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/worker"
)

// recorder is an Observer that remembers what it was told.
type recorder struct {
	mu       sync.Mutex
	attempts map[string]int // "channel/result"
	circuit  map[string]string
	held     map[string]bool
	hbErrors int
}

func newRecorder() *recorder {
	return &recorder{attempts: map[string]int{}, circuit: map[string]string{}, held: map[string]bool{}}
}

func (r *recorder) Attempt(channel, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts[channel+"/"+result]++
}

func (r *recorder) Circuit(channel, state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.circuit[channel] = state
}

func (r *recorder) LockHeld(channel string, held bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held[channel] = held
}

func (r *recorder) HeartbeatError(string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hbErrors++
}

func (r *recorder) snapshot() (attempts map[string]int, circuit map[string]string, held map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	attempts, circuit, held = map[string]int{}, map[string]string{}, map[string]bool{}
	for k, v := range r.attempts {
		attempts[k] = v
	}
	for k, v := range r.circuit {
		circuit[k] = v
	}
	for k, v := range r.held {
		held[k] = v
	}
	return attempts, circuit, held
}

// The observer is how the worker's metrics get their numbers: outcomes per channel, the circuit state, and whether the
// worker serves the channel.
func TestWorker_theObserverSeesOutcomesCircuitAndLock(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	id := e.campaign(t, 30, "sms", "email")
	rec := newRecorder()
	_, stop := e.start(t, worker.Config{Observer: rec, RatePerSecond: 200, HeartbeatInterval: 50 * time.Millisecond})
	e.waitFinished(t, id, 30*time.Second)

	eventually(t, 5*time.Second, "the observer was not told that the worker serves both channels", func() bool {
		_, circuit, held := rec.snapshot()
		return held["sms"] && held["email"] && circuit["sms"] == "closed" && circuit["email"] == "closed"
	})
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	attempts, _, held := rec.snapshot()
	if attempts["sms/sent"] != 30 || attempts["email/sent"] != 30 {
		t.Fatalf("attempts = %v, want 30 sent per channel", attempts)
	}
	if held["sms"] || held["email"] {
		t.Fatalf("held = %v after the worker stopped, want none", held)
	}
	if rec.hbErrors != 0 {
		t.Fatalf("%d heartbeat errors", rec.hbErrors)
	}
}

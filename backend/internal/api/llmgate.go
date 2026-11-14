package api

import (
	"context"
	"errors"
	"sync"
	"time"
)

// errBusy means no generation slot became free in time.
var errBusy = errors.New("too many generations are running")

// busyRetryAfter is the Retry-After of the 503 for a busy gate.
const busyRetryAfter = 5 * time.Second

// gate limits how many generations run at once, so a burst of requests cannot spend credit on the real model faster than
// intended. A request that finds no free slot waits a little, then gives up with errBusy (503 llm_busy).
type gate struct {
	slots chan struct{}
	wait  time.Duration
}

func newGate(n int, wait time.Duration) *gate {
	return &gate{slots: make(chan struct{}, n), wait: wait}
}

// acquire takes a slot and returns the function that gives it back. It returns errBusy after the wait, or the context's
// error if the caller gives up first.
func (g *gate) acquire(ctx context.Context) (release func(), err error) {
	timer := time.NewTimer(g.wait)
	defer timer.Stop()
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, nil
	case <-timer.C:
		return nil, errBusy
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// genLocks makes sure one campaign has at most one generation at a time (a segment or its content), in this process. The
// api runs as one instance (docs: DECISIONS.md); a second instance would need an advisory lock instead.
type genLocks struct {
	mu     sync.Mutex
	active map[int64]struct{}
}

func newGenLocks() *genLocks { return &genLocks{active: map[int64]struct{}{}} }

// tryLock reports whether the campaign had no generation running, and if so marks it as busy until unlock is called.
func (l *genLocks) tryLock(id int64) (unlock func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, busy := l.active[id]; busy {
		return nil, false
	}
	l.active[id] = struct{}{}
	return func() {
		l.mu.Lock()
		delete(l.active, id)
		l.mu.Unlock()
	}, true
}

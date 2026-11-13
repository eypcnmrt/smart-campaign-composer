// Package ratelimit paces the sends of one channel to at most N per second.
//
// Two mechanisms work together:
//
//   - a token bucket from golang.org/x/time/rate (rate N, burst 1 by default) spaces the sends
//     evenly, 1/N apart, so there is no burst at the start of a second;
//   - a sliding-window guard remembers the last N send times and refuses to let the (N+1)th send
//     happen less than window (1 s plus a safety margin) after the oldest of them.
//
// The guard exists because evenly spaced sends are not enough to be sure about "no more than N in
// any one-second window": the goroutine that makes the HTTP call wakes up a few milliseconds late
// now and then, so two sends that were 1/N apart can reach the provider closer together, and N+1
// of them can fall into one second. The margin absorbs that scheduling jitter at the price of a
// slightly lower steady rate (about 4 % below the cap with the default margin).
package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// DefaultMargin is added to the one-second window of the guard.
const DefaultMargin = 40 * time.Millisecond

// Limiter is safe for concurrent use.
type Limiter struct {
	bucket *rate.Limiter
	guard  *slidingWindow
}

// New returns a limiter for perSecond sends per second. burst is the token-bucket burst (1 spaces
// the sends evenly); margin widens the guard's window beyond one second.
func New(perSecond float64, burst int, margin time.Duration) (*Limiter, error) {
	if perSecond < 1 {
		return nil, fmt.Errorf("rate must be at least 1 per second, got %v", perSecond)
	}
	if burst < 1 {
		return nil, fmt.Errorf("burst must be at least 1, got %d", burst)
	}
	if margin < 0 {
		return nil, fmt.Errorf("margin must not be negative")
	}
	return &Limiter{
		bucket: rate.NewLimiter(rate.Limit(perSecond), burst),
		// The guard allows floor(perSecond) sends per window, never more than the cap.
		guard: newSlidingWindow(int(perSecond), time.Second+margin),
	}, nil
}

// Wait blocks until one more send is allowed, or until ctx ends (then it returns the context's error).
func (l *Limiter) Wait(ctx context.Context) error {
	if err := l.bucket.Wait(ctx); err != nil {
		return fmt.Errorf("waiting for a token: %w", err)
	}
	at := l.guard.reserve(time.Now())
	return sleepUntil(ctx, at)
}

func sleepUntil(ctx context.Context, at time.Time) error {
	d := time.Until(at)
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// slidingWindow guarantees that any `limit+1` consecutive reserved times span at least `window`.
type slidingWindow struct {
	mu     sync.Mutex
	window time.Duration
	times  []time.Time // ring of the last `limit` reserved times
	next   int         // index of the oldest entry
	filled int
}

func newSlidingWindow(limit int, window time.Duration) *slidingWindow {
	return &slidingWindow{window: window, times: make([]time.Time, limit)}
}

// reserve returns the earliest time at which one more send is allowed, not before now, and records it.
func (w *slidingWindow) reserve(now time.Time) time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	at := now
	if w.filled == len(w.times) {
		if earliest := w.times[w.next].Add(w.window); earliest.After(at) {
			at = earliest
		}
	} else {
		w.filled++
	}
	w.times[w.next] = at
	w.next = (w.next + 1) % len(w.times)
	return at
}

package ratelimit

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

var base = time.Date(2025, 11, 13, 12, 0, 0, 0, time.UTC)

func ms(n int) time.Time { return base.Add(time.Duration(n) * time.Millisecond) }

func TestSlidingWindow_reserve(t *testing.T) {
	t.Parallel()
	t.Run("the first limit sends are not delayed", func(t *testing.T) {
		t.Parallel()
		w := newSlidingWindow(3, time.Second)
		for i := range 3 {
			if got := w.reserve(ms(i)); !got.Equal(ms(i)) {
				t.Fatalf("send %d reserved %v, want now", i, got)
			}
		}
	})
	t.Run("the next send waits a full window after the oldest", func(t *testing.T) {
		t.Parallel()
		w := newSlidingWindow(3, time.Second)
		w.reserve(ms(0))
		w.reserve(ms(10))
		w.reserve(ms(20))
		if got := w.reserve(ms(30)); !got.Equal(ms(1000)) {
			t.Fatalf("4th send reserved %v, want 1000ms (oldest + window)", got.Sub(base))
		}
		if got := w.reserve(ms(31)); !got.Equal(ms(1010)) {
			t.Fatalf("5th send reserved %v, want 1010ms", got.Sub(base))
		}
	})
	t.Run("a send after a quiet period is not delayed", func(t *testing.T) {
		t.Parallel()
		w := newSlidingWindow(2, time.Second)
		w.reserve(ms(0))
		w.reserve(ms(1))
		if got := w.reserve(ms(5000)); !got.Equal(ms(5000)) {
			t.Fatalf("reserved %v, want now", got.Sub(base))
		}
	})
	t.Run("never more than limit in any window, whatever the arrival pattern", func(t *testing.T) {
		t.Parallel()
		const limit = 10
		w := newSlidingWindow(limit, time.Second)
		var reserved []time.Time
		for i := range 300 {
			reserved = append(reserved, w.reserve(ms(i*3))) // arrivals every 3 ms: far above the cap
		}
		for i := 0; i+limit < len(reserved); i++ {
			if span := reserved[i+limit].Sub(reserved[i]); span < time.Second {
				t.Fatalf("sends %d and %d are only %v apart: %d sends inside one window", i, i+limit, span, limit+1)
			}
		}
	})
}

func TestNew_validates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		rate   float64
		burst  int
		margin time.Duration
	}{
		{"zero rate", 0, 1, 0},
		{"zero burst", 10, 0, 0},
		{"negative margin", 10, 1, -time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(tt.rate, tt.burst, tt.margin); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// Real clock: 150 sends at a cap of 100 per second must take over a second and never put more than
// 100 sends into one window, even with several goroutines waiting at once.
func TestLimiter_Wait_capsTheRate(t *testing.T) {
	t.Parallel()
	l, err := New(100, 1, DefaultMargin)
	if err != nil {
		t.Fatal(err)
	}
	const total = 150
	var mu sync.Mutex
	var stamps []time.Time
	var wg sync.WaitGroup
	started := time.Now()
	for range 6 {
		wg.Go(func() {
			for range total / 6 {
				if err := l.Wait(context.Background()); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				stamps = append(stamps, time.Now())
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(started)

	if len(stamps) != total {
		t.Fatalf("got %d sends, want %d", len(stamps), total)
	}
	if elapsed < 1400*time.Millisecond {
		t.Fatalf("150 sends took %v; at 100 per second they cannot take less than 1.4s", elapsed)
	}
	// stamps are appended under the lock but goroutines may interleave: sort before windowing.
	slices.SortFunc(stamps, func(a, b time.Time) int { return a.Compare(b) })
	best, lo := 0, 0
	for hi := range stamps {
		for stamps[hi].Sub(stamps[lo]) >= time.Second {
			lo++
		}
		best = max(best, hi-lo+1)
	}
	if best > 100 {
		t.Fatalf("%d sends fell into one second, cap is 100", best)
	}
}

func TestLimiter_Wait_stopsOnCancel(t *testing.T) {
	t.Parallel()
	l, err := New(1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Wait(context.Background()); err != nil { // uses the single token
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = l.Wait(ctx)
	if err == nil {
		t.Fatal("Wait returned nil, want the context error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		// x/time/rate reports "would exceed the context deadline" without wrapping the context error.
		t.Logf("error = %v", err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("Wait took %v to give up", time.Since(started))
	}
}

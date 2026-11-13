package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWaitReady_invalidURLFailsAtOnce(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()
	_, err := WaitReady(ctx, "postgres://user:pass@host:notaport/db", Options{})
	if !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("error = %v, want ErrInvalidURL", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("took %v: an unparseable URL cannot get better by waiting", time.Since(started))
	}
	if msg := err.Error(); len(msg) > 0 && (strings.Contains(msg, "pass") || strings.Contains(msg, "notaport")) {
		t.Fatalf("error %q echoes the URL", msg)
	}
}

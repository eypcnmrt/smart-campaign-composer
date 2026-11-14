//go:build integration

package queue_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/queue"
	"smartcampaign/internal/testdb"
)

func TestChannelStatus_roundTripAndAge(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := queue.New(pool)

	if got, err := q.ChannelStatuses(ctx); err != nil || len(got) != 0 {
		t.Fatalf("statuses of a worker that never reported = %v, %v, want none", got, err)
	}
	if err := q.SetChannelStatus(ctx, "sms", queue.CircuitOpen, "http 503"); err != nil {
		t.Fatal(err)
	}
	if err := q.SetChannelStatus(ctx, "email", queue.CircuitClosed, ""); err != nil {
		t.Fatal(err)
	}
	got, err := q.ChannelStatuses(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("statuses = %v, %v", got, err)
	}
	if s := got["sms"]; s.Circuit != queue.CircuitOpen || s.LastError != "http 503" || s.Age < 0 || s.Age > 5*time.Second {
		t.Fatalf("sms status = %+v", s)
	}

	// the age is measured by the database and grows while nobody writes
	time.Sleep(300 * time.Millisecond)
	old, _ := q.ChannelStatuses(ctx)
	if old["email"].Age < 250*time.Millisecond {
		t.Fatalf("age of an unchanged row = %v after 300ms", old["email"].Age)
	}
	// a new report replaces the row (one row per channel) and resets the age
	if err := q.SetChannelStatus(ctx, "sms", queue.CircuitClosed, ""); err != nil {
		t.Fatal(err)
	}
	fresh, _ := q.ChannelStatuses(ctx)
	if len(fresh) != 2 || fresh["sms"].Circuit != queue.CircuitClosed || fresh["sms"].LastError != "" || fresh["sms"].Age >= old["sms"].Age {
		t.Fatalf("after the update: %+v (before: %+v)", fresh["sms"], old["sms"])
	}

	if err := q.SetChannelStatus(ctx, "sms", queue.CircuitOpen, strings.Repeat("x", 2000)); err != nil {
		t.Fatal(err)
	}
	long, _ := q.ChannelStatuses(ctx)
	if n := len([]rune(long["sms"].LastError)); n != 500 {
		t.Fatalf("stored error is %d characters, want it cut to 500", n)
	}
	if err := q.SetChannelStatus(ctx, "fax", queue.CircuitClosed, ""); err == nil {
		t.Fatal("unknown channel accepted")
	}
	if err := q.SetChannelStatus(ctx, "sms", "ajar", ""); err == nil {
		t.Fatal("unknown circuit state accepted")
	}
}

// A refunded attempt goes back to its delivery: the retry is attempt 1 again, not attempt 2.
func TestFinish_refundGivesTheAttemptBack(t *testing.T) {
	ctx := context.Background()
	pool, q, _ := enqueued(t, 1, "sms")
	attempts := func(id int64) int {
		return count(t, pool, `SELECT attempts FROM deliveries WHERE id = $1`, id)
	}

	d, ok, err := q.Claim(ctx, "sms", time.Minute)
	if err != nil || !ok || d.Attempt != 1 {
		t.Fatalf("claim = %+v, %v, %v", d, ok, err)
	}
	if err := q.Finish(ctx, d, queue.Outcome{Status: queue.StatusQueued, Reason: "http 503", Refund: true}); err != nil {
		t.Fatal(err)
	}
	if got := attempts(d.ID); got != 0 {
		t.Fatalf("attempts after a refund = %d, want 0", got)
	}
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE id = $1 AND status = 'queued' AND last_error = 'http 503' AND lease_until IS NULL`, d.ID); n != 1 {
		t.Fatal("a refunded delivery must be queued again and keep the reason")
	}

	d2, ok, err := q.Claim(ctx, "sms", time.Minute)
	if err != nil || !ok || d2.Attempt != 1 || d2.Key() != d.Key() {
		t.Fatalf("claim after the refund = %+v, %v, %v, want attempt 1 and the same key", d2, ok, err)
	}
	// a normal retry keeps the attempt
	if err := q.Finish(ctx, d2, queue.Outcome{Status: queue.StatusQueued, Reason: "http 500"}); err != nil {
		t.Fatal(err)
	}
	if got := attempts(d.ID); got != 1 {
		t.Fatalf("attempts after a normal retry = %d, want 1", got)
	}
	// the fence still holds: a result of an earlier claim must not be written once the row has moved on
	d3, ok, err := q.Claim(ctx, "sms", time.Minute) // attempt 2
	if err != nil || !ok || d3.Attempt != 2 {
		t.Fatalf("claim = %+v, %v, %v, want attempt 2", d3, ok, err)
	}
	if err := q.Finish(ctx, d2, queue.Outcome{Status: queue.StatusQueued, Refund: true}); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("stale result: err = %v, want ErrLeaseLost", err)
	}
	if got := attempts(d.ID); got != 2 {
		t.Fatalf("a stale refund changed the attempts to %d, want 2", got)
	}
}

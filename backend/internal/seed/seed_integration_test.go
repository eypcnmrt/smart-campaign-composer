//go:build integration

package seed_test

import (
	"context"
	"testing"
	"time"

	"smartcampaign/internal/seed"
	"smartcampaign/internal/testdb"
)

func TestRun_isIdempotent(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	opts := seed.Options{Users: 400, Now: time.Date(2025, 11, 13, 12, 0, 0, 0, time.UTC)}

	first, err := seed.Run(ctx, pool, opts)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.UsersInserted != 400 || first.EventsInserted == 0 {
		t.Fatalf("first run = %+v, want 400 users and some events", first)
	}

	second, err := seed.Run(ctx, pool, opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.UsersInserted != 0 || second.EventsInserted != 0 {
		t.Fatalf("second run = %+v, want nothing inserted", second)
	}

	var users, events int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM events)`).Scan(&users, &events); err != nil {
		t.Fatal(err)
	}
	if users != 400 || events != first.EventsInserted {
		t.Fatalf("database has %d users and %d events, want 400 and %d", users, events, first.EventsInserted)
	}
}

func TestRun_growsWithoutDuplicates(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2025, 11, 13, 12, 0, 0, 0, time.UTC)
	if _, err := seed.Run(ctx, pool, seed.Options{Users: 100, Now: now}); err != nil {
		t.Fatal(err)
	}
	res, err := seed.Run(ctx, pool, seed.Options{Users: 150, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if res.UsersInserted != 50 {
		t.Fatalf("inserted %d users, want only the 50 new ones", res.UsersInserted)
	}
}

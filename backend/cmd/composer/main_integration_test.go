//go:build integration

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"smartcampaign/internal/testdb"
)

// The migrate and seed commands against a real database, twice: the second run adds nothing.
func TestRun_migrateAndSeed(t *testing.T) {
	_, url := testdb.NewWithURL(t) // a migrated, empty database of its own
	t.Setenv(envDatabaseURL, url)
	t.Setenv(envSeedUsers, "50")

	for i, want := range []string{"seed: 50 users", "seed: 0 users"} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), []string{"migrate"}, &stdout, &stderr); code != 0 {
			t.Fatalf("migrate code %d: %s", code, stderr.String())
		}
		stdout.Reset()
		if code := run(context.Background(), []string{"seed"}, &stdout, &stderr); code != 0 {
			t.Fatalf("run %d: seed code %d: %s", i, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("run %d: stdout = %q, want %q", i, stdout.String(), want)
		}
	}
}

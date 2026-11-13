// Package testdb gives integration tests their own freshly migrated PostgreSQL database.
//
// It reads TEST_DATABASE_URL (any database on the server; only used to run CREATE DATABASE).
// An unreachable or unset database fails the test instead of skipping it, so `make test`
// cannot go green by silently not running the integration tests.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/migrate"
	"smartcampaign/migrations"
)

// EnvURL is the environment variable that holds the admin connection URL.
const EnvURL = "TEST_DATABASE_URL"

// New creates a database named scc_test_<random>, applies all migrations and returns a pool for
// it. The database is dropped when the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Fatalf("%s is not set: integration tests need a PostgreSQL server (run them through `make test`)", EnvURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatalf("PostgreSQL is not reachable: %v", err)
	}

	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		admin.Close()
		t.Fatalf("random name: %v", err)
	}
	name := "scc_test_" + hex.EncodeToString(suffix[:])
	// The name is generated above (lower-case hex), so it is safe to put into the statement.
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Fatalf("creating test database: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close()
		t.Fatalf("parsing %s: invalid format", EnvURL)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 30
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		dropDatabase(t, admin, name)
		t.Fatalf("connecting to the test database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropDatabase(t, admin, name)
	})

	if _, err := migrate.Up(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("migrating the test database: %v", err)
	}
	return pool
}

func dropDatabase(t testing.TB, admin *pgxpool.Pool, name string) {
	t.Helper()
	defer admin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
		t.Errorf("dropping test database %s: %v", name, err)
	}
}

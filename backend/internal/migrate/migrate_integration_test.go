//go:build integration

package migrate_test

import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"

	"smartcampaign/internal/migrate"
	"smartcampaign/internal/testdb"
	"smartcampaign/migrations"
)

// withExtra returns the real migrations plus one more file.
func withExtra(t *testing.T, name, sql string) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = &fstest.MapFile{Data: b}
	}
	out[name] = &fstest.MapFile{Data: []byte(sql)}
	return out
}

func TestUp_secondRunAppliesNothing(t *testing.T) {
	pool := testdb.New(t) // already ran Up once
	done, err := migrate.Up(context.Background(), pool, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 0 {
		t.Fatalf("second run applied %v, want nothing", done)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	all, _ := migrate.Load(migrations.FS)
	if n != len(all) {
		t.Fatalf("schema_migrations has %d rows, want %d", n, len(all))
	}
}

func TestUp_detectsModifiedMigration(t *testing.T) {
	pool := testdb.New(t)
	fsys := withExtra(t, "0001_users_events.sql", "SELECT 1")
	_, err := migrate.Up(context.Background(), pool, fsys)
	if !errors.Is(err, migrate.ErrChecksum) {
		t.Fatalf("error = %v, want ErrChecksum", err)
	}
}

func TestUp_failedMigrationLeavesNothingBehind(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	fsys := withExtra(t, "0003_broken.sql", "CREATE TABLE half_done (id int); SELECT * FROM table_that_does_not_exist;")
	if _, err := migrate.Up(ctx, pool, fsys); err == nil {
		t.Fatal("want an error from the broken migration")
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('half_done') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("the first statement of the failed migration must be rolled back")
	}
	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version = 3`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Fatal("a failed migration must not be recorded")
	}
}

func TestUp_concurrentRunnersApplyOnce(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	fsys := withExtra(t, "0003_extra.sql", "CREATE TABLE extra (id int)")

	const runners = 6
	counts := make([]int, runners)
	errs := make([]error, runners)
	var wg sync.WaitGroup
	for i := range runners {
		wg.Go(func() {
			done, err := migrate.Up(ctx, pool, fsys)
			counts[i], errs[i] = len(done), err
		})
	}
	wg.Wait()

	applied := 0
	for i := range runners {
		if errs[i] != nil {
			t.Fatalf("runner %d: %v", i, errs[i])
		}
		applied += counts[i]
	}
	if applied != 1 {
		t.Fatalf("migration 3 was applied %d times in total, want once", applied)
	}
}

func TestUp_refusesADatabaseAheadOfTheBinary(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES (99, 'from_the_future', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Up(ctx, pool, migrations.FS); !errors.Is(err, migrate.ErrUnknownMigration) {
		t.Fatalf("error = %v, want ErrUnknownMigration", err)
	}
}

// Package migrate applies the embedded SQL migrations in order and records them in the
// schema_migrations table. It is deliberately small: forward-only, one transaction per file,
// and a checksum so an already applied file cannot be edited silently.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// lockKey is the advisory lock that serializes concurrent runners (for example two
// containers starting together).
const lockKey int64 = 0x5343435f4d49 // "SCC_MI"

var fileName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// ErrChecksum means an applied migration file was changed afterwards.
var ErrChecksum = errors.New("applied migration was modified")

// Migration is one SQL file.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

// Load reads and orders the migrations in fsys (the root holds the .sql files). Versions must
// start at 1 and have no gaps or duplicates, so a missing file is noticed before anything runs.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("reading migrations: %w", err)
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := fileName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration file %q does not match NNNN_name.sql", e.Name())
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("migration file %q: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("reading migration %q: %w", e.Name(), err)
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{Version: version, Name: m[2], SQL: string(body), Checksum: hex.EncodeToString(sum[:])})
	}
	slices.SortFunc(out, func(a, b Migration) int { return a.Version - b.Version })
	for i, m := range out {
		if want := i + 1; m.Version != want {
			return nil, fmt.Errorf("migration versions must be 1, 2, 3, ...: found %04d where %04d was expected", m.Version, want)
		}
	}
	return out, nil
}

// Up applies every migration that is not recorded yet and returns the versions it applied.
func Up(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]int, error) {
	migrations, err := Load(fsys)
	if err != nil {
		return nil, err
	}

	// A dedicated connection: the advisory lock belongs to the session.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return nil, fmt.Errorf("taking migration lock: %w", err)
	}
	defer func() {
		// Best effort: closing the session releases the lock anyway.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockKey)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER     PRIMARY KEY,
		name       TEXT        NOT NULL,
		checksum   TEXT        NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, fmt.Errorf("creating schema_migrations: %w", err)
	}

	applied, err := appliedChecksums(ctx, conn)
	if err != nil {
		return nil, err
	}

	var done []int
	for _, m := range migrations {
		if sum, ok := applied[m.Version]; ok {
			if sum != m.Checksum {
				return done, fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, ErrChecksum)
			}
			continue
		}
		if err := apply(ctx, conn, m); err != nil {
			return done, fmt.Errorf("applying migration %04d_%s: %w", m.Version, m.Name, err)
		}
		done = append(done, m.Version)
	}
	return done, nil
}

func appliedChecksums(ctx context.Context, conn *pgxpool.Conn) (map[int]string, error) {
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, fmt.Errorf("scanning schema_migrations: %w", err)
		}
		out[v] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	return out, nil
}

// apply runs one file and records it in the same transaction, so a failure leaves nothing behind.
func apply(ctx context.Context, conn *pgxpool.Conn, m Migration) (err error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			// Roll back on a context that survives the caller's cancellation.
			if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
			}
		}
	}()
	// No arguments: pgx sends the whole file with the simple protocol, which allows many statements.
	if _, err = tx.Exec(ctx, m.SQL); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		m.Version, m.Name, m.Checksum); err != nil {
		return fmt.Errorf("recording version: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

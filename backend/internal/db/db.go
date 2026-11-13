// Package db opens the PostgreSQL connection pool used by every command.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidURL means the database URL cannot be parsed. The text of the parse error is left out because
// it can echo parts of the URL.
var ErrInvalidURL = errors.New("parsing database url: invalid format")

// Options tunes the pool. The zero value gives the defaults below.
type Options struct {
	MaxConns int32 // default 20
}

// Open parses the URL, applies the pool limits and checks that the database answers.
// The caller closes the pool.
func Open(ctx context.Context, url string, opts Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		// pgx error text can echo parts of the URL; keep it out of the message.
		return nil, ErrInvalidURL
	}
	cfg.MaxConns = 20
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	}
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return pool, nil
}

// WaitReady retries Open until the database answers or ctx ends. The compose stack uses it so
// a command started next to the database does not fail on a slow start.
func WaitReady(ctx context.Context, url string, opts Options) (*pgxpool.Pool, error) {
	delay := 200 * time.Millisecond
	for {
		pool, err := Open(ctx, url, opts)
		if err == nil {
			return pool, nil
		}
		if errors.Is(err, ErrInvalidURL) {
			return nil, err // waiting will not fix it
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("database not ready: %w (last error: %v)", ctx.Err(), err)
		case <-time.After(delay):
		}
		delay = min(delay*2, 2*time.Second)
	}
}

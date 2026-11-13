// Command composer is the single backend binary: api, worker, migrate and seed are
// subcommands of it. api is a stub until its component is implemented.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/db"
	"smartcampaign/internal/migrate"
	"smartcampaign/internal/seed"
	"smartcampaign/migrations"
)

const usage = "usage: composer <api|worker|migrate|seed>"

// Environment variables read by the commands.
const (
	envDatabaseURL = "DATABASE_URL"
	envSeedUsers   = "SEED_USERS"
)

func main() {
	// SIGTERM / SIGINT cancel the context; commands finish what they hold and return.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches the subcommand and returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "api":
		fmt.Fprintf(stdout, "composer %s: not implemented yet\n", args[0])
		return 0
	case "worker":
		err = runWorker(ctx, stdout)
	case "migrate":
		err = runMigrate(ctx, stdout)
	case "seed":
		err = runSeed(ctx, stdout)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s\n", args[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "composer %s: %v\n", args[0], err)
		return 1
	}
	return 0
}

var errNoDatabaseURL = errors.New(envDatabaseURL + " is not set")

// openDB connects to the database named by DATABASE_URL, waiting briefly for a database that
// is still starting (compose starts the commands next to it).
func openDB(ctx context.Context) (*pgxpool.Pool, error) {
	url := os.Getenv(envDatabaseURL)
	if url == "" {
		return nil, errNoDatabaseURL
	}
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	pool, err := db.WaitReady(waitCtx, url, db.Options{})
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func runMigrate(ctx context.Context, stdout io.Writer) error {
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := migrate.Up(ctx, pool, migrations.FS)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "migrations applied: %d\n", len(applied))
	return nil
}

func runSeed(ctx context.Context, stdout io.Writer) error {
	opts := seed.Options{}
	if v := os.Getenv(envSeedUsers); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return fmt.Errorf("%s must be a positive number", envSeedUsers)
		}
		opts.Users = n
	}
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	res, err := seed.Run(ctx, pool, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "seed: %d users and %d events inserted\n", res.UsersInserted, res.EventsInserted)
	return nil
}

func runWorker(_ context.Context, stdout io.Writer) error {
	fmt.Fprintln(stdout, "composer worker: not implemented yet")
	return nil
}

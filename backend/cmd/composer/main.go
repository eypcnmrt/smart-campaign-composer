// Command composer is the single backend binary: api, worker, migrate and seed are
// subcommands of it. api is a stub until its component is implemented.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/db"
	"smartcampaign/internal/migrate"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/seed"
	"smartcampaign/internal/worker"
	"smartcampaign/migrations"
)

const usage = "usage: composer <api|worker|migrate|seed>"

// Environment variables read by the commands.
const (
	envDatabaseURL   = "DATABASE_URL"
	envSeedUsers     = "SEED_USERS"
	envProviderURL   = "PROVIDER_URL"
	envWorkerRate    = "WORKER_RATE"
	envWorkerTimeout = "WORKER_REQUEST_TIMEOUT"
	envWorkerBeat    = "WORKER_HEARTBEAT_INTERVAL"

	defaultProviderURL = "http://mock-provider:8081"
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
		err = runWorker(ctx, stderr)
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

// validateProviderURL rejects a provider address that cannot work (no scheme, no host, another scheme). Without this a
// typo such as "mock-provider:8081" is taken for an unknown scheme at the first request, which is classed as a
// transient error, and every delivery would be retried for nothing. The value is not echoed: it can carry credentials.
func validateProviderURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("%s must be an http or https URL with a host, for example http://mock-provider:8081", envProviderURL)
	}
	return nil
}

// runWorker serves the delivery queue until ctx ends (SIGTERM), then lets the sends in flight finish.
func runWorker(ctx context.Context, logOut io.Writer) error {
	cfg := worker.Config{Logger: slog.New(slog.NewTextHandler(logOut, nil))}
	if v := os.Getenv(envWorkerRate); v != "" {
		rate, err := strconv.ParseFloat(v, 64)
		if err != nil || rate < 1 {
			return fmt.Errorf("%s must be a number of at least 1", envWorkerRate)
		}
		cfg.RatePerSecond = rate
	}
	if v := os.Getenv(envWorkerTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s must be a positive duration such as 5s", envWorkerTimeout)
		}
		cfg.RequestTimeout = d
	}
	if v := os.Getenv(envWorkerBeat); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s must be a positive duration such as 2s", envWorkerBeat)
		}
		cfg.HeartbeatInterval = d
	}
	providerURL := os.Getenv(envProviderURL)
	if providerURL == "" {
		providerURL = defaultProviderURL
	}
	if err := validateProviderURL(providerURL); err != nil {
		return err
	}

	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	w, err := worker.New(queue.New(pool), worker.NewHTTPSender(providerURL), cfg)
	if err != nil {
		return err
	}
	cfg.Logger.Info("worker started", "provider", providerURL)
	if err := w.Run(ctx); err != nil {
		return err
	}
	cfg.Logger.Info("worker stopped", "counters", fmt.Sprintf("%+v", w.Counters()))
	return nil
}

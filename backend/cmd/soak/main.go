// Command soak sends thousands of messages through the real worker and a chaos-configured mock provider
// and reports whether the delivery guarantees held. It is run by `make soak`, not by `make test`.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"smartcampaign/internal/db"
	"smartcampaign/internal/soak"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	code := run(ctx, os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg := soak.Config{Users: 2500, ProviderURL: "http://mock-provider:8081", RateCap: 100, Timeout: 15 * time.Minute}
	for name, dst := range map[string]*int{"SOAK_USERS": &cfg.Users, "SOAK_RATE_CAP": &cfg.RateCap} {
		if v := getenv(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				fmt.Fprintf(stderr, "soak: %s must be a positive number\n", name)
				return 2
			}
			*dst = n
		}
	}
	if v := getenv("PROVIDER_URL"); v != "" {
		cfg.ProviderURL = v
	}
	url := getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(stderr, "soak: DATABASE_URL is not set")
		return 2
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	pool, err := db.WaitReady(waitCtx, url, db.Options{})
	cancel()
	if err != nil {
		fmt.Fprintf(stderr, "soak: %v\n", err)
		return 1
	}
	defer pool.Close()

	if err := soak.Run(ctx, pool, cfg, stdout); err != nil {
		fmt.Fprintf(stderr, "soak: %v\n", err)
		return 1
	}
	return 0
}

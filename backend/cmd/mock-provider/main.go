// Command mock-provider is the stand-in SMS/email provider used by the worker. Its behaviour
// (latency, failures, 429s, own rate limit, late replies) is set through MOCK_* variables, see
// internal/mockprovider.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"smartcampaign/internal/mockprovider"
)

const envAddr = "MOCK_ADDR"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	code := run(ctx, os.Getenv, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, getenv func(string) string, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	cfg, err := mockprovider.ConfigFromEnv(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "mock-provider: %v\n", err)
		return 2
	}
	srv, err := mockprovider.New(cfg, log)
	if err != nil {
		fmt.Fprintf(stderr, "mock-provider: %v\n", err)
		return 2
	}
	addr := getenv(envAddr)
	if addr == "" {
		addr = ":8081"
	}
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	log.Info("mock provider listening", "addr", addr, "config", fmt.Sprintf("%+v", cfg))

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server stopped", "err", err)
			return 1
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown", "err", err)
			return 1
		}
	}
	return 0
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"smartcampaign/internal/api"
	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/queue"
)

// Environment variables of the api command.
const (
	envAPIAddr         = "API_ADDR"
	envLLMMaxParallel  = "LLM_MAX_CONCURRENCY"
	envWorkerStale     = "WORKER_STALE_AFTER"
	envWorkerMetrics   = "WORKER_METRICS_ADDR"
	defaultAPIAddr     = ":8080"
	defaultWorkerAddr  = ":9091"
	shutdownGracePause = 15 * time.Second
)

// onListen is told the address a server of this binary listens on; tests use it to find the port they asked for.
var onListen = func(name string, addr net.Addr) {}

// apiSettings reads the optional settings of the api command.
func apiSettings(getenv func(string) string) (addr string, cfg api.Config, err error) {
	addr = getenv(envAPIAddr)
	if addr == "" {
		addr = defaultAPIAddr
	}
	if v := getenv(envLLMMaxParallel); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return "", cfg, fmt.Errorf("%s must be a whole number of at least 1", envLLMMaxParallel)
		}
		cfg.LLMConcurrency = n
	}
	if v := getenv(envWorkerStale); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return "", cfg, fmt.Errorf("%s must be a positive duration such as 10s", envWorkerStale)
		}
		cfg.WorkerStaleAfter = d
	}
	return addr, cfg, nil
}

// logLLMCall logs what one model call used. It carries no brief, no answer and no key: only the task, the model, the tokens,
// the cost and the times. first_text_ms is set for a streamed call that produced text.
func logLLMCall(log *slog.Logger) func(llm.Call) {
	return func(c llm.Call) {
		attrs := []any{
			"task", c.Task, "model", c.Model, "served_by", c.ServedBy, "stream", c.Stream,
			"input_tokens", c.InputTokens, "output_tokens", c.OutputTokens,
			"cost_usd", fmt.Sprintf("%.6f", c.CostUSD), "duration_ms", c.Duration.Milliseconds(),
		}
		if c.FirstText > 0 {
			attrs = append(attrs, "first_text_ms", c.FirstText.Milliseconds())
		}
		if c.Err != nil {
			log.Warn("llm call failed", append(attrs, "err", c.Err)...)
			return
		}
		log.Info("llm call", attrs...)
	}
}

// runAPI serves the HTTP API until ctx ends (SIGTERM): open content streams end with an error event, in-flight requests
// finish within the grace period, then the process exits.
func runAPI(ctx context.Context, logOut io.Writer) error {
	log := slog.New(slog.NewTextHandler(logOut, nil))
	addr, cfg, err := apiSettings(os.Getenv)
	if err != nil {
		return err
	}
	// The model is chosen before anything else: LLM_MODE=anthropic without a key must stop the process here, loudly, and
	// not after the database has been waited for.
	sel, err := llm.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	log.Info("llm client chosen", "mode", sel.Client.Mode(), "setting", sel.Setting, "model", sel.Client.Model(), "reason", sel.Reason)
	client := sel.Client
	if client.Mode() == llm.ModeAnthropic {
		client = llm.Metered(client, logLLMCall(log)) // the real model costs money: every call is logged with its tokens and cost
	}

	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	m := metrics.NewAPI(pool)
	srv, err := api.New(api.Deps{
		Store: campaign.NewStore(pool), Queue: queue.New(pool), Generator: compose.New(client), LLM: client,
		DB: pool, Metrics: m, MetricsHandler: m.Handler(), Logger: log, Config: cfg,
	})
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	onListen("api", ln.Addr())
	httpSrv := &http.Server{
		Handler: srv.Handler(),
		// No WriteTimeout: a content stream lasts minutes. The request is small, so reading it gets a deadline.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	served := make(chan error, 1)
	go func() { served <- httpSrv.Serve(ln) }()
	log.Info("api listening", "addr", ln.Addr().String())

	select {
	case err := <-served:
		return fmt.Errorf("serving: %w", err) // Serve returns only on a failure while the server was not asked to stop
	case <-ctx.Done():
	}

	log.Info("shutting down")
	srv.BeginShutdown() // open streams end with an event instead of being cut off
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGracePause)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		_ = httpSrv.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("api stopped")
	return nil
}

// startOpsServer serves handler on addr in the background and returns the function that stops it. The worker uses it for its
// own /metrics and /healthz, since it has no other HTTP surface.
func startOpsServer(name, addr string, handler http.Handler, log *slog.Logger) (stop func(), err error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%s: listening on %s: %w", name, addr, err)
	}
	onListen(name, ln.Addr())
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("the "+name+" server stopped unexpectedly", "err", err)
		}
	}()
	log.Info("listening", "server", name, "addr", ln.Addr().String())
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Warn("the "+name+" server did not stop in time", "err", err)
			_ = srv.Close()
		}
	}, nil
}

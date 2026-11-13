//go:build integration

package soak_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/retry"
	"smartcampaign/internal/soak"
	"smartcampaign/internal/testdb"
	"smartcampaign/internal/worker"
)

// The driver end to end on a small scale: real database, in-process provider with chaos, real worker.
func TestRun_smallScale(t *testing.T) {
	provider, err := mockprovider.New(mockprovider.Config{
		Seed: 3, DelayMin: time.Millisecond, DelayMax: 5 * time.Millisecond,
		ErrorRate: 0.1, ThrottleRate: 0.1, RetryAfter: time.Second, LateRate: 0.05, LateDelay: 500 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(provider.Handler())
	defer ts.Close()

	pool := testdb.New(t)
	w, err := worker.New(queue.New(pool), worker.NewHTTPSender(ts.URL), worker.Config{
		RatePerSecond: 200, RequestTimeout: 250 * time.Millisecond, Lease: 5 * time.Second, MaintenanceInterval: 100 * time.Millisecond,
		Retry: retry.Policy{MaxAttempts: 5, BaseDelay: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond, MaxRetryAfter: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { cancel(); <-done }()

	var out bytes.Buffer
	if err := soak.Run(context.Background(), pool, soak.Config{Users: 150, ProviderURL: ts.URL, RateCap: 200, Timeout: time.Minute}, &out); err != nil {
		t.Fatalf("soak.Run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "PASS") {
		t.Fatalf("report does not say PASS:\n%s", out.String())
	}
}

func TestRun_invalidConfig(t *testing.T) {
	pool := testdb.New(t)
	if err := soak.Run(context.Background(), pool, soak.Config{}, &bytes.Buffer{}); err == nil {
		t.Fatal("an empty config must be refused")
	}
}

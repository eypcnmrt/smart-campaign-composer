package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun_rejectsBadConfig(t *testing.T) {
	t.Parallel()
	env := map[string]string{"MOCK_ERROR_RATE": "2"}
	var stderr bytes.Buffer
	code := run(context.Background(), func(k string) string { return env[k] }, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "error rate") {
		t.Fatalf("code = %d, stderr = %q, want 2 and a message about the error rate", code, stderr.String())
	}
}

func TestRun_stopsOnCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	env := map[string]string{envAddr: "127.0.0.1:0"}
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() { done <- run(ctx, func(k string) string { return env[k] }, &stderr) }()
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
}

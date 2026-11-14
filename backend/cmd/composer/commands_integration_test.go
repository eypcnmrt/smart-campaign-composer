//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"smartcampaign/internal/testdb"
)

// lockedBuffer is a log destination that the command and the test can use from different goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// listenAddrs captures the addresses the commands listen on, by server name.
func listenAddrs(t *testing.T) <-chan string {
	t.Helper()
	ch := make(chan string, 4)
	onListen = func(name string, a net.Addr) { ch <- name + " " + a.String() }
	t.Cleanup(func() { onListen = func(string, net.Addr) {} })
	return ch
}

func waitAddr(t *testing.T, ch <-chan string, name string) string {
	t.Helper()
	select {
	case got := <-ch:
		if !strings.HasPrefix(got, name+" ") {
			t.Fatalf("got %q, want the %s server", got, name)
		}
		return strings.TrimPrefix(got, name+" ")
	case <-time.After(30 * time.Second):
		t.Fatalf("the %s server did not start", name)
		return ""
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// The api command against a real database: it chooses the model, serves, and stops cleanly on the signal.
func TestRun_apiServesAndStopsOnTheSignal(t *testing.T) {
	_, url := testdb.NewWithURL(t)
	t.Setenv(envDatabaseURL, url)
	t.Setenv(envAPIAddr, "127.0.0.1:0")
	t.Setenv("LLM_MODE", "mock")
	t.Setenv("ANTHROPIC_API_KEY", "")
	addrs := listenAddrs(t)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"api"}, &stdout, &stderr) }()
	base := "http://" + waitAddr(t, addrs, "api")

	if code, _ := get(t, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz = %d", code)
	}
	if code, _ := get(t, base+"/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz = %d", code)
	}
	if code, body := get(t, base+"/api/config"); code != http.StatusOK || !strings.Contains(body, `"llm_mode":"mock"`) || !strings.Contains(body, `"model":"mock"`) {
		t.Fatalf("/api/config = %d %s", code, body)
	}
	if code, body := get(t, base+"/metrics"); code != http.StatusOK || !strings.Contains(body, "scc_http_request_duration_seconds") {
		t.Fatalf("/metrics = %d", code)
	}

	stop() // SIGTERM
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d, want 0; stderr:\n%s", code, stderr.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the api did not stop after the signal")
	}
	log := stderr.String()
	for _, want := range []string{"llm client chosen", "mode=mock", "LLM_MODE=mock", "shutting down", "api stopped"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "ANTHROPIC_API_KEY=") {
		t.Errorf("the log mentions a key value:\n%s", log)
	}
}

// The worker command serves its own metrics and a liveness check, and reports that it serves the channels.
func TestRun_workerServesMetrics(t *testing.T) {
	_, url := testdb.NewWithURL(t)
	t.Setenv(envDatabaseURL, url)
	t.Setenv(envWorkerMetrics, "127.0.0.1:0")
	t.Setenv(envProviderURL, "http://127.0.0.1:1") // nothing is sent in this test
	addrs := listenAddrs(t)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"worker"}, &stdout, &stderr) }()
	base := "http://" + waitAddr(t, addrs, "worker-metrics")

	if code, _ := get(t, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz = %d", code)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, body := get(t, base+"/metrics")
		if code == http.StatusOK && strings.Contains(body, `scc_worker_channel_lock_held{channel="sms"} 1`) &&
			strings.Contains(body, `scc_worker_channel_lock_held{channel="email"} 1`) &&
			strings.Contains(body, `scc_worker_circuit_state{channel="sms",state="closed"} 1`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker never reported that it serves the channels:\n%s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}

	stop()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d, want 0; stderr:\n%s", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not stop after the signal")
	}
}

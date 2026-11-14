//go:build integration

package api_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smartcampaign/internal/api"
	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/queue"
)

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func TestAPI_livenessAndReadiness(t *testing.T) {
	e, _ := newEnv(t)
	if body := e.expect("GET", "/healthz", nil, http.StatusOK, "").json(t); body["status"] != "ok" {
		t.Fatalf("healthz = %v", body)
	}
	if body := e.expect("GET", "/readyz", nil, http.StatusOK, "").json(t); body["status"] != "ready" {
		t.Fatalf("readyz = %v", body)
	}

	// without a database the process is alive but not ready, and says so in the common error shape
	client := llm.NewScripted()
	srv, err := api.New(api.Deps{
		Store: campaign.NewStore(e.pool), Queue: queue.New(e.pool), Generator: compose.New(client), LLM: client,
		DB: pinger{err: errors.New("connection refused")}, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	check := func(path string, want int, wantCode string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("GET %s = %d, want %d\n%s", path, resp.StatusCode, want, body)
		}
		if wantCode != "" {
			if code, _ := (response{body: body}).errorOf(t); code != wantCode {
				t.Fatalf("GET %s: code = %q, want %q", path, code, wantCode)
			}
		}
	}
	check("/healthz", http.StatusOK, "")
	check("/readyz", http.StatusServiceUnavailable, "not_ready")
	check("/metrics", http.StatusNotFound, "not_found") // no metrics handler was given
}

func TestAPI_metrics(t *testing.T) {
	e, _ := newEnv(t, llm.Reply(ankaraRule), llm.Reply(goodContent))
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
	e.stream(path(id, "/content/stream"))
	e.expect("POST", path(id, "/approve"), map[string]any{"version": 3}, http.StatusOK, "")
	e.expect("GET", path(id, "/stats"), nil, http.StatusOK, "")
	e.expect("GET", "/api/nothing", nil, http.StatusNotFound, "not_found")

	r := e.expect("GET", "/metrics", nil, http.StatusOK, "")
	out := string(r.body)
	for _, want := range []string{
		// HTTP durations by route pattern, not by raw path
		`scc_http_request_duration_seconds_count{method="POST",route="/api/campaigns",status="201"} 1`,
		`scc_http_request_duration_seconds_count{method="GET",route="/api/campaigns/{id}/stats",status="200"} 1`,
		`scc_http_request_duration_seconds_count{method="POST",route="/api/campaigns/{id}/content/stream",status="200"} 1`,
		`scc_http_request_duration_seconds_count{method="POST",route="/api/campaigns/{id}/approve",status="200"} 1`,
		`scc_http_request_duration_seconds_count{method="GET",route="/api/*",status="404"} 1`, // an unknown path under /api
		// model calls by outcome
		`scc_llm_calls_total{outcome="ok",task="segment"} 1`,
		`scc_llm_calls_total{outcome="ok",task="content"} 1`,
		// deliveries by channel and status, counted in the database
		`scc_deliveries{channel="sms",status="queued"} 10`,
		`scc_deliveries{channel="email",status="queued"} 10`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the scrape is missing %q", want)
		}
	}
	if strings.Contains(out, `route="/api/campaigns/`+"1"+`/`) {
		t.Error("a raw campaign id is a label value: one series per campaign")
	}
}

// Failed generations are counted by what went wrong.
func TestAPI_metricsCountFailedGenerationsByOutcome(t *testing.T) {
	e, _ := newEnv(t, llm.Fail(&llm.RefusalError{Category: "cyber"}), llm.Fail(&llm.RateLimitError{}))
	e.expect("POST", "/api/campaigns", map[string]string{"brief": "a"}, http.StatusUnprocessableEntity, "llm_refused")
	e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusTooManyRequests, "llm_rate_limited")
	out := string(e.expect("GET", "/metrics", nil, http.StatusOK, "").body)
	for _, want := range []string{
		`scc_llm_calls_total{outcome="refused",task="segment"} 1`,
		`scc_llm_calls_total{outcome="rate_limited",task="segment"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the scrape is missing %q", want)
		}
	}
}

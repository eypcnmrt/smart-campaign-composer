package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/llm"
)

func TestKnownProblem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string // empty: not a known problem
	}{
		{"a refusal", &llm.RefusalError{Category: "x"}, 422, "llm_refused"},
		{"a model timeout", llm.ErrTimeout, 504, "llm_timeout"},
		{"the task deadline", fmt.Errorf("%w: %w", errTaskTimeout, context.DeadlineExceeded), 504, "llm_timeout"},
		{"a bare deadline is not an llm timeout", fmt.Errorf("counting: %w", context.DeadlineExceeded), 0, ""},
		{"repairs run out", &compose.GenerationError{Stage: "segment", Attempts: 3, Problems: []string{"p"}}, 502, "llm_invalid_output"},
		{"the model is busy", errBusy, 503, "llm_busy"},
		{"canceled by the client", context.Canceled, 499, "client_closed"},
		{"an approval that may have been applied", fmt.Errorf("%w: commit: boom", campaign.ErrApprovalUnconfirmed), 500, "approval_unconfirmed"},
		{"not found", campaign.ErrNotFound, 404, "not_found"},
		{"nobody to send to", &campaign.NoRecipientsError{Channels: []string{"sms"}}, 422, "no_recipients"},
		{"a state change", fmt.Errorf("x: %w", campaign.ErrStateChanged), 409, "state_changed"},
		{"a database error", errors.New("pq: connection reset"), 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := knownProblem(tt.err)
			if tt.code == "" {
				if p != nil {
					t.Fatalf("knownProblem = %+v, want it to be unknown", p)
				}
				return
			}
			if p == nil || p.Status != tt.status || p.Code != tt.code {
				t.Fatalf("knownProblem = %+v, want %d %s", p, tt.status, tt.code)
			}
		})
	}
}

// An error the client gets a generic answer for must leave its cause in the log, or it exists nowhere.
func TestProblemOf_logsTheCauseOfOperatorFailures(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := &Server{log: slog.New(slog.NewTextHandler(&buf, nil))}

	p := s.problemOf(fmt.Errorf("anthropic: 401 invalid x-api-key: %w", llm.ErrBadRequest), "req-1")
	if p.Code != "llm_error" || strings.Contains(p.Message, "401") {
		t.Fatalf("problem = %+v, want a generic answer", p)
	}
	log := buf.String()
	if !strings.Contains(log, "req-1") || !strings.Contains(log, "401 invalid x-api-key") || !strings.Contains(log, "llm_error") {
		t.Fatalf("the log lacks the cause:\n%s", log)
	}

	buf.Reset()
	s.problemOf(campaign.ErrNotFound, "req-2")
	s.problemOf(&problem{Status: 422, Code: "validation_failed", Message: "m"}, "req-3")
	if buf.Len() != 0 {
		t.Fatalf("expected client mistakes are not logged, got:\n%s", buf.String())
	}

	// unknown errors: logged once, and never sent to the client
	buf.Reset()
	p = s.problemOf(errors.New("secret SQL text"), "req-4")
	if p.Code != "internal" || strings.Contains(fmt.Sprint(p.Details, p.Message), "secret") || strings.Count(buf.String(), "secret SQL text") != 1 {
		t.Fatalf("problem = %+v, log:\n%s", p, buf.String())
	}
}

// A shutdown or a timeout replaces only a context error: a real failure that arrives at the same moment keeps its own face.
func TestCause_replacesOnlyContextErrors(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errShuttingDown)

	if got := cause(ctx, fmt.Errorf("compose: %w", context.Canceled)); !errors.Is(got, errShuttingDown) {
		t.Fatalf("cause = %v, want the shutdown", got)
	}
	if got := cause(ctx, llm.ErrTimeout); !errors.Is(got, errShuttingDown) {
		t.Fatalf("cause = %v, want the shutdown for the model's timeout too", got)
	}
	for _, real := range []error{campaign.ErrStateChanged, errors.New("database is gone"), &compose.GenerationError{Stage: "content"}} {
		if got := cause(ctx, real); !errors.Is(got, real) || errors.Is(got, errShuttingDown) {
			t.Fatalf("cause(%v) = %v: a real failure was reported as a shutdown", real, got)
		}
	}
}

func TestTaskTimeout(t *testing.T) {
	t.Parallel()
	if got := taskTimeout(fmt.Errorf("segment: %w", context.DeadlineExceeded)); !errors.Is(got, errTaskTimeout) || !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("taskTimeout = %v, want it to match both", got)
	}
	plain := errors.New("x")
	if got := taskTimeout(plain); got != plain {
		t.Fatalf("taskTimeout changed an unrelated error: %v", got)
	}
}

// A value that cannot be encoded must not become a 200 with an empty body.
func TestWriteJSON_encodingFailureIsA500(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"bad": make(chan int)})
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"code":"internal"`) {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]int{"a": 1})
	if rec.Code != http.StatusCreated || strings.TrimSpace(rec.Body.String()) != `{"a":1}` {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestConfig_streamWriteTimeoutDefault(t *testing.T) {
	t.Parallel()
	if got := (Config{}).withDefaults().SSEWriteTimeout; got != 15*time.Second {
		t.Fatalf("default SSEWriteTimeout = %v, want 15s", got)
	}
	if err := (Config{SSEWriteTimeout: -time.Second}).withDefaults().validate(); err == nil {
		t.Fatal("a negative write timeout was accepted")
	}
}

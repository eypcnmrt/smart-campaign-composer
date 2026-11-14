package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/worker"
)

var _ worker.Observer = (*Worker)(nil)

// scrape returns the text a scrape of the handler would get.
func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape = %d\n%s", rec.Code, body)
	}
	return string(body)
}

func TestWorker_metrics(t *testing.T) {
	t.Parallel()
	w := NewWorker()
	w.Attempt("sms", "sent")
	w.Attempt("sms", "sent")
	w.Attempt("sms", "refunded")
	w.Attempt("email", "failed")
	w.Circuit("sms", "open")
	w.Circuit("email", "closed")
	w.LockHeld("sms", true)
	w.LockHeld("email", false)
	w.HeartbeatError("sms")

	out := scrape(t, w.Handler())
	for _, want := range []string{
		`scc_worker_attempts_total{channel="sms",result="sent"} 2`,
		`scc_worker_attempts_total{channel="sms",result="refunded"} 1`,
		`scc_worker_attempts_total{channel="email",result="failed"} 1`,
		`scc_worker_circuit_state{channel="sms",state="open"} 1`,
		`scc_worker_circuit_state{channel="sms",state="closed"} 0`,
		`scc_worker_circuit_state{channel="sms",state="half_open"} 0`,
		`scc_worker_circuit_state{channel="email",state="closed"} 1`,
		`scc_worker_channel_lock_held{channel="sms"} 1`,
		`scc_worker_channel_lock_held{channel="email"} 0`,
		`scc_worker_heartbeat_errors_total{channel="sms"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the scrape is missing %q", want)
		}
	}

	// a circuit that closes again is reported as closed, not as both
	w.Circuit("sms", "closed")
	out = scrape(t, w.Handler())
	if !strings.Contains(out, `scc_worker_circuit_state{channel="sms",state="open"} 0`) || !strings.Contains(out, `scc_worker_circuit_state{channel="sms",state="closed"} 1`) {
		t.Error("the circuit gauge kept the old state")
	}
}

func TestAPI_httpAndLLMMetrics(t *testing.T) {
	t.Parallel()
	// no database: the delivery collector is exercised by the integration test
	a := newAPI()
	a.ObserveHTTP("GET", "/api/campaigns/{id}/stats", 200, 12*time.Millisecond)
	a.ObserveHTTP("POST", "/api/campaigns", 201, 2*time.Second)
	a.ObserveLLM("segment", "ok", time.Second)
	a.ObserveLLM("content", "refused", 3*time.Second)
	a.ObserveLLM("content", "refused", time.Second)

	out := scrape(t, a.Handler())
	for _, want := range []string{
		`scc_http_request_duration_seconds_count{method="GET",route="/api/campaigns/{id}/stats",status="200"} 1`,
		`scc_http_request_duration_seconds_count{method="POST",route="/api/campaigns",status="201"} 1`,
		`scc_llm_calls_total{outcome="ok",task="segment"} 1`,
		`scc_llm_calls_total{outcome="refused",task="content"} 2`,
		`scc_llm_call_duration_seconds_count{task="content"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the scrape is missing %q\n%s", want, out)
		}
	}
}

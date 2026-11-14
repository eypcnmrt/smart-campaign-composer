//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"smartcampaign/internal/compose"
	"smartcampaign/internal/llm"
)

func mustJSON(t *testing.T, data string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(data), dst); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
}

func lastError(t *testing.T, msgs []sseMessage) (code string, details any) {
	t.Helper()
	if len(msgs) == 0 || msgs[len(msgs)-1].event != "error" {
		t.Fatalf("events = %v, want the stream to end with an error event", eventNames(msgs))
	}
	var body map[string]any
	mustJSON(t, msgs[len(msgs)-1].data, &body)
	e, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("the error event is not in the common shape: %v", body)
	}
	return e["code"].(string), e["details"]
}

// openStream starts a stream that the test can cancel, and returns what arrives on it once it ends.
func (e *env) openStream(ctx context.Context, path string) (resp *http.Response, msgs <-chan []sseMessage) {
	e.t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ts.URL+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	ch := make(chan []sseMessage, 1)
	go func() {
		defer resp.Body.Close()
		ch <- readSSE(e.t, resp.Body)
	}()
	return resp, ch
}

var (
	tooLong = `{"sms":"` + strings.Repeat("x", 170) + `","email_subject":"s","email_body":"b"}`
	newer   = `{"sms":"Yeni metin {{first_name}}","email_subject":"Yeni konu","email_body":"Yeni gövde"}`
)

func TestStream_aRepairStartsANewAttempt(t *testing.T) {
	e, _ := newEnv(t, llm.Reply(ankaraRule), llm.Reply(tooLong), llm.Reply(goodContent))
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))

	_, msgs := e.stream(path(id, "/content/stream"))
	var attempts []int
	for _, m := range msgs {
		if m.event == "attempt" {
			var a struct{ Attempt int }
			mustJSON(t, m.data, &a)
			attempts = append(attempts, a.Attempt)
		}
	}
	if !slices.Equal(attempts, []int{1, 2}) || msgs[len(msgs)-1].event != "done" {
		t.Fatalf("attempts = %v, events = %v; want attempt 1, then 2 after the repair, then done", attempts, eventNames(msgs))
	}
	got := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t)
	if field(t, got, "content", "sms") != "Merhaba {{first_name}}, {{city}} için fırsat" {
		t.Fatalf("the saved content is not the repaired one: %v", got["content"])
	}
}

func TestStream_failuresEndWithAnErrorEvent(t *testing.T) {
	invalid := llm.Reply(tooLong)
	tests := []struct {
		name string
		step []llm.Step
		code string
	}{
		{"a refusal that arrives mid-stream", []llm.Step{llm.FailAfter(&llm.RefusalError{Category: "cyber"}, `{"sms":"Merha`)}, "llm_refused"},
		{"a rate limit", []llm.Step{llm.Fail(&llm.RateLimitError{RetryAfter: 7 * time.Second})}, "llm_rate_limited"},
		{"the repairs run out", []llm.Step{invalid, invalid, invalid}, "llm_invalid_output"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newEnv(t, append([]llm.Step{llm.Reply(ankaraRule)}, tt.step...)...)
			id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
			_, msgs := e.stream(path(id, "/content/stream"))
			code, _ := lastError(t, msgs)
			if code != tt.code {
				t.Fatalf("error code = %q, want %q", code, tt.code)
			}
			if slices.Contains(eventNames(msgs), "done") {
				t.Fatal("a failed stream also said done")
			}
			// nothing was saved
			if got := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t); got["content"] != nil || got["status"] != "audience_ready" {
				t.Fatalf("a failed generation changed the campaign: %v", got)
			}
		})
	}
}

// EventSource reconnects by itself; that must not start another generation over content the marketer may have edited.
func TestStream_existingContentIsOnlyRegeneratedOnRequest(t *testing.T) {
	e, client := newEnv(t, llm.Reply(ankaraRule), llm.Reply(goodContent), llm.Reply(newer))
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
	e.stream(path(id, "/content/stream"))
	e.expect("PUT", path(id, "/content"), map[string]string{"sms": "Elle yazıldı", "email_subject": "s", "email_body": "b"}, http.StatusOK, "") // version 4
	requests := len(client.Requests())

	// what an automatically reconnecting EventSource does
	for range 3 {
		r := e.expect("GET", path(id, "/content/stream"), nil, http.StatusConflict, "content_exists")
		if got := r.header.Get("Content-Type"); strings.Contains(got, "event-stream") {
			t.Fatalf("Content-Type = %q: the refusal must be a plain JSON error", got)
		}
	}
	e.expect("GET", path(id, "/content/stream?regenerate=false"), nil, http.StatusConflict, "content_exists")
	e.expect("GET", path(id, "/content/stream?regenerate=yes"), nil, http.StatusUnprocessableEntity, "validation_failed")
	if n := len(client.Requests()); n != requests {
		t.Fatalf("a refused stream called the model (%d requests, was %d)", n, requests)
	}
	got := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t)
	if field(t, got, "content", "sms") != "Elle yazıldı" || got["version"] != 4.0 {
		t.Fatalf("the hand edit was touched: %v", got)
	}

	// on request it is generated again, and replaces the content
	_, msgs := e.stream(path(id, "/content/stream?regenerate=true"))
	if msgs[len(msgs)-1].event != "done" {
		t.Fatalf("events = %v", eventNames(msgs))
	}
	got = e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t)
	if field(t, got, "content", "sms") != "Yeni metin {{first_name}}" || got["version"] != 5.0 {
		t.Fatalf("after regenerate=true: %v", got)
	}
}

// When the client closes the connection the model request is canceled, and the campaign can be generated again at once.
func TestStream_aClosedConnectionCancelsTheModelRequest(t *testing.T) {
	gen := newBlockingGen()
	e := newEnvWith(t, gen, llm.NewScripted())
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))

	ctx, cancel := context.WithCancel(t.Context())
	_, msgs := e.openStream(ctx, path(id, "/content/stream"))
	<-gen.started
	cancel() // the browser tab is closed

	select {
	case cause := <-gen.canceled:
		if cause == nil {
			t.Fatal("the model request was canceled without a cause")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the model request was not canceled after the client closed the connection")
	}
	<-msgs

	// the lock and the slot are free again: a new stream starts instead of getting 409 or 503
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	resp, _ := e.openStream(ctx2, path(id, "/content/stream"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a new stream after the cancel = %d, want 200", resp.StatusCode)
	}
	<-gen.started
}

func TestStream_aSecondGenerationOfTheSameCampaignIsAConflict(t *testing.T) {
	gen := newBlockingGen()
	e := newEnvWith(t, gen, llm.NewScripted())
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
	other := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b2"}, http.StatusCreated, ""))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e.openStream(ctx, path(id, "/content/stream"))
	<-gen.started

	e.expect("GET", path(id, "/content/stream"), nil, http.StatusConflict, "generation_in_progress")
	e.expect("POST", path(id, "/segment"), nil, http.StatusConflict, "generation_in_progress")
	// another campaign is not affected
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	if resp, _ := e.openStream(ctx2, path(other, "/content/stream")); resp.StatusCode != http.StatusOK {
		t.Fatalf("the stream of another campaign = %d, want 200", resp.StatusCode)
	}
}

// A hand edit made while the model is writing wins: the generated text is dropped, not saved over it.
func TestStream_aHandEditDuringTheStreamWins(t *testing.T) {
	gen := newBlockingGen()
	e := newEnvWith(t, gen, llm.NewScripted())
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))

	_, msgs := e.openStream(t.Context(), path(id, "/content/stream"))
	<-gen.started
	e.expect("PUT", path(id, "/content"), map[string]string{"sms": "Elle yazıldı", "email_subject": "s", "email_body": "b"}, http.StatusOK, "")
	gen.release <- goodContent

	events := <-msgs
	if code, _ := lastError(t, events); code != "state_changed" {
		t.Fatalf("error code = %q, want state_changed", code)
	}
	got := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t)
	if field(t, got, "content", "sms") != "Elle yazıldı" {
		t.Fatalf("the generated text overwrote the hand edit: %v", got["content"])
	}
}

// Open streams end with an error event when the server shuts down, instead of being cut off.
func TestStream_shutdownEndsOpenStreamsWithAnEvent(t *testing.T) {
	gen := newBlockingGen()
	e := newEnvWith(t, gen, llm.NewScripted())
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))

	_, msgs := e.openStream(t.Context(), path(id, "/content/stream"))
	<-gen.started
	e.server.BeginShutdown()

	select {
	case events := <-msgs:
		if code, _ := lastError(t, events); code != "server_shutting_down" {
			t.Fatalf("error code = %q, want server_shutting_down", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end after the shutdown began")
	}
	select {
	case <-gen.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("the model request was not canceled by the shutdown")
	}
}

// A client that does not read cannot make the model's read loop wait: the generation is canceled instead.
func TestStream_aSlowClientIsCutOff(t *testing.T) {
	gen := newBlockingGen()
	gen.emit = func(onEvent func(compose.Event)) {
		for range 200_000 {
			onEvent(compose.Event{Kind: compose.EventDelta, Field: "sms", Text: strings.Repeat("a", 64)})
		}
	}
	e := newEnvWith(t, gen, llm.NewScripted(), withConfig(func(c *apiConfig) { c.EventBuffer = 1 }))
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))

	_, msgs := e.openStream(t.Context(), path(id, "/content/stream"))
	<-gen.started
	select {
	case cause := <-gen.canceled:
		if cause == nil || !strings.Contains(cause.Error(), "does not read the stream fast enough") {
			t.Fatalf("the generation was canceled with %v, want the slow client", cause)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the generation was not canceled")
	}
	if code, _ := lastError(t, <-msgs); code != "client_too_slow" {
		t.Fatalf("error code = %q, want client_too_slow", code)
	}
}

func TestAPI_theGenerationGateAnswers503WhenFull(t *testing.T) {
	gen := newBlockingGen()
	hold, started := make(chan struct{}), make(chan struct{}, 1)
	gen.segment = func() (compose.SegmentResult, error) {
		started <- struct{}{}
		<-hold
		return compose.New(llm.NewScripted(llm.Reply(ankaraRule))).Segment(context.Background(), "b")
	}
	e := newEnvWith(t, gen, llm.NewScripted(), withConfig(func(c *apiConfig) { c.LLMConcurrency = 1; c.LLMWait = 100 * time.Millisecond }))

	var wg sync.WaitGroup
	var first response
	wg.Go(func() { first = e.do("POST", "/api/campaigns", map[string]string{"brief": "ilk"}) })
	<-started

	r := e.expect("POST", "/api/campaigns", map[string]string{"brief": "ikinci"}, http.StatusServiceUnavailable, "llm_busy")
	if r.header.Get("Retry-After") == "" {
		t.Error("no Retry-After on llm_busy")
	}
	if n := e.count(`SELECT count(*) FROM campaigns`); n != 1 {
		t.Fatalf("%d campaigns, want 1: a refused request must not leave a draft behind", n)
	}
	close(hold)
	wg.Wait()
	if first.status != http.StatusCreated {
		t.Fatalf("the first request = %d\n%s", first.status, first.body)
	}
}

type panicGen struct{}

func (panicGen) Segment(context.Context, string) (compose.SegmentResult, error) {
	panic("boom")
}

func (panicGen) Content(context.Context, compose.ContentInput, func(compose.Event)) (compose.ContentResult, error) {
	panic("boom")
}

func TestAPI_aPanicIsAnInternalErrorInTheCommonShape(t *testing.T) {
	e := newEnvWith(t, panicGen{}, llm.NewScripted())
	r := e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusInternalServerError, "internal")
	_, details := r.errorOf(t)
	if field(t, details, "request_id") == "" || strings.Contains(string(r.body), "boom") {
		t.Fatalf("body = %s, want a request id and not the panic text", r.body)
	}
	e.expect("GET", "/healthz", nil, http.StatusOK, "") // the server went on
}

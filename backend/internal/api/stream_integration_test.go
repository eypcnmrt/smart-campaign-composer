//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.ts.URL+path, nil)
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
		r := e.expect("POST", path(id, "/content/stream"), nil, http.StatusConflict, "content_exists")
		if got := r.header.Get("Content-Type"); strings.Contains(got, "event-stream") {
			t.Fatalf("Content-Type = %q: the refusal must be a plain JSON error", got)
		}
	}
	e.expect("POST", path(id, "/content/stream?regenerate=false"), nil, http.StatusConflict, "content_exists")
	e.expect("POST", path(id, "/content/stream?regenerate=yes"), nil, http.StatusUnprocessableEntity, "validation_failed")
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

	e.expect("POST", path(id, "/content/stream"), nil, http.StatusConflict, "generation_in_progress")
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

// A segment generation is tied to the shutdown signal like the stream: the request that is in flight when the server starts
// shutting down ends with 503 server_shutting_down (and its model request is canceled) while http.Server.Shutdown is still
// waiting for it, instead of running out its two minutes and getting a cut connection. The draft stays and says so.
func TestAPI_shutdownEndsAnOpenSegmentRequest(t *testing.T) {
	for _, tt := range []struct {
		name string
		// request sends the segment request that blocks; it runs after a campaign exists.
		request func(e *env, id int64) response
		// campaigns is how many campaigns exist at the end: the one that was prepared, and the draft a refused create leaves.
		campaigns int
	}{
		{"creating a campaign", func(e *env, _ int64) response {
			return e.do("POST", "/api/campaigns", map[string]string{"brief": "yeni"})
		}, 2},
		{"generating the segment again", func(e *env, id int64) response { return e.do("POST", path(id, "/segment"), nil) }, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gen := newBlockingGen()
			var calls atomic.Int32
			started, canceled := make(chan struct{}, 1), make(chan error, 1)
			gen.segment = func(ctx context.Context) (compose.SegmentResult, error) {
				if calls.Add(1) == 1 { // the first call only prepares a campaign
					return compose.New(llm.NewScripted(llm.Reply(ankaraRule))).Segment(ctx, "hazırlık")
				}
				started <- struct{}{}
				<-ctx.Done()
				canceled <- context.Cause(ctx)
				return compose.SegmentResult{}, ctx.Err()
			}
			e := newEnvWith(t, gen, llm.NewScripted())
			id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "hazırlık"}, http.StatusCreated, ""))

			got := make(chan response, 1)
			go func() { got <- tt.request(e, id) }()
			<-started
			e.server.BeginShutdown()

			var r response
			select {
			case r = <-got:
			case <-time.After(5 * time.Second):
				t.Fatal("the segment request did not end after the shutdown began")
			}
			if r.status != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503\n%s", r.status, r.body)
			}
			if code, details := r.errorOf(t); code != "server_shutting_down" {
				t.Fatalf("error code = %q (details %v), want server_shutting_down", code, details)
			}
			select {
			case cause := <-canceled:
				if cause == nil || !strings.Contains(cause.Error(), "shutting down") {
					t.Fatalf("the model request was canceled with %v, want the shutdown", cause)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the model request was not canceled by the shutdown")
			}
			if n := e.count(`SELECT count(*) FROM campaigns`); n != tt.campaigns {
				t.Fatalf("%d campaigns after the shutdown, want %d", n, tt.campaigns)
			}
			if body := e.expect("GET", "/metrics", nil, http.StatusOK, "").body; !strings.Contains(string(body), `scc_llm_calls_total{outcome="server_shutting_down",task="segment"} 1`) {
				t.Fatalf("the shutdown is not counted as the outcome of the generation:\n%s", grepLines(body, "scc_llm_calls_total"))
			}
		})
	}
}

// After the shutdown has begun a new segment request does not ask the model at all (no credit is spent), whatever the route.
func TestAPI_aSegmentRequestAfterTheShutdownBeganDoesNotCallTheModel(t *testing.T) {
	gen := newBlockingGen()
	var calls atomic.Int32
	gen.segment = func(ctx context.Context) (compose.SegmentResult, error) {
		calls.Add(1)
		return compose.New(llm.NewScripted(llm.Reply(ankaraRule))).Segment(ctx, "b")
	}
	e := newEnvWith(t, gen, llm.NewScripted())
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
	before := calls.Load()

	e.server.BeginShutdown()
	r := e.expect("POST", "/api/campaigns", map[string]string{"brief": "geç kaldı"}, http.StatusServiceUnavailable, "server_shutting_down")
	if _, details := r.errorOf(t); field(t, details, "campaign_id") == nil {
		t.Fatalf("details = %v, want the id of the draft that was left", details)
	}
	e.expect("POST", path(id, "/segment"), nil, http.StatusServiceUnavailable, "server_shutting_down")
	if got := calls.Load(); got != before {
		t.Fatalf("the model was called %d times after the shutdown began", got-before)
	}
}

// grepLines returns the lines of text that contain substr, for failure messages.
func grepLines(text []byte, substr string) string {
	var out []string
	for _, l := range strings.Split(string(text), "\n") {
		if strings.Contains(l, substr) && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
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
	gen.segment = func(context.Context) (compose.SegmentResult, error) {
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

// A HEAD request is answered by the GET handler of the same path, without a body. Nothing a GET does changes anything, and the
// one route that costs money (the content stream) is a POST, so a HEAD (a health probe, a link checker) can never start a
// generation.
func TestHEAD_isAnsweredByTheGETRoutes(t *testing.T) {
	e, _ := newEnv(t, llm.Reply(ankaraRule))
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
	for _, p := range []string{"/healthz", "/readyz", "/api/config", "/api/campaigns", path(id, ""), path(id, "/preview"), path(id, "/stats")} {
		get := e.do("GET", p, nil)
		head := e.do("HEAD", p, nil)
		if head.status != get.status || len(head.body) != 0 {
			t.Errorf("HEAD %s = %d with %d body bytes, want the status of the GET (%d) and none", p, head.status, len(head.body), get.status)
		}
		// a content-less campaign has no preview (409); every other path must be a plain success
		if get.status != http.StatusOK && p != path(id, "/preview") {
			t.Errorf("GET %s = %d, want 200", p, get.status)
		}
	}
	// A GET of the stream is refused and starts nothing: the campaign is as it was.
	for _, m := range []string{"GET", "HEAD"} {
		e.expect(m, path(id, "/content/stream"), nil, http.StatusMethodNotAllowed, "")
	}
	if got := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t); got["content"] != nil || got["status"] != "audience_ready" {
		t.Errorf("campaign after GET and HEAD of the stream = %v, want no content", got)
	}
}

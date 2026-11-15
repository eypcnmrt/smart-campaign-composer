package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"smartcampaign/internal/streamjson"
)

// The tests below run the real SDK against a fake API server (base URL
// override), so the request the SDK builds and the way it reads responses are
// both exercised without a key or a network.

type recordedCall struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   map[string]any
}

type fakeAPI struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []recordedCall
}

// newFakeAPI starts a server that records every request and answers it with
// handle; n is the 1-based number of the call.
func newFakeAPI(t *testing.T, handle func(n int, w http.ResponseWriter, r *http.Request)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("fake API: read request body: %v", err)
		}
		var body map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("fake API: request body is not JSON: %v\n%s", err, raw)
			}
		}
		f.mu.Lock()
		f.calls = append(f.calls, recordedCall{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), body})
		n := len(f.calls)
		f.mu.Unlock()
		handle(n, w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) client(cfg Config) *Anthropic {
	cfg.APIKey = "test-key"
	cfg.BaseURL = f.srv.URL
	return NewAnthropic(cfg)
}

func (f *fakeAPI) recorded() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCall(nil), f.calls...)
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any, headers ...string) {
	t.Helper()
	for i := 0; i+1 < len(headers); i += 2 {
		w.Header().Set(headers[i], headers[i+1])
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("fake API: write response: %v", err)
	}
}

type sseEvent struct {
	name string
	data map[string]any
}

func writeSSE(t *testing.T, w http.ResponseWriter, events ...sseEvent) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)
	for _, e := range events {
		data, err := json.Marshal(e.data)
		if err != nil {
			t.Fatalf("fake API: marshal event: %v", err)
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, data); err != nil {
			return // the client went away, which some tests do on purpose
		}
		if canFlush {
			flusher.Flush()
		}
	}
}

func obj(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// Non-streaming response bodies.

func textBlock(s string) map[string]any { return obj("type", "text", "text", s) }

func fallbackBlock(from, to string) map[string]any {
	return obj("type", "fallback", "from", obj("model", from), "to", obj("model", to), "trigger", obj("type", "refusal", "category", "cyber"))
}

func messageBody(model, stop string, details any, blocks ...any) map[string]any {
	return obj("id", "msg_test", "type", "message", "role", "assistant", "model", model, "content", blocks,
		"stop_reason", stop, "stop_sequence", nil, "stop_details", details,
		"usage", obj("input_tokens", 12, "output_tokens", 34))
}

func apiError(kind, msg string) map[string]any {
	return obj("type", "error", "error", obj("type", kind, "message", msg), "request_id", "req_test")
}

// Streaming events.

func msgStart(model string) sseEvent {
	return sseEvent{"message_start", obj("type", "message_start", "message", obj(
		"id", "msg_test", "type", "message", "role", "assistant", "model", model, "content", []any{},
		"stop_reason", nil, "stop_sequence", nil, "usage", obj("input_tokens", 12, "output_tokens", 1)))}
}

func blockStart(index int, block map[string]any) sseEvent {
	return sseEvent{"content_block_start", obj("type", "content_block_start", "index", index, "content_block", block)}
}

func textDelta(index int, text string) sseEvent {
	return sseEvent{"content_block_delta", obj("type", "content_block_delta", "index", index, "delta", obj("type", "text_delta", "text", text))}
}

func blockStop(index int) sseEvent {
	return sseEvent{"content_block_stop", obj("type", "content_block_stop", "index", index)}
}

func msgDelta(stop string, details any) sseEvent {
	return sseEvent{"message_delta", obj("type", "message_delta",
		"delta", obj("stop_reason", stop, "stop_sequence", nil, "stop_details", details), "usage", obj("output_tokens", 34))}
}

func msgStop() sseEvent { return sseEvent{"message_stop", obj("type", "message_stop")} }

// textStream is the events of one ordinary text block.
func textStream(index int, pieces ...string) []sseEvent {
	events := []sseEvent{blockStart(index, textBlock(""))}
	for _, p := range pieces {
		events = append(events, textDelta(index, p))
	}
	return append(events, blockStop(index))
}

var testSchema = map[string]any{
	"type":                 "object",
	"properties":           map[string]any{"x": map[string]any{"type": "string"}},
	"required":             []any{"x"},
	"additionalProperties": false,
}

const answer = `{"x":"hello"}`

func TestAnthropic_modeAndModel(t *testing.T) {
	t.Parallel()
	c := NewAnthropic(Config{APIKey: "test-key"})
	if c.Mode() != ModeAnthropic || c.Model() != DefaultModel {
		t.Errorf("Mode() = %q, Model() = %q", c.Mode(), c.Model())
	}
	if m := NewAnthropic(Config{APIKey: "test-key", Model: "claude-sonnet-5-5"}).Model(); m != "claude-sonnet-5-5" {
		t.Errorf("Model() = %q", m)
	}
}

func TestAnthropic_Complete(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, messageBody("claude-opus-5-5", "end_turn", nil, textBlock(answer)))
	})
	resp, err := api.client(Config{Fallback: true}).Complete(t.Context(), Request{
		System: "be brief", User: "say hello", Schema: testSchema, Effort: EffortLow,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	want := Response{Text: answer, Model: "claude-opus-5-5", ServedBy: "claude-opus-5-5", InputTokens: 12, OutputTokens: 34}
	if resp != want {
		t.Errorf("Complete() = %+v, want %+v", resp, want)
	}

	calls := api.recorded()
	if len(calls) != 1 {
		t.Fatalf("%d requests, want 1", len(calls))
	}
	call := calls[0]
	if call.Method != http.MethodPost || call.Path != "/v1/messages" {
		t.Errorf("request = %s %s", call.Method, call.Path)
	}
	// The v2 SDK sends the key as a bearer token.
	if got := call.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q, want the explicit key and nothing from the environment", got)
	}
	if got := call.Header.Get("Anthropic-Beta"); !strings.Contains(got, "server-side-fallback-2026-07-01") {
		t.Errorf("anthropic-beta = %q, want the server-side fallback beta", got)
	}

	body := call.Body
	if body["model"] != DefaultModel {
		t.Errorf("model = %v, want the default %s", body["model"], DefaultModel)
	}
	if body["max_tokens"] != float64(16000) {
		t.Errorf("max_tokens = %v, want 16000: thinking tokens count toward it, so it must not be small", body["max_tokens"])
	}
	if body["fallbacks"] != "default" {
		t.Errorf("fallbacks = %v, want the default routing", body["fallbacks"])
	}
	for _, key := range []string{"thinking", "temperature", "top_p", "top_k", "tool_choice", "tools", "stream"} {
		if _, present := body[key]; present {
			t.Errorf("request sets %q; Claude Opus 5.5 rejects or does not need it", key)
		}
	}
	system, _ := body["system"].([]any)
	if len(system) != 1 || system[0].(map[string]any)["text"] != "be brief" {
		t.Errorf("system = %v", body["system"])
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %v, want a single user message (no prefill, no history)", body["messages"])
	}
	first := messages[0].(map[string]any)
	if first["role"] != "user" || first["content"].([]any)[0].(map[string]any)["text"] != "say hello" {
		t.Errorf("message = %v", first)
	}

	output, _ := body["output_config"].(map[string]any)
	if output["effort"] != "low" {
		t.Errorf("output_config.effort = %v, want low", output["effort"])
	}
	format, _ := output["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Errorf("output_config.format.type = %v", format["type"])
	}
	var wantSchema any
	if err := json.Unmarshal(mustJSONBytes(t, testSchema), &wantSchema); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(format["schema"], wantSchema) {
		t.Errorf("output_config.format.schema = %v, want %v", format["schema"], wantSchema)
	}
}

func mustJSONBytes(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAnthropic_Complete_requestOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		cfg          Config
		req          Request
		wantMax      float64
		wantEffort   any
		wantFallback bool
		wantSystem   bool
		wantFormat   bool
	}{
		{name: "fallback off", cfg: Config{Fallback: false}, req: Request{User: "u"}, wantMax: 16000},
		{name: "explicit max tokens and medium effort", cfg: Config{Fallback: true}, req: Request{User: "u", MaxTokens: 5000, Effort: EffortMedium}, wantMax: 5000, wantEffort: "medium", wantFallback: true},
		{name: "system and schema", cfg: Config{}, req: Request{User: "u", System: "s", Schema: testSchema}, wantMax: 16000, wantSystem: true, wantFormat: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, 200, messageBody("claude-opus-5-5", "end_turn", nil, textBlock(answer)))
			})
			if _, err := api.client(tt.cfg).Complete(t.Context(), tt.req); err != nil {
				t.Fatalf("Complete() error = %v", err)
			}
			call := api.recorded()[0]
			if call.Body["max_tokens"] != tt.wantMax {
				t.Errorf("max_tokens = %v, want %v", call.Body["max_tokens"], tt.wantMax)
			}
			_, hasFallbacks := call.Body["fallbacks"]
			hasBeta := strings.Contains(call.Header.Get("Anthropic-Beta"), "server-side-fallback")
			if hasFallbacks != tt.wantFallback || hasBeta != tt.wantFallback {
				t.Errorf("fallbacks present = %v, beta header present = %v, want both %v", hasFallbacks, hasBeta, tt.wantFallback)
			}
			output, hasOutput := call.Body["output_config"].(map[string]any)
			if got := output["effort"]; got != tt.wantEffort {
				t.Errorf("effort = %v, want %v", got, tt.wantEffort)
			}
			if _, hasFormat := output["format"]; hasFormat != tt.wantFormat {
				t.Errorf("output_config present = %v, format present = %v, want format %v", hasOutput, hasFormat, tt.wantFormat)
			}
			if _, hasSystem := call.Body["system"]; hasSystem != tt.wantSystem {
				t.Errorf("system present = %v, want %v", hasSystem, tt.wantSystem)
			}
		})
	}
}

func TestAnthropic_Complete_stopReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     map[string]any
		wantErr  error
		wantText string
	}{
		{
			name:    "refusal with details",
			body:    messageBody("claude-opus-5-5", "refusal", obj("type", "refusal", "category", "cyber", "explanation", "not allowed"), textBlock("partial")),
			wantErr: ErrRefusal,
		},
		{
			name:    "refusal without details",
			body:    messageBody("claude-opus-5-5", "refusal", nil),
			wantErr: ErrRefusal,
		},
		{name: "max tokens", body: messageBody("claude-opus-5-5", "max_tokens", nil, textBlock(`{"x":"cut`)), wantErr: ErrMaxTokens},
		{name: "context window", body: messageBody("claude-opus-5-5", "model_context_window_exceeded", nil, textBlock(`{`)), wantErr: ErrMaxTokens},
		{name: "pause turn", body: messageBody("claude-opus-5-5", "pause_turn", nil, textBlock(`{`)), wantErr: ErrUnexpectedStop},
		{name: "tool use", body: messageBody("claude-opus-5-5", "tool_use", nil), wantErr: ErrUnexpectedStop},
		{name: "end turn", body: messageBody("claude-opus-5-5", "end_turn", nil, textBlock(answer)), wantText: answer},
		{name: "stop sequence", body: messageBody("claude-opus-5-5", "stop_sequence", nil, textBlock(answer)), wantText: answer},
		{
			name: "thinking blocks are not part of the text",
			body: messageBody("claude-opus-5-5", "end_turn", nil,
				obj("type", "thinking", "thinking", "secret reasoning", "signature", "sig"), textBlock(answer)),
			wantText: answer,
		},
		{
			name: "several text blocks are joined",
			body: messageBody("claude-opus-5-5", "end_turn", nil, textBlock(`{"x":`), textBlock(`"hello"}`)),

			wantText: answer,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) { writeJSON(t, w, 200, tt.body) })
			resp, err := api.client(Config{}).Complete(t.Context(), Request{User: "u"})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Complete() error = %v, want %v", err, tt.wantErr)
				}
				if resp.Text != "" {
					t.Errorf("Text = %q on failure; a cut-off or refused answer must not be returned", resp.Text)
				}
				return
			}
			if err != nil || resp.Text != tt.wantText {
				t.Errorf("Complete() = %q, %v; want %q", resp.Text, err, tt.wantText)
			}
		})
	}
}

// A refused or cut-off answer is billed, so its tokens must reach the caller even though its text must not.
func TestAnthropic_Complete_failedAnswerKeepsItsUsage(t *testing.T) {
	t.Parallel()
	for _, stop := range []string{"max_tokens", "refusal"} {
		t.Run(stop, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, 200, messageBody("claude-opus-5-5", stop, nil, textBlock("partial")))
			})
			resp, err := api.client(Config{}).Complete(t.Context(), Request{User: "u"})
			if err == nil {
				t.Fatal("no error")
			}
			if resp.Text != "" || resp.InputTokens != 12 || resp.OutputTokens != 34 || resp.Model != "claude-opus-5-5" {
				t.Errorf("Response = %+v, want the usage (12 in, 34 out) and no text", resp)
			}
		})
	}
}

func TestAnthropic_Complete_refusalDetails(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, messageBody("claude-opus-5-5", "refusal", obj("type", "refusal", "category", "cyber", "explanation", "not allowed")))
	})
	_, err := api.client(Config{Fallback: true}).Complete(t.Context(), Request{User: "u"})
	refusal, ok := errors.AsType[*RefusalError](err)
	if !ok {
		t.Fatalf("Complete() error = %v, want *RefusalError", err)
	}
	if refusal.Category != "cyber" || refusal.Explanation != "not allowed" {
		t.Errorf("refusal = %+v", refusal)
	}
	if n := len(api.recorded()); n != 1 {
		t.Errorf("%d requests; a refusal must not be retried", n)
	}
}

// A request that a classifier declined can be answered by the fallback model
// inside the same call; the reply then has a fallback block in between.
func TestAnthropic_Complete_fallbackServed(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, messageBody("claude-opus-4-8", "end_turn", nil,
			fallbackBlock("claude-opus-5-5", "claude-opus-4-8"), textBlock(answer)))
	})
	resp, err := api.client(Config{Fallback: true}).Complete(t.Context(), Request{User: "u"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.Text != answer || !resp.FallbackUsed || resp.ServedBy != "claude-opus-4-8" {
		t.Errorf("Complete() = %+v", resp)
	}
}

func TestAnthropic_Complete_httpErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		status    int
		kind      string
		headers   []string
		retries   int
		wantErr   error
		wantCalls int
	}{
		{name: "bad request", status: 400, kind: "invalid_request_error", wantErr: ErrBadRequest, wantCalls: 1},
		{name: "unauthorized", status: 401, kind: "authentication_error", wantErr: ErrBadRequest, wantCalls: 1},
		{name: "forbidden", status: 403, kind: "permission_error", wantErr: ErrBadRequest, wantCalls: 1},
		{name: "unknown model", status: 404, kind: "not_found_error", wantErr: ErrBadRequest, wantCalls: 1},
		{name: "too large", status: 413, kind: "request_too_large", wantErr: ErrBadRequest, wantCalls: 1},
		// 5xx is retried by the SDK (two retries by default) before it is reported.
		{name: "server error", status: 500, kind: "api_error", headers: []string{"Retry-After-Ms", "1"}, wantErr: ErrUnavailable, wantCalls: 3},
		{name: "overloaded", status: 529, kind: "overloaded_error", headers: []string{"Retry-After-Ms", "1"}, wantErr: ErrUnavailable, wantCalls: 3},
		{name: "server error without retries", status: 500, kind: "api_error", retries: -1, wantErr: ErrUnavailable, wantCalls: 1},
		{name: "request timeout", status: 408, kind: "timeout_error", retries: -1, wantErr: ErrTimeout, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, tt.status, apiError(tt.kind, "boom"), tt.headers...)
			})
			_, err := api.client(Config{MaxRetries: tt.retries}).Complete(t.Context(), Request{User: "u"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Complete() error = %v, want %v", err, tt.wantErr)
			}
			if n := len(api.recorded()); n != tt.wantCalls {
				t.Errorf("%d requests, want %d", n, tt.wantCalls)
			}
			for _, other := range []error{ErrRefusal, ErrMaxTokens, ErrRateLimited} {
				if errors.Is(err, other) {
					t.Errorf("error also matches %v", other)
				}
			}
		})
	}
}

func TestAnthropic_Complete_rateLimit(t *testing.T) {
	t.Parallel()

	t.Run("the SDK waits for retry-after and retries", func(t *testing.T) {
		t.Parallel()
		api := newFakeAPI(t, func(n int, w http.ResponseWriter, _ *http.Request) {
			if n == 1 {
				writeJSON(t, w, 429, apiError("rate_limit_error", "slow down"), "Retry-After-Ms", "1")
				return
			}
			writeJSON(t, w, 200, messageBody("claude-opus-5-5", "end_turn", nil, textBlock(answer)))
		})
		resp, err := api.client(Config{}).Complete(t.Context(), Request{User: "u"})
		if err != nil || resp.Text != answer {
			t.Fatalf("Complete() = %q, %v", resp.Text, err)
		}
		if n := len(api.recorded()); n != 2 {
			t.Errorf("%d requests, want 2 (the 429 and the retry)", n)
		}
	})

	t.Run("persistent 429 is reported with the wait the provider asked for", func(t *testing.T) {
		t.Parallel()
		api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, 429, apiError("rate_limit_error", "slow down"), "Retry-After", "7")
		})
		_, err := api.client(Config{MaxRetries: -1}).Complete(t.Context(), Request{User: "u"})
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("Complete() error = %v, want ErrRateLimited", err)
		}
		limit, ok := errors.AsType[*RateLimitError](err)
		if !ok || limit.RetryAfter != 7*time.Second {
			t.Errorf("RateLimitError = %+v, want RetryAfter 7s", limit)
		}
		if n := len(api.recorded()); n != 1 {
			t.Errorf("%d requests, want 1 with retries off", n)
		}
	})

	t.Run("persistent 429 after the SDK retries", func(t *testing.T) {
		t.Parallel()
		api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, 429, apiError("rate_limit_error", "slow down"), "Retry-After-Ms", "1")
		})
		_, err := api.client(Config{}).Complete(t.Context(), Request{User: "u"})
		limit, ok := errors.AsType[*RateLimitError](err)
		if !ok || limit.RetryAfter != time.Millisecond {
			t.Fatalf("Complete() error = %v, want RateLimitError with 1ms", err)
		}
		if n := len(api.recorded()); n != 3 {
			t.Errorf("%d requests, want 3 (first try and two SDK retries)", n)
		}
	})

	t.Run("429 without a header", func(t *testing.T) {
		t.Parallel()
		api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, 429, apiError("rate_limit_error", "slow down"))
		})
		_, err := api.client(Config{MaxRetries: -1}).Complete(t.Context(), Request{User: "u"})
		limit, ok := errors.AsType[*RateLimitError](err)
		if !ok || limit.RetryAfter != 0 {
			t.Errorf("Complete() error = %v, want RateLimitError with no wait", err)
		}
	})
}

func TestAnthropic_Complete_timeout(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(_ int, _ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	start := time.Now()
	_, err := api.client(Config{CompleteTimeout: 100 * time.Millisecond}).Complete(t.Context(), Request{User: "u"})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Complete() error = %v, want ErrTimeout", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Error("a timeout must not be reported as unavailable")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s; the SDK must not keep retrying after the deadline", elapsed)
	}
	if n := len(api.recorded()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

func TestAnthropic_Complete_callerCancels(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	api := newFakeAPI(t, func(_ int, _ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	})
	_, err := api.client(Config{}).Complete(ctx, Request{User: "u"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete() error = %v, want context.Canceled", err)
	}
	for _, other := range []error{ErrTimeout, ErrUnavailable} {
		if errors.Is(err, other) {
			t.Errorf("a caller cancel is also reported as %v", other)
		}
	}
}

func TestAnthropic_Complete_connectionRefused(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(int, http.ResponseWriter, *http.Request) {})
	c := api.client(Config{MaxRetries: -1})
	api.srv.Close()
	_, err := c.Complete(t.Context(), Request{User: "u"})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("Complete() error = %v, want ErrUnavailable", err)
	}
}

func TestAnthropic_Stream(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		events := []sseEvent{msgStart("claude-opus-5-5")}
		events = append(events, textStream(0, `{"x":`, `"hel`, `lo"}`)...)
		events = append(events, msgDelta("end_turn", nil), msgStop())
		writeSSE(t, w, events...)
	})
	var pieces []string
	resp, err := api.client(Config{Fallback: true}).Stream(t.Context(),
		Request{System: "s", User: "u", Schema: testSchema, Effort: EffortMedium},
		func(s string) { pieces = append(pieces, s) })
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if got := strings.Join(pieces, ""); got != answer || resp.Text != answer {
		t.Errorf("streamed %q, response %q, want %q", got, resp.Text, answer)
	}
	if len(pieces) != 3 {
		t.Errorf("onText got %d pieces %q, want the 3 deltas as sent", len(pieces), pieces)
	}
	if resp.Model != "claude-opus-5-5" || resp.FallbackUsed {
		t.Errorf("resp = %+v", resp)
	}

	body := api.recorded()[0].Body
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	if body["max_tokens"] != float64(64000) {
		t.Errorf("max_tokens = %v, want 64000 for a streamed request", body["max_tokens"])
	}
	if output, _ := body["output_config"].(map[string]any); output["effort"] != "medium" {
		t.Errorf("output_config = %v", body["output_config"])
	}
	if _, present := body["thinking"]; present {
		t.Error("request sets thinking")
	}
}

func TestAnthropic_Stream_nilCallbackAndThinking(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		events := []sseEvent{msgStart("claude-opus-5-5"),
			blockStart(0, obj("type", "thinking", "thinking", "", "signature", "")),
			sseEvent{"content_block_delta", obj("type", "content_block_delta", "index", 0, "delta", obj("type", "thinking_delta", "thinking", "hmm"))},
			blockStop(0)}
		events = append(events, textStream(1, answer)...)
		events = append(events, msgDelta("end_turn", nil), msgStop())
		writeSSE(t, w, events...)
	})
	resp, err := api.client(Config{}).Stream(t.Context(), Request{User: "u"}, nil)
	if err != nil || resp.Text != answer {
		t.Errorf("Stream() = %q, %v", resp.Text, err)
	}

	var pieces []string
	if _, err := api.client(Config{}).Stream(t.Context(), Request{User: "u"}, func(s string) { pieces = append(pieces, s) }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(pieces, "") != answer {
		t.Errorf("thinking text leaked into the stream: %q", pieces)
	}
}

// The case the streaming path has to survive: a safety classifier stops the
// model after it has already streamed part of the JSON, the fallback model
// takes over, and its text arrives in a new block. The pieces together must be
// the complete JSON, even though the cut falls inside an escape sequence, and
// the extractor that feeds the UI must not be confused by it.
func TestAnthropic_Stream_refusalMidStreamThenFallback(t *testing.T) {
	t.Parallel()
	const (
		partial = `{"sms":"Merhaba {{first_name}}, %30 indirim \u00`
		rest    = `e7ok iyi!","email_subject":"Fırsat \"özel\"","email_body":"Selam\n{{city}} için"}`
		full    = partial + rest
	)
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		events := []sseEvent{msgStart("claude-opus-5-5")}
		events = append(events, textStream(0, partial[:20], partial[20:])...) // first model, cut off mid-escape
		events = append(events, blockStart(1, fallbackBlock("claude-opus-5-5", "claude-opus-4-8")), blockStop(1))
		events = append(events, textStream(2, rest[:10], rest[10:40], rest[40:])...) // fallback model continues
		events = append(events, msgDelta("end_turn", nil), msgStop())
		writeSSE(t, w, events...)
	})

	extractor := streamjson.New("sms", "email_subject", "email_body")
	fields := map[string]string{}
	var pieces []string
	resp, err := api.client(Config{Fallback: true}).Stream(t.Context(), Request{User: "u"}, func(s string) {
		pieces = append(pieces, s)
		for _, d := range extractor.Write(s) {
			fields[d.Field] += d.Text
		}
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if got := strings.Join(pieces, ""); got != full {
		t.Errorf("streamed text = %q\nwant %q", got, full)
	}
	if resp.Text != full {
		t.Errorf("Response.Text = %q\nwant %q", resp.Text, full)
	}
	if !json.Valid([]byte(resp.Text)) {
		t.Errorf("the blocks together are not valid JSON: %s", resp.Text)
	}
	if !resp.FallbackUsed || resp.ServedBy != "claude-opus-4-8" {
		t.Errorf("FallbackUsed = %v, ServedBy = %q; want the fallback model recorded", resp.FallbackUsed, resp.ServedBy)
	}

	var want map[string]string
	if err := json.Unmarshal([]byte(resp.Text), &want); err != nil {
		t.Fatal(err)
	}
	if extractor.Err() != nil || !extractor.Done() {
		t.Errorf("extractor Err() = %v, Done() = %v", extractor.Err(), extractor.Done())
	}
	for field, text := range want {
		if fields[field] != text {
			t.Errorf("extracted %s = %q, want %q", field, fields[field], text)
		}
	}
	if want["sms"] != "Merhaba {{first_name}}, %30 indirim çok iyi!" {
		t.Errorf("sms = %q: the escape split across the fallback boundary was not rejoined", want["sms"])
	}
}

// Without a usable fallback the refusal is final: the partial text that was
// already streamed is not an answer and the call fails.
func TestAnthropic_Stream_refusalMidStream(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		events := []sseEvent{msgStart("claude-opus-5-5")}
		events = append(events, textStream(0, `{"sms":"Merha`, `ba`)...)
		events = append(events, msgDelta("refusal", obj("type", "refusal", "category", "bio", "explanation", "declined")), msgStop())
		writeSSE(t, w, events...)
	})
	var pieces []string
	resp, err := api.client(Config{Fallback: true}).Stream(t.Context(), Request{User: "u"}, func(s string) { pieces = append(pieces, s) })
	refusal, ok := errors.AsType[*RefusalError](err)
	if !ok || !errors.Is(err, ErrRefusal) {
		t.Fatalf("Stream() error = %v, want a RefusalError", err)
	}
	if refusal.Category != "bio" {
		t.Errorf("category = %q", refusal.Category)
	}
	if resp.Text != "" {
		t.Errorf("Text = %q on a refusal; the partial answer must be discarded", resp.Text)
	}
	if strings.Join(pieces, "") != `{"sms":"Merhaba` {
		t.Errorf("onText received %q; the caller sees the partial text before the error and must drop it", pieces)
	}
}

func TestAnthropic_Stream_stopReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		stop    string
		wantErr error
	}{
		{"max tokens", "max_tokens", ErrMaxTokens},
		{"pause turn", "pause_turn", ErrUnexpectedStop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
				events := []sseEvent{msgStart("claude-opus-5-5")}
				events = append(events, textStream(0, `{"x":"cut`)...)
				events = append(events, msgDelta(tt.stop, nil), msgStop())
				writeSSE(t, w, events...)
			})
			resp, err := api.client(Config{}).Stream(t.Context(), Request{User: "u"}, nil)
			if !errors.Is(err, tt.wantErr) || resp.Text != "" {
				t.Errorf("Stream() = %q, %v; want %v and no text", resp.Text, err, tt.wantErr)
			}
		})
	}
}

func TestAnthropic_Stream_httpErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		status    int
		kind      string
		headers   []string
		wantErr   error
		wantCalls int
	}{
		{name: "rate limited", status: 429, kind: "rate_limit_error", headers: []string{"Retry-After-Ms", "1"}, wantErr: ErrRateLimited, wantCalls: 3},
		{name: "server error", status: 500, kind: "api_error", headers: []string{"Retry-After-Ms", "1"}, wantErr: ErrUnavailable, wantCalls: 3},
		{name: "bad request", status: 400, kind: "invalid_request_error", wantErr: ErrBadRequest, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, tt.status, apiError(tt.kind, "boom"), tt.headers...)
			})
			called := false
			_, err := api.client(Config{}).Stream(t.Context(), Request{User: "u"}, func(string) { called = true })
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Stream() error = %v, want %v", err, tt.wantErr)
			}
			if called {
				t.Error("onText called although no stream was opened")
			}
			if n := len(api.recorded()); n != tt.wantCalls {
				t.Errorf("%d requests, want %d", n, tt.wantCalls)
			}
		})
	}
}

func TestAnthropic_Stream_errorEvent(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		events := []sseEvent{msgStart("claude-opus-5-5")}
		events = append(events, textStream(0, `{"x":`)...)
		events = append(events, sseEvent{"error", apiError("overloaded_error", "Overloaded")})
		writeSSE(t, w, events...)
	})
	resp, err := api.client(Config{}).Stream(t.Context(), Request{User: "u"}, nil)
	if err == nil || resp.Text != "" {
		t.Fatalf("Stream() = %q, %v; want an error and no text", resp.Text, err)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("Stream() error = %v, want ErrUnavailable", err)
	}
}

func TestAnthropic_Stream_timeout(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		// Open the stream, then go quiet: the whole-call deadline must still fire.
		writeSSE(t, w, msgStart("claude-opus-5-5"))
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	start := time.Now()
	_, err := api.client(Config{StreamTimeout: 150 * time.Millisecond}).Stream(t.Context(), Request{User: "u"}, nil)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Stream() error = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s", elapsed)
	}
}

func TestMapError_plainErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		err     error
		wantIs  error
		wantNot []error
	}{
		{"deadline", context.DeadlineExceeded, ErrTimeout, []error{ErrUnavailable}},
		{"wrapped deadline", fmt.Errorf("Post: %w", context.DeadlineExceeded), ErrTimeout, []error{ErrUnavailable}},
		{"canceled", context.Canceled, context.Canceled, []error{ErrTimeout, ErrUnavailable}},
		{"other", errors.New("connection reset by peer"), ErrUnavailable, []error{ErrTimeout}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mapError(tt.err)
			if !errors.Is(got, tt.wantIs) {
				t.Errorf("mapError() = %v, want %v", got, tt.wantIs)
			}
			for _, not := range tt.wantNot {
				if errors.Is(got, not) {
					t.Errorf("mapError() = %v, must not match %v", got, not)
				}
			}
			if !errors.Is(got, tt.err) {
				t.Errorf("mapError() lost the original error from the chain")
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	resp := func(kv ...string) *http.Response {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return &http.Response{Header: h}
	}
	tests := []struct {
		name string
		resp *http.Response
		min  time.Duration
		max  time.Duration
	}{
		{"nil response", nil, 0, 0},
		{"no headers", resp(), 0, 0},
		{"seconds", resp("Retry-After", "3"), 3 * time.Second, 3 * time.Second},
		{"fractional seconds", resp("Retry-After", "1.5"), 1500 * time.Millisecond, 1500 * time.Millisecond},
		{"milliseconds", resp("Retry-After-Ms", "250"), 250 * time.Millisecond, 250 * time.Millisecond},
		{"milliseconds win", resp("Retry-After-Ms", "250", "Retry-After", "9"), 250 * time.Millisecond, 250 * time.Millisecond},
		{"http date", resp("Retry-After", time.Now().Add(30*time.Second).UTC().Format(http.TimeFormat)), 25 * time.Second, 30 * time.Second},
		{"date in the past", resp("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)), 0, 0},
		{"garbage", resp("Retry-After", "soon"), 0, 0},
		{"zero", resp("Retry-After", "0"), 0, 0},
		{"negative", resp("Retry-After", "-5"), 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := retryAfter(tt.resp); got < tt.min || got > tt.max {
				t.Errorf("retryAfter() = %s, want between %s and %s", got, tt.min, tt.max)
			}
		})
	}
}

func TestResult_streamEndedWithoutStopReason(t *testing.T) {
	t.Parallel()
	_, err := result(&anthropic.BetaMessage{})
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrUnexpectedStop) {
		t.Fatalf("error = %v, want ErrUnavailable (a cut connection), not a model decision", err)
	}
}

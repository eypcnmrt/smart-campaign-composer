// Package llm is the boundary to the language model. The rest of the backend
// talks to the Client interface and never to a provider SDK, so the real
// Anthropic client and the mocks are interchangeable and tests run without an
// API key.
//
// A Client does one thing: send one request and return the text, or a typed
// error. It does not validate or repair the output; that is the job of the
// layer above (package compose).
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Mode says which implementation is active, for the "Mock LLM" badge in the UI.
type Mode string

const (
	ModeAnthropic Mode = "anthropic"
	ModeMock      Mode = "mock"
)

// DefaultModel is used when ANTHROPIC_MODEL is not set.
const DefaultModel = "claude-opus-5-5"

// Task names what a request is for. Real providers ignore it; the demo mock
// uses it to decide what to answer.
type Task string

const (
	TaskSegment Task = "segment"
	TaskContent Task = "content"
)

// Effort is how much the model should think. It is sent as output_config.effort.
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
)

// Request is one single-turn call. There is no conversation history: a repair
// is a new request that carries the previous answer in User, which keeps every
// call independent of earlier model output (see DECISIONS.md).
type Request struct {
	Task      Task
	System    string
	User      string
	Schema    map[string]any // JSON schema the answer must follow (structured outputs)
	Effort    Effort
	MaxTokens int64 // thinking tokens count toward this; do not set it low

	// Brief is the marketer's text. Real providers get it inside User; the
	// demo mock reads it from here so it does not depend on the prompt layout.
	Brief string
}

// Response is the complete answer of a successful call.
type Response struct {
	Text         string // all text blocks joined; valid JSON when the schema was followed
	Model        string // model that produced the message
	ServedBy     string // model that wrote the final part; differs from Model after a fallback
	FallbackUsed bool   // a fallback model continued or replaced the answer
	InputTokens  int64
	OutputTokens int64
}

// Client sends requests to a model.
type Client interface {
	// Complete returns the whole answer.
	Complete(ctx context.Context, req Request) (Response, error)
	// Stream calls onText for every piece of text as it arrives and returns the
	// whole answer at the end. If an error is returned after onText was called,
	// the text that was already delivered is not part of any result and must be
	// discarded (a refusal can arrive mid-stream).
	Stream(ctx context.Context, req Request, onText func(string)) (Response, error)
	Mode() Mode
	Model() string
}

// Errors a Client returns. Match them with errors.Is; RefusalError and
// RateLimitError carry details and are found with errors.AsType.
var (
	// ErrRefusal: the model declined (stop_reason "refusal"). Retrying the same
	// request does not help, and the repair loop does not retry it.
	ErrRefusal = errors.New("the model refused the request")
	// ErrMaxTokens: the answer was cut off at max_tokens.
	ErrMaxTokens = errors.New("the answer was cut off at max_tokens")
	// ErrTimeout: the call took longer than the client's timeout.
	ErrTimeout = errors.New("the model request timed out")
	// ErrRateLimited: the provider returned 429 after the SDK's own retries.
	ErrRateLimited = errors.New("the model provider rate-limited the request")
	// ErrUnavailable: a 5xx response or a connection failure after the SDK's retries.
	ErrUnavailable = errors.New("the model provider is unavailable")
	// ErrBadRequest: the provider rejected the request (4xx other than 429).
	// Retrying will not help; it usually means a wrong model name or API key.
	ErrBadRequest = errors.New("the model provider rejected the request")
	// ErrUnexpectedStop: the model stopped for a reason this application does not use.
	ErrUnexpectedStop = errors.New("the model stopped for an unexpected reason")
)

// RefusalError carries what the provider said about a refusal.
type RefusalError struct {
	Category    string // e.g. "cyber", "bio"; empty when the provider gave none
	Explanation string
}

func (e *RefusalError) Error() string {
	msg := ErrRefusal.Error()
	if e.Category != "" {
		msg += " (category " + e.Category + ")"
	}
	return msg
}

func (e *RefusalError) Is(target error) bool { return target == ErrRefusal }

// RateLimitError carries the wait the provider asked for, zero if it sent none.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s (retry after %s)", ErrRateLimited, e.RetryAfter)
	}
	return ErrRateLimited.Error()
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// FromEnv picks the implementation from the environment, read through getenv
// (pass os.Getenv). Without ANTHROPIC_API_KEY the demo mock is returned and
// Mode() reports ModeMock; no other credential source is consulted, so a
// machine that happens to be logged in elsewhere cannot spend money by accident.
//
//	ANTHROPIC_API_KEY   the key; empty or unset selects the mock
//	ANTHROPIC_MODEL     model name, default claude-opus-5-5
//	ANTHROPIC_FALLBACK  "off" disables the server-side refusal fallback
func FromEnv(getenv func(string) string) Client {
	model := strings.TrimSpace(getenv("ANTHROPIC_MODEL"))
	if model == "" {
		model = DefaultModel
	}
	key := strings.TrimSpace(getenv("ANTHROPIC_API_KEY"))
	if key == "" {
		return NewMock(MockConfig{ChunkDelay: 15 * time.Millisecond})
	}
	return NewAnthropic(Config{
		APIKey:   key,
		Model:    model,
		Fallback: !strings.EqualFold(strings.TrimSpace(getenv("ANTHROPIC_FALLBACK")), "off"),
	})
}

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
	// A Client that returns an error may still return a Response with the model and the tokens (never text): a refused
	// or cut-off answer is billed, and Metered counts it.
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

// Names of the environment variables FromEnv reads.
const (
	EnvMode     = "LLM_MODE"
	EnvAPIKey   = "ANTHROPIC_API_KEY"
	EnvModel    = "ANTHROPIC_MODEL"
	EnvFallback = "ANTHROPIC_FALLBACK"
)

// Values of LLM_MODE. "auto" is the default.
const (
	ModeSettingAuto      = "auto"
	ModeSettingAnthropic = "anthropic"
	ModeSettingMock      = "mock"
)

// ErrInvalidMode means LLM_MODE is none of auto, anthropic and mock.
var ErrInvalidMode = errors.New(EnvMode + " must be auto, anthropic or mock")

// ErrNoAPIKey means LLM_MODE=anthropic was asked for but there is no API key. It is an error, not a silent switch to the
// mock: the person who set the mode wants the real model, and a demo that looks like a working system is the worst answer.
var ErrNoAPIKey = errors.New(EnvMode + "=anthropic needs " + EnvAPIKey)

// Selection is the client FromEnv chose and why, for the start-up log.
type Selection struct {
	Client Client
	// Setting is LLM_MODE as configured: auto, anthropic or mock.
	Setting string
	// Reason says in one sentence why this client was chosen. It never contains the key.
	Reason string
}

// FromEnv picks the implementation from the environment, read through getenv (pass os.Getenv).
//
//	LLM_MODE            auto (default), anthropic or mock
//	ANTHROPIC_API_KEY   the key
//	ANTHROPIC_MODEL     model name, default claude-opus-5-5
//	ANTHROPIC_FALLBACK  "off" disables the server-side refusal fallback
//
// auto uses the real client when a key is set and the demo mock otherwise; anthropic needs the key and fails with
// ErrNoAPIKey without it; mock never calls the real API, even when a key is set. No other credential source is
// consulted, so a machine that happens to be logged in elsewhere cannot spend money by accident.
func FromEnv(getenv func(string) string) (Selection, error) {
	setting := strings.ToLower(strings.TrimSpace(getenv(EnvMode)))
	if setting == "" {
		setting = ModeSettingAuto
	}
	key := strings.TrimSpace(getenv(EnvAPIKey))

	sel := Selection{Setting: setting}
	switch setting {
	case ModeSettingAuto:
		if key == "" {
			sel.Reason = EnvMode + "=auto and no " + EnvAPIKey + " is set, so the demo mock is used"
			sel.Client = newDemoMock()
		} else {
			sel.Reason = EnvMode + "=auto and " + EnvAPIKey + " is set"
		}
	case ModeSettingAnthropic:
		if key == "" {
			return Selection{}, ErrNoAPIKey
		}
		sel.Reason = EnvMode + "=anthropic"
	case ModeSettingMock:
		sel.Reason = EnvMode + "=mock"
		if key != "" {
			sel.Reason += "; the set " + EnvAPIKey + " is ignored"
		}
		sel.Client = newDemoMock()
	default:
		return Selection{}, ErrInvalidMode // the value is not echoed: a key pasted into the wrong variable must not reach a log
	}

	if sel.Client == nil {
		model := strings.TrimSpace(getenv(EnvModel))
		if model == "" {
			model = DefaultModel
		}
		sel.Client = NewAnthropic(Config{APIKey: key, Model: model, Fallback: fallbackEnabled(getenv(EnvFallback))})
	}
	return sel, nil
}

// DemoChunkDelay is the pause between the pieces of the demo mock's stream. A demo content (about 47 pieces) then takes three to
// four seconds, long enough to see the fields fill in; at 15 ms it was over in under a second. Only the demo mock waits: the
// tests build their mocks with a zero delay.
const DemoChunkDelay = 75 * time.Millisecond

func newDemoMock() Client { return NewMock(MockConfig{ChunkDelay: DemoChunkDelay}) }

// fallbackEnabled reads ANTHROPIC_FALLBACK. Empty and the usual spellings of "yes" keep the server-side
// refusal fallback on (the documented default). Any other value, "off" and also a typo, turns it off: a
// misspelt setting must not silently keep sending prompts to a model other than the configured one.
func fallbackEnabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "on", "true", "1", "yes":
		return true
	}
	return false
}

package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Defaults. Thinking tokens count toward max_tokens, so the limits are
// generous: a cut-off answer costs a whole retry, a large limit costs nothing
// unless the model actually uses it. Streaming is used for the large one so a
// long answer does not hit an HTTP timeout.
const (
	defaultCompleteMaxTokens = 16000
	defaultStreamMaxTokens   = 64000
	defaultCompleteTimeout   = 90 * time.Second
	defaultStreamTimeout     = 4 * time.Minute
	defaultMaxRetries        = 2 // the SDK's own default, spelled out
)

// Config configures the Anthropic client.
type Config struct {
	APIKey string
	Model  string // default DefaultModel
	// BaseURL overrides the API address; tests point it at a fake server.
	BaseURL string
	// Fallback asks the API to re-run a request that a safety classifier
	// declined on the model Anthropic recommends for that category
	// (beta server-side-fallback-2026-07-01, fallbacks: "default").
	Fallback bool
	// MaxRetries is how often the SDK repeats a request after a 408, 409, 429,
	// 5xx or connection error. Zero means the SDK default of 2; negative means
	// no retries. These retries are about transport and capacity only. They
	// are unrelated to the repair loop, which re-asks the model about its own
	// invalid output, and neither counts toward the other's budget.
	MaxRetries int
	// CompleteTimeout and StreamTimeout bound a whole call, retries included.
	// Zero means 90 seconds and 4 minutes.
	CompleteTimeout time.Duration
	StreamTimeout   time.Duration
}

// Anthropic is the real Client, built on the official Go SDK.
type Anthropic struct {
	client          anthropic.Client
	model           string
	fallback        bool
	completeTimeout time.Duration
	streamTimeout   time.Duration
}

// NewAnthropic creates a client. The key is passed explicitly; no credential
// is looked up elsewhere.
func NewAnthropic(cfg Config) *Anthropic {
	retries := cfg.MaxRetries
	switch {
	case retries == 0:
		retries = defaultMaxRetries
	case retries < 0:
		retries = 0
	}
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey), option.WithMaxRetries(retries)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	a := &Anthropic{
		client:          anthropic.NewClient(opts...),
		model:           cfg.Model,
		fallback:        cfg.Fallback,
		completeTimeout: cfg.CompleteTimeout,
		streamTimeout:   cfg.StreamTimeout,
	}
	if a.model == "" {
		a.model = DefaultModel
	}
	if a.completeTimeout <= 0 {
		a.completeTimeout = defaultCompleteTimeout
	}
	if a.streamTimeout <= 0 {
		a.streamTimeout = defaultStreamTimeout
	}
	return a
}

// Mode implements Client.
func (a *Anthropic) Mode() Mode { return ModeAnthropic }

// Model implements Client.
func (a *Anthropic) Model() string { return a.model }

// params builds the request. Thinking is left unset: on Claude Opus 5.5 it is
// always on and adaptive, and effort is the only control. No assistant prefill
// and no forced tool use, both of which that model rejects; the JSON shape
// comes from structured outputs (output_config.format) instead.
func (a *Anthropic) params(req Request, defaultMaxTokens int64) anthropic.BetaMessageNewParams {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	p := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: maxTokens,
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.User))},
	}
	if req.System != "" {
		p.System = []anthropic.BetaTextBlockParam{{Text: req.System}}
	}
	if req.Effort != "" {
		p.OutputConfig.Effort = anthropic.BetaOutputConfigEffort(req.Effort)
	}
	if req.Schema != nil {
		p.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: req.Schema}
	}
	if a.fallback {
		p.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		p.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	return p
}

// Complete implements Client.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, a.completeTimeout)
	defer cancel()

	msg, err := a.client.Beta.Messages.New(ctx, a.params(req, defaultCompleteMaxTokens))
	if err != nil {
		return Response{}, mapError(err)
	}
	return result(msg)
}

// Stream implements Client. Text deltas are passed on as they arrive, whichever
// model produced them; after a mid-stream fallback the text continues in a new
// block, and the pieces together are the complete answer.
func (a *Anthropic) Stream(ctx context.Context, req Request, onText func(string)) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, a.streamTimeout)
	defer cancel()

	stream := a.client.Beta.Messages.NewStreaming(ctx, a.params(req, defaultStreamMaxTokens))
	defer stream.Close()

	var msg anthropic.BetaMessage
	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			return Response{}, fmt.Errorf("anthropic: assemble streamed message: %w", err)
		}
		delta, ok := event.AsAny().(anthropic.BetaRawContentBlockDeltaEvent)
		if !ok || onText == nil {
			continue
		}
		if text, ok := delta.Delta.AsAny().(anthropic.BetaTextDelta); ok && text.Text != "" {
			onText(text.Text)
		}
	}
	if err := stream.Err(); err != nil {
		return Response{}, mapError(err)
	}
	return result(&msg)
}

// result turns a finished message into a Response, or into the error its stop
// reason stands for. A refusal is checked before the content is read: the
// content is empty or partial then.
func result(msg *anthropic.BetaMessage) (Response, error) {
	switch msg.StopReason {
	case anthropic.BetaStopReasonEndTurn, anthropic.BetaStopReasonStopSequence:
	case anthropic.BetaStopReasonRefusal:
		return Response{}, &RefusalError{
			Category:    string(msg.StopDetails.Category),
			Explanation: msg.StopDetails.Explanation,
		}
	case anthropic.BetaStopReasonMaxTokens, anthropic.BetaStopReasonModelContextWindowExceeded:
		return Response{}, fmt.Errorf("anthropic: stop reason %q after %d output tokens: %w",
			msg.StopReason, msg.Usage.OutputTokens, ErrMaxTokens)
	default:
		return Response{}, fmt.Errorf("anthropic: stop reason %q: %w", msg.StopReason, ErrUnexpectedStop)
	}

	resp := Response{
		Model:        string(msg.Model),
		ServedBy:     string(msg.Model),
		InputTokens:  msg.Usage.InputTokens,
		OutputTokens: msg.Usage.OutputTokens,
	}
	var text strings.Builder
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.BetaTextBlock:
			text.WriteString(b.Text)
		case anthropic.BetaFallbackBlock:
			resp.FallbackUsed = true
			resp.ServedBy = string(b.To.Model)
		}
	}
	resp.Text = text.String()
	return resp, nil
}

// mapError turns an SDK or transport error into one of the package errors,
// keeping the original in the chain for logs.
func mapError(err error) error {
	if apiErr, ok := errors.AsType[*anthropic.Error](err); ok {
		detail := fmt.Sprintf("anthropic: status %d (request %s): %s", apiErr.StatusCode, apiErr.RequestID, apiErr.Message)
		// An error event in the middle of a stream arrives with the status of
		// the response that opened it (200), so the error type decides too.
		kind := string(apiErr.Type())
		switch {
		case apiErr.StatusCode == http.StatusTooManyRequests || kind == "rate_limit_error":
			return fmt.Errorf("%s: %w", detail, &RateLimitError{RetryAfter: retryAfter(apiErr.Response)})
		case apiErr.StatusCode == http.StatusRequestTimeout || kind == "timeout_error":
			return fmt.Errorf("%s: %w", detail, ErrTimeout)
		case apiErr.StatusCode >= 500 || kind == "overloaded_error" || kind == "api_error": // 529 is overloaded
			return fmt.Errorf("%s: %w", detail, ErrUnavailable)
		default:
			return fmt.Errorf("%s: %w", detail, ErrBadRequest)
		}
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("anthropic: %w", err) // the caller gave up; not the provider's fault
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("anthropic: %w: %w", ErrTimeout, err)
	}
	return fmt.Errorf("anthropic: %w: %w", ErrUnavailable, err)
}

// retryAfter reads the wait a 429 asked for from Retry-After-Ms or Retry-After
// (seconds or an HTTP date); zero when there is none.
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	if ms, err := strconv.ParseFloat(resp.Header.Get("Retry-After-Ms"), 64); err == nil && ms > 0 {
		return time.Duration(ms * float64(time.Millisecond))
	}
	v := resp.Header.Get("Retry-After")
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(0, time.Until(t))
	}
	return 0
}

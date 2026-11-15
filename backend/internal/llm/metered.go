package llm

import (
	"context"
	"time"
)

// Call is what one request to a model used: tokens, cost and time. It never carries the request or the answer.
type Call struct {
	Task         Task
	Stream       bool
	Model        string
	ServedBy     string
	FallbackUsed bool
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
	Duration     time.Duration
	// FirstText is the time from the start of a streamed call to its first piece of text; zero for Complete and for a
	// stream that produced no text.
	FirstText time.Duration
	// Err is the error of the call, nil on success. The usage of a failed call is still counted: a cut-off answer is billed.
	Err error
}

// Metered wraps a Client and reports every call to onCall, after the call has ended and on the goroutine that made it (so
// onCall must be safe for concurrent use). It is how the api logs its spend and how `make eval` keeps its budget.
func Metered(c Client, onCall func(Call)) Client { return &metered{Client: c, onCall: onCall} }

type metered struct {
	Client
	onCall func(Call)
}

func (m *metered) report(req Request, stream bool, start time.Time, first time.Duration, resp Response, err error) {
	call := Call{
		Task: req.Task, Stream: stream, Model: resp.Model, ServedBy: resp.ServedBy, FallbackUsed: resp.FallbackUsed,
		InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens, CostUSD: CostOf(resp),
		Duration: time.Since(start), FirstText: first, Err: err,
	}
	if call.Model == "" {
		call.Model = m.Client.Model() // the call failed before any message arrived
	}
	m.onCall(call)
}

// Complete implements Client.
func (m *metered) Complete(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	resp, err := m.Client.Complete(ctx, req)
	m.report(req, false, start, 0, resp, err)
	return resp, err
}

// Stream implements Client.
func (m *metered) Stream(ctx context.Context, req Request, onText func(string)) (Response, error) {
	start := time.Now()
	var first time.Duration
	resp, err := m.Client.Stream(ctx, req, func(piece string) {
		if first == 0 {
			first = max(time.Since(start), 1) // never zero once a piece has arrived
		}
		if onText != nil {
			onText(piece)
		}
	})
	m.report(req, true, start, first, resp, err)
	return resp, err
}

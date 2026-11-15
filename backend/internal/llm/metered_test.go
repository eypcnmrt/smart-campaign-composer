package llm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// usageClient answers with fixed usage, so Metered can be checked without a network.
type usageClient struct {
	resp  Response
	err   error
	pause time.Duration
	chunk []string
}

func (usageClient) Mode() Mode    { return ModeAnthropic }
func (usageClient) Model() string { return "claude-opus-5-5" }

func (u usageClient) Complete(context.Context, Request) (Response, error) {
	time.Sleep(u.pause)
	return u.resp, u.err
}

func (u usageClient) Stream(_ context.Context, _ Request, onText func(string)) (Response, error) {
	time.Sleep(u.pause)
	for _, c := range u.chunk {
		onText(c)
	}
	return u.resp, u.err
}

type calls struct {
	mu  sync.Mutex
	all []Call
}

func (c *calls) add(call Call) { c.mu.Lock(); c.all = append(c.all, call); c.mu.Unlock() }

func TestMetered_reportsEveryCall(t *testing.T) {
	t.Parallel()
	var got calls
	ok := Response{Text: "{}", Model: "claude-opus-5-5", ServedBy: "claude-opus-5-5", InputTokens: 1000, OutputTokens: 500}
	m := Metered(usageClient{resp: ok, pause: 5 * time.Millisecond, chunk: []string{"a", "b", "c"}}, got.add)

	if m.Mode() != ModeAnthropic || m.Model() != "claude-opus-5-5" {
		t.Errorf("Mode() = %q, Model() = %q; the wrapper must be invisible", m.Mode(), m.Model())
	}
	if _, err := m.Complete(t.Context(), Request{Task: TaskSegment}); err != nil {
		t.Fatal(err)
	}
	pieces := 0
	if _, err := m.Stream(t.Context(), Request{Task: TaskContent}, func(string) { pieces++ }); err != nil {
		t.Fatal(err)
	}
	if pieces != 3 {
		t.Errorf("onText got %d pieces, want 3: the wrapper must pass them on", pieces)
	}

	if len(got.all) != 2 {
		t.Fatalf("%d calls reported, want 2", len(got.all))
	}
	seg, content := got.all[0], got.all[1]
	if seg.Task != TaskSegment || seg.Stream || seg.FirstText != 0 || seg.Err != nil {
		t.Errorf("segment call = %+v", seg)
	}
	if !content.Stream || content.Task != TaskContent || content.FirstText <= 0 || content.FirstText > content.Duration {
		t.Errorf("content call = %+v: first text must be set and within the duration", content)
	}
	for _, c := range got.all {
		if c.InputTokens != 1000 || c.OutputTokens != 500 || !near(c.CostUSD, 0.004+0.01) || c.Duration < 5*time.Millisecond {
			t.Errorf("call = %+v", c)
		}
	}
}

func TestMetered_failedCallStillCountsItsTokens(t *testing.T) {
	t.Parallel()
	var got calls
	cut := Response{Model: "claude-opus-5-5", ServedBy: "claude-opus-5-5", InputTokens: 1000, OutputTokens: 16000}
	m := Metered(usageClient{resp: cut, err: ErrMaxTokens}, got.add)
	if _, err := m.Complete(t.Context(), Request{Task: TaskSegment}); !errors.Is(err, ErrMaxTokens) {
		t.Fatalf("error = %v, want ErrMaxTokens passed through", err)
	}
	c := got.all[0]
	if !errors.Is(c.Err, ErrMaxTokens) || c.OutputTokens != 16000 || !near(c.CostUSD, 0.004+0.32) {
		t.Errorf("call = %+v, want the cut-off answer billed", c)
	}
}

func TestMetered_failureBeforeAnyMessageNamesTheClientsModel(t *testing.T) {
	t.Parallel()
	var got calls
	m := Metered(usageClient{err: ErrUnavailable}, got.add)
	_, _ = m.Complete(t.Context(), Request{Task: TaskSegment})
	if c := got.all[0]; c.Model != "claude-opus-5-5" || c.CostUSD != 0 {
		t.Errorf("call = %+v", c)
	}
}

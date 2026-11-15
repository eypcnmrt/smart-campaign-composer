package llm

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCostOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		resp Response
		want float64
	}{
		{"opus", Response{Model: "claude-opus-5-5", InputTokens: 1_000_000, OutputTokens: 1_000_000}, 24},
		{"sonnet", Response{Model: "claude-sonnet-5-5", InputTokens: 1_000_000, OutputTokens: 1_000_000}, 12},
		{"small call", Response{Model: "claude-opus-5-5", InputTokens: 1200, OutputTokens: 800}, 0.0048 + 0.016},
		{"mock reports no tokens", Response{Model: "mock"}, 0},
		{"unknown model is charged at the dearest price", Response{Model: "claude-future", InputTokens: 1_000_000, OutputTokens: 1_000_000}, 24},
		{
			"a fallback to a dearer model is charged at it",
			Response{Model: "claude-sonnet-5-5", ServedBy: "claude-opus-5-5", FallbackUsed: true, InputTokens: 1_000_000, OutputTokens: 1_000_000},
			24,
		},
		{
			"a fallback to a cheaper model keeps the dearer price",
			Response{Model: "claude-opus-5-5", ServedBy: "claude-sonnet-5-5", FallbackUsed: true, InputTokens: 1_000_000, OutputTokens: 1_000_000},
			24,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CostOf(tt.resp); !near(got, tt.want) {
				t.Errorf("CostOf() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPriceOf_defaultModelIsPriced(t *testing.T) {
	t.Parallel()
	if _, ok := PriceOf(DefaultModel); !ok {
		t.Errorf("the default model %q has no price: the cost of every call would be guessed", DefaultModel)
	}
}

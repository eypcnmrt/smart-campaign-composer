package llm

import (
	"errors"
	"testing"
	"time"
)

func TestRefusalError(t *testing.T) {
	t.Parallel()
	err := error(&RefusalError{Category: "cyber", Explanation: "x"})
	if !errors.Is(err, ErrRefusal) {
		t.Error("RefusalError must match ErrRefusal")
	}
	if errors.Is(err, ErrMaxTokens) {
		t.Error("RefusalError must not match other sentinels")
	}
	if got := err.Error(); got != "the model refused the request (category cyber)" {
		t.Errorf("Error() = %q", got)
	}
	if got := (&RefusalError{}).Error(); got != "the model refused the request" {
		t.Errorf("Error() without a category = %q", got)
	}
}

func TestRateLimitError(t *testing.T) {
	t.Parallel()
	err := error(&RateLimitError{RetryAfter: 7 * time.Second})
	if !errors.Is(err, ErrRateLimited) {
		t.Error("RateLimitError must match ErrRateLimited")
	}
	if got := err.Error(); got != "the model provider rate-limited the request (retry after 7s)" {
		t.Errorf("Error() = %q", got)
	}
	if got := (&RateLimitError{}).Error(); got != "the model provider rate-limited the request" {
		t.Errorf("Error() without a wait = %q", got)
	}
}

func TestFromEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		env          map[string]string
		wantMode     Mode
		wantModel    string
		wantFallback bool
	}{
		{name: "nothing set", env: nil, wantMode: ModeMock, wantModel: "mock"},
		{name: "empty key", env: map[string]string{"ANTHROPIC_API_KEY": ""}, wantMode: ModeMock, wantModel: "mock"},
		{name: "blank key", env: map[string]string{"ANTHROPIC_API_KEY": "  \t"}, wantMode: ModeMock, wantModel: "mock"},
		{
			// Any other credential source must not switch the real client on.
			name:     "auth token without a key",
			env:      map[string]string{"ANTHROPIC_AUTH_TOKEN": "t", "ANTHROPIC_PROFILE": "p"},
			wantMode: ModeMock, wantModel: "mock",
		},
		{name: "model without a key", env: map[string]string{"ANTHROPIC_MODEL": "claude-sonnet-5-5"}, wantMode: ModeMock, wantModel: "mock"},
		{name: "key only", env: map[string]string{"ANTHROPIC_API_KEY": "test-key"}, wantMode: ModeAnthropic, wantModel: DefaultModel, wantFallback: true},
		{name: "key with spaces", env: map[string]string{"ANTHROPIC_API_KEY": " test-key \n"}, wantMode: ModeAnthropic, wantModel: DefaultModel, wantFallback: true},
		{name: "custom model", env: map[string]string{"ANTHROPIC_API_KEY": "test-key", "ANTHROPIC_MODEL": " claude-sonnet-5-5 "}, wantMode: ModeAnthropic, wantModel: "claude-sonnet-5-5", wantFallback: true},
		{name: "empty model", env: map[string]string{"ANTHROPIC_API_KEY": "test-key", "ANTHROPIC_MODEL": ""}, wantMode: ModeAnthropic, wantModel: DefaultModel, wantFallback: true},
		{name: "fallback off", env: map[string]string{"ANTHROPIC_API_KEY": "test-key", "ANTHROPIC_FALLBACK": "off"}, wantMode: ModeAnthropic, wantModel: DefaultModel},
		{name: "fallback OFF in capitals", env: map[string]string{"ANTHROPIC_API_KEY": "test-key", "ANTHROPIC_FALLBACK": " OFF "}, wantMode: ModeAnthropic, wantModel: DefaultModel},
		{name: "fallback on", env: map[string]string{"ANTHROPIC_API_KEY": "test-key", "ANTHROPIC_FALLBACK": "on"}, wantMode: ModeAnthropic, wantModel: DefaultModel, wantFallback: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := FromEnv(func(k string) string { return tt.env[k] })
			if c.Mode() != tt.wantMode {
				t.Errorf("Mode() = %q, want %q", c.Mode(), tt.wantMode)
			}
			if c.Model() != tt.wantModel {
				t.Errorf("Model() = %q, want %q", c.Model(), tt.wantModel)
			}
			if a, ok := c.(*Anthropic); ok && a.fallback != tt.wantFallback {
				t.Errorf("fallback = %v, want %v", a.fallback, tt.wantFallback)
			}
		})
	}
}

func TestDefaultModel(t *testing.T) {
	t.Parallel()
	if DefaultModel != "claude-opus-5-5" {
		t.Errorf("DefaultModel = %q, want claude-opus-5-5", DefaultModel)
	}
}

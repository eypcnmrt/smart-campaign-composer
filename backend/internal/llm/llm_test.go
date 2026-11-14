package llm

import (
	"errors"
	"strings"
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
			sel, err := FromEnv(func(k string) string { return tt.env[k] })
			if err != nil {
				t.Fatalf("FromEnv() error = %v", err)
			}
			c := sel.Client
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

func TestFromEnv_mode(t *testing.T) {
	t.Parallel()
	const key = "fake-key-for-the-mode-table" // never a real key
	tests := []struct {
		name        string
		env         map[string]string
		wantErr     error
		wantMode    Mode
		wantSetting string
		wantReason  string // part of the explanation for the log
	}{
		{"unset is auto, no key: mock", nil, nil, ModeMock, "auto", "no ANTHROPIC_API_KEY"},
		{"unset is auto, key: anthropic", map[string]string{EnvAPIKey: key}, nil, ModeAnthropic, "auto", "ANTHROPIC_API_KEY is set"},
		{"auto spelled out, no key", map[string]string{EnvMode: "auto"}, nil, ModeMock, "auto", "demo mock"},
		{"auto in capitals with spaces", map[string]string{EnvMode: " AUTO ", EnvAPIKey: key}, nil, ModeAnthropic, "auto", ""},
		{"anthropic with a key", map[string]string{EnvMode: "anthropic", EnvAPIKey: key}, nil, ModeAnthropic, "anthropic", "LLM_MODE=anthropic"},
		{"anthropic without a key is an error", map[string]string{EnvMode: "anthropic"}, ErrNoAPIKey, "", "", ""},
		{"anthropic with a blank key is an error", map[string]string{EnvMode: "anthropic", EnvAPIKey: "  "}, ErrNoAPIKey, "", "", ""},
		{"mock without a key", map[string]string{EnvMode: "mock"}, nil, ModeMock, "mock", "LLM_MODE=mock"},
		{"mock ignores a key that is set", map[string]string{EnvMode: "mock", EnvAPIKey: key}, nil, ModeMock, "mock", "is ignored"},
		{"unknown mode", map[string]string{EnvMode: "claude", EnvAPIKey: key}, ErrInvalidMode, "", "", ""},
		{"a typo is not auto", map[string]string{EnvMode: "anthropik"}, ErrInvalidMode, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sel, err := FromEnv(func(k string) string { return tt.env[k] })
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("FromEnv() error = %v, want %v", err, tt.wantErr)
				}
				if sel.Client != nil {
					t.Fatal("a failed selection must not hand out a client")
				}
				return
			}
			if err != nil {
				t.Fatalf("FromEnv() error = %v", err)
			}
			if sel.Client.Mode() != tt.wantMode || sel.Setting != tt.wantSetting {
				t.Errorf("mode = %q, setting = %q, want %q and %q", sel.Client.Mode(), sel.Setting, tt.wantMode, tt.wantSetting)
			}
			if !strings.Contains(sel.Reason, tt.wantReason) {
				t.Errorf("reason = %q, want it to contain %q", sel.Reason, tt.wantReason)
			}
			if strings.Contains(sel.Reason, key) {
				t.Errorf("the reason %q contains the key", sel.Reason)
			}
		})
	}
}

// A value in the wrong variable (a key pasted into LLM_MODE) must not end up in the error or in a log line.
func TestFromEnv_invalidModeIsNotEchoed(t *testing.T) {
	t.Parallel()
	_, err := FromEnv(func(k string) string {
		if k == EnvMode {
			return "pasted-by-mistake"
		}
		return ""
	})
	if !errors.Is(err, ErrInvalidMode) || strings.Contains(err.Error(), "pasted-by-mistake") {
		t.Fatalf("error = %v, want ErrInvalidMode without the value", err)
	}
}

func TestDefaultModel(t *testing.T) {
	t.Parallel()
	if DefaultModel != "claude-opus-5-5" {
		t.Errorf("DefaultModel = %q, want claude-opus-5-5", DefaultModel)
	}
}

func TestFallbackEnabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{"", true}, {"on", true}, {" ON ", true}, {"true", true}, {"1", true}, {"yes", true},
		{"off", false}, {"false", false}, {"0", false}, {"no", false}, {"of", false}, {"enabled", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := fallbackEnabled(tt.in); got != tt.want {
				t.Fatalf("fallbackEnabled(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

package mockprovider

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestConfigFromEnv(t *testing.T) {
	t.Parallel()
	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		c, err := ConfigFromEnv(env(nil))
		if err != nil {
			t.Fatal(err)
		}
		if c.ErrorRate != 0 || c.ThrottleRate != 0 || c.LateRate != 0 || c.RateLimit != 0 {
			t.Fatalf("default provider must not fail: %+v", c)
		}
		if c.RetryAfter != time.Second || c.Seed != 1 {
			t.Fatalf("defaults = %+v", c)
		}
	})
	t.Run("all values", func(t *testing.T) {
		t.Parallel()
		c, err := ConfigFromEnv(env(map[string]string{
			EnvSeed: "42", EnvDelayMin: "1ms", EnvDelayMax: "9ms", EnvErrorRate: "0.1", EnvThrottleRate: "0.2",
			EnvRetryAfter: "2s", EnvRateLimit: "120", EnvLateRate: "0.05", EnvLateDelay: "4s",
		}))
		if err != nil {
			t.Fatal(err)
		}
		want := Config{Seed: 42, DelayMin: time.Millisecond, DelayMax: 9 * time.Millisecond, ErrorRate: 0.1, ThrottleRate: 0.2,
			RetryAfter: 2 * time.Second, RateLimit: 120, LateRate: 0.05, LateDelay: 4 * time.Second}
		if c != want {
			t.Fatalf("config = %+v, want %+v", c, want)
		}
	})
	errTests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"not a number", map[string]string{EnvErrorRate: "lots"}, EnvErrorRate},
		{"not a duration", map[string]string{EnvRetryAfter: "soon"}, EnvRetryAfter},
		{"rate above one", map[string]string{EnvThrottleRate: "1.5"}, "throttle rate"},
		{"negative rate", map[string]string{EnvLateRate: "-0.1"}, "late rate"},
		{"rates add up to more than one", map[string]string{EnvErrorRate: "0.6", EnvThrottleRate: "0.6"}, "add up"},
		{"inverted delay range", map[string]string{EnvDelayMin: "50ms", EnvDelayMax: "10ms"}, "delay range"},
		{"negative rate limit", map[string]string{EnvRateLimit: "-1"}, "negative"},
		{"two problems are both reported", map[string]string{EnvErrorRate: "x", EnvRateLimit: "y"}, EnvRateLimit},
	}
	for _, tt := range errTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ConfigFromEnv(env(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestConfigFromEnv_rejectsNaN(t *testing.T) {
	t.Parallel()
	for _, name := range []string{EnvErrorRate, EnvThrottleRate, EnvLateRate} {
		if _, err := ConfigFromEnv(env(map[string]string{name: "NaN"})); err == nil {
			t.Errorf("%s=NaN was accepted; every comparison with NaN is false, so the rate would silently be off", name)
		}
	}
}

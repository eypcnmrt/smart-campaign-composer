// Package mockprovider is the stand-in SMS/email provider: an HTTP server that accepts messages
// with an Idempotency-Key and can misbehave on purpose (latency, 500s, 429s with Retry-After, its
// own rate limit, and replies that arrive after the client gave up) so the worker's retry, rate
// limit and idempotency logic can be tested against realistic failures.
package mockprovider

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

// Channels the provider serves. They are the delivery channel names used everywhere else.
const (
	ChannelSMS   = "sms"
	ChannelEmail = "email"
)

// Config describes the provider's behaviour. The zero value is a well-behaved provider that
// answers immediately and never fails.
type Config struct {
	// Seed makes the random decisions reproducible: each one is derived from the seed, the
	// channel, the idempotency key and how often that key has been seen, so it does not depend on
	// the order in which concurrent requests arrive.
	Seed uint64

	// DelayMin and DelayMax bound the processing time of every request.
	DelayMin, DelayMax time.Duration

	// ErrorRate is the share of requests answered with 500 (the message is not accepted).
	ErrorRate float64
	// ThrottleRate is the share of requests answered with 429 (the message is not accepted).
	ThrottleRate float64
	// RetryAfter is sent as the Retry-After header of every 429, rounded up to whole seconds.
	RetryAfter time.Duration

	// RateLimit is the provider's own limit in requests per second and channel, counted over any
	// sliding second; above it requests get 429. Zero disables the limit.
	RateLimit int

	// LateRate is the share of accepted messages whose reply is held back for LateDelay. The
	// message counts as delivered; only the answer is late, so a client that times out and
	// retries with the same key must not get a second delivery.
	LateRate  float64
	LateDelay time.Duration
}

// Validate reports a configuration that cannot work.
func (c Config) Validate() error {
	rates := map[string]float64{"error rate": c.ErrorRate, "throttle rate": c.ThrottleRate, "late rate": c.LateRate}
	for name, r := range rates {
		if math.IsNaN(r) || r < 0 || r > 1 {
			return fmt.Errorf("%s must be between 0 and 1, got %v", name, r)
		}
	}
	if c.ErrorRate+c.ThrottleRate > 1 {
		return fmt.Errorf("error rate and throttle rate add up to more than 1")
	}
	if c.DelayMin < 0 || c.DelayMax < c.DelayMin {
		return fmt.Errorf("delay range %v..%v is invalid", c.DelayMin, c.DelayMax)
	}
	if c.RetryAfter < 0 || c.LateDelay < 0 || c.RateLimit < 0 {
		return fmt.Errorf("retry-after, late delay and rate limit must not be negative")
	}
	return nil
}

// Environment variable names read by ConfigFromEnv.
const (
	EnvSeed         = "MOCK_SEED"
	EnvDelayMin     = "MOCK_DELAY_MIN"
	EnvDelayMax     = "MOCK_DELAY_MAX"
	EnvErrorRate    = "MOCK_ERROR_RATE"
	EnvThrottleRate = "MOCK_THROTTLE_RATE"
	EnvRetryAfter   = "MOCK_RETRY_AFTER"
	EnvRateLimit    = "MOCK_RATE_LIMIT"
	EnvLateRate     = "MOCK_LATE_RATE"
	EnvLateDelay    = "MOCK_LATE_DELAY"
)

// ConfigFromEnv reads the configuration through getenv (os.Getenv in main). Unset variables keep
// the defaults: seed 1, delay 5ms..25ms, no failures, Retry-After 1s, late delay 3s.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{Seed: 1, DelayMin: 5 * time.Millisecond, DelayMax: 25 * time.Millisecond, RetryAfter: time.Second, LateDelay: 3 * time.Second}
	var errs []error
	read := func(name string, set func(string) error) {
		if v := getenv(name); v != "" {
			if err := set(v); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
		}
	}
	dur := func(dst *time.Duration) func(string) error {
		return func(v string) (err error) { *dst, err = time.ParseDuration(v); return }
	}
	rate := func(dst *float64) func(string) error {
		return func(v string) (err error) { *dst, err = strconv.ParseFloat(v, 64); return }
	}
	read(EnvSeed, func(v string) (err error) { c.Seed, err = strconv.ParseUint(v, 10, 64); return })
	read(EnvDelayMin, dur(&c.DelayMin))
	read(EnvDelayMax, dur(&c.DelayMax))
	read(EnvErrorRate, rate(&c.ErrorRate))
	read(EnvThrottleRate, rate(&c.ThrottleRate))
	read(EnvRetryAfter, dur(&c.RetryAfter))
	read(EnvRateLimit, func(v string) (err error) { c.RateLimit, err = strconv.Atoi(v); return })
	read(EnvLateRate, rate(&c.LateRate))
	read(EnvLateDelay, dur(&c.LateDelay))
	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

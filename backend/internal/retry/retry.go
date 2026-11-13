// Package retry decides what to do after a delivery attempt. Everything here is a pure function:
// the clock and the random jitter are passed in, so every rule is tested with exact numbers.
//
// The rules (docs/PLAN.md section 1):
//   - 2xx: delivered.
//   - 429: try again, but not before the Retry-After the provider sent.
//   - 5xx, 408, timeouts and network errors: try again with exponential backoff and jitter.
//   - any other 4xx: permanent failure, retrying would repeat the same mistake.
//   - at most MaxAttempts attempts, then the delivery fails.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Kind is how one attempt ended.
type Kind int

const (
	// Delivered: the provider accepted the message.
	Delivered Kind = iota
	// RateLimited: the provider said 429; Result.RetryAfter may carry its wait.
	RateLimited
	// Transient: a 5xx, 408, timeout or connection error; worth another try.
	Transient
	// Permanent: another 4xx; the request itself is wrong.
	Permanent
)

func (k Kind) String() string {
	switch k {
	case Delivered:
		return "delivered"
	case RateLimited:
		return "rate_limited"
	case Transient:
		return "transient"
	case Permanent:
		return "permanent"
	}
	return "unknown"
}

// Result is what one attempt produced, already classified.
type Result struct {
	Kind       Kind
	Status     int           // HTTP status, 0 when there was no response
	RetryAfter time.Duration // from the Retry-After header, 0 when absent or unreadable
	Err        error         // transport error, nil when there was a response
}

// Classify turns the raw outcome of an HTTP attempt into a Result. status is 0 and err set when no
// response arrived (timeout, connection reset); retryAfter is the raw header value.
func Classify(status int, retryAfter string, err error, now time.Time) Result {
	if err != nil {
		return Result{Kind: Transient, Err: err}
	}
	r := Result{Status: status}
	switch {
	case status >= 200 && status < 300:
		r.Kind = Delivered
	case status == http.StatusTooManyRequests:
		r.Kind = RateLimited
		r.RetryAfter = ParseRetryAfter(retryAfter, now)
	case status >= 500, status == http.StatusRequestTimeout:
		r.Kind = Transient
	default:
		// 3xx are not followed by the client, and any other 4xx is a mistake in the request.
		r.Kind = Permanent
	}
	return r
}

// ParseRetryAfter reads a Retry-After value: whole seconds or an HTTP date. Anything unreadable,
// negative or in the past gives 0, so the caller falls back to its own backoff.
func ParseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs <= 0 || secs > int64(math.MaxInt64/int64(time.Second)) {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// Action is what the caller should do with the delivery.
type Action int

const (
	// Done: the delivery succeeded.
	Done Action = iota
	// Retry: queue it again after Decision.Delay.
	Retry
	// Fail: give up; Decision.Reason says why.
	Fail
)

// Decision is the verdict on one attempt.
type Decision struct {
	Action Action
	Delay  time.Duration // for Retry
	Reason string        // for Retry and Fail: short text for last_error
}

// Policy holds the tunables.
type Policy struct {
	MaxAttempts int           // attempts in total, the first one included
	BaseDelay   time.Duration // backoff before the second attempt
	MaxDelay    time.Duration // cap of the backoff
	// MaxRetryAfter caps what a provider may ask for, so one absurd header cannot park a delivery
	// for hours.
	MaxRetryAfter time.Duration
}

// DefaultPolicy is the production policy: 5 attempts, 1s base doubling to at most 1 minute.
func DefaultPolicy() Policy {
	return Policy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: time.Minute, MaxRetryAfter: 5 * time.Minute}
}

// Backoff is the wait before the next attempt after attempt number `attempt` (1 is the first) failed:
// BaseDelay * 2^(attempt-1), capped at MaxDelay, then "equal jitter": half of it is fixed and the other
// half is scaled by jitter in [0, 1). The result lies in [d/2, d).
func (p Policy) Backoff(attempt int, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := float64(p.BaseDelay) * math.Pow(2, float64(attempt-1))
	if limit := float64(p.MaxDelay); d > limit || math.IsInf(d, 0) {
		d = limit
	}
	jitter = min(max(jitter, 0), math.Nextafter(1, 0))
	return time.Duration(d/2 + jitter*d/2)
}

// Decide judges attempt number `attempt` (1 is the first) that ended with res. jitter is a random
// number in [0, 1); pass a constant in tests.
func (p Policy) Decide(attempt int, res Result, jitter float64) Decision {
	switch res.Kind {
	case Delivered:
		return Decision{Action: Done}
	case Permanent:
		return Decision{Action: Fail, Reason: fmt.Sprintf("permanent error: %s", describe(res))}
	}

	if attempt >= p.MaxAttempts {
		return Decision{Action: Fail, Reason: fmt.Sprintf("gave up after %d attempts: %s", attempt, describe(res))}
	}
	delay := p.Backoff(attempt, jitter)
	if res.Kind == RateLimited && res.RetryAfter > 0 {
		// Never come back earlier than the provider asked; the backoff may be longer.
		delay = max(delay, min(res.RetryAfter, p.MaxRetryAfter))
	}
	return Decision{Action: Retry, Delay: delay, Reason: describe(res)}
}

// describe gives the short, secret-free text stored in last_error.
func describe(r Result) string {
	switch {
	case r.Err != nil && isTimeout(r.Err):
		return "timeout"
	case r.Err != nil:
		return "request failed: " + r.Err.Error()
	case r.Kind == RateLimited && r.RetryAfter > 0:
		return fmt.Sprintf("http %d, retry after %s", r.Status, r.RetryAfter)
	default:
		return fmt.Sprintf("http %d", r.Status)
	}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if ne, ok := errors.AsType[net.Error](err); ok {
		return ne.Timeout()
	}
	return false
}

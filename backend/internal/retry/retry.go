// Package retry decides what to do after a delivery attempt. Everything here is a pure function:
// the clock and the random jitter are passed in, so every rule is tested with exact numbers.
//
// The rules (docs/PLAN.md section 1):
//   - 2xx: delivered.
//   - 429: try again, but not before the Retry-After the provider sent.
//   - 5xx, 408, timeouts and network errors: try again with exponential backoff and jitter.
//   - any other 4xx: permanent failure, retrying would repeat the same mistake.
//   - at most MaxAttempts send attempts, then the delivery fails; except that a last attempt which ended without
//     any answer (timeout, connection error) is followed by one confirmation request with the same idempotency
//     key, because the message may have been delivered after all (see Policy.confirms). The confirmation is an
//     ordinary claim, so it also passes the rate limiter. If it gets no answer either, the delivery fails with
//     an "outcome unknown" reason that says so plainly.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
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
		// The sender does not follow redirects, so a 3xx arrives here, and any other 4xx is a mistake in the request.
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

// DefaultPolicy is the production policy: 5 attempts, a 15s base doubling to at most 2 minutes (15s, 30s, 1m, 2m, each
// with equal jitter). The four waits add up to about 2 to 4 minutes, so a provider outage of a couple of minutes is
// ridden out by the retries alone; a longer one is handled by the circuit breaker in the worker, which stops claiming
// and gives the attempts back (see package worker).
func DefaultPolicy() Policy {
	return Policy{MaxAttempts: 5, BaseDelay: 15 * time.Second, MaxDelay: 2 * time.Minute, MaxRetryAfter: 5 * time.Minute}
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

	if attempt >= p.MaxAttempts && !p.confirms(attempt, res) {
		if noAnswer(res) {
			// Only the confirmation request (or a later one) gets here with no answer. Say that the result is
			// unknown, not that the delivery failed: the provider may have the message.
			return Decision{Action: Fail, Reason: UnknownOutcomeReason(p.MaxAttempts, attempt, describe(res))}
		}
		return Decision{Action: Fail, Reason: fmt.Sprintf("gave up after %d attempts: %s", attempt, describe(res))}
	}
	delay := p.Backoff(attempt, jitter)
	if res.Kind == RateLimited && res.RetryAfter > 0 {
		// Never come back earlier than the provider asked; the backoff may be longer.
		delay = max(delay, min(res.RetryAfter, p.MaxRetryAfter))
	}
	return Decision{Action: Retry, Delay: delay, Reason: describe(res)}
}

// MaxTotalAttempts is the most attempts a delivery can ever use: MaxAttempts, plus the one confirmation
// attempt described at confirms.
func (p Policy) MaxTotalAttempts() int { return p.MaxAttempts + 1 }

// confirms reports whether the attempt that just ended is the last regular one and ended without any answer
// (a timeout or a connection error). Then the outcome is unknown, not failed: the provider may have accepted
// the message and only its reply was lost. Declaring such a delivery failed would be wrong whenever it was
// delivered, so exactly one more attempt is allowed. It carries the same idempotency key, so it cannot deliver
// twice: if the provider has the message it returns the first reply, otherwise it is an ordinary last try.
// A 5xx or 429 is an answer ("not accepted") and does not qualify.
func (p Policy) confirms(attempt int, res Result) bool {
	return attempt == p.MaxAttempts && noAnswer(res)
}

// noAnswer reports whether the attempt ended without any reply from the provider (timeout, connection error).
func noAnswer(res Result) bool { return res.Kind == Transient && res.Err != nil }

// UnknownReasonPrefix starts the last_error of a delivery whose outcome could not be determined.
const UnknownReasonPrefix = "outcome unknown"

// UnknownOutcomeReason is the last_error of a delivery that used its send attempts and its confirmation request
// without ever getting an answer. detail says how the last one ended.
func UnknownOutcomeReason(maxAttempts, confirmAttempt int, detail string) string {
	return fmt.Sprintf("%s: attempt %d and the confirmation request with the same idempotency key (attempt %d) both ended without an answer (%s); the provider may have delivered the message",
		UnknownReasonPrefix, maxAttempts, confirmAttempt, detail)
}

// Describe gives the short, secret-free text for an attempt that did not deliver (stored in last_error, shown next to an
// open circuit).
func Describe(r Result) string { return describe(r) }

// describe gives the short, secret-free text stored in last_error.
func describe(r Result) string {
	switch {
	case r.Err != nil && isTimeout(r.Err):
		return "timeout"
	case r.Err != nil:
		return "request failed: " + transportReason(r.Err)
	case r.Kind == RateLimited && r.RetryAfter > 0:
		return fmt.Sprintf("http %d, retry after %s", r.Status, r.RetryAfter)
	default:
		return fmt.Sprintf("http %d", r.Status)
	}
}

// transportReason is the cause of a failed request without the request itself. An *url.Error prints the whole URL (method,
// query, user name), which can carry a credential, and this text is stored in last_error and shown on the dashboard.
func transportReason(err error) string {
	if ue, ok := errors.AsType[*url.Error](err); ok && ue.Err != nil {
		return ue.Err.Error()
	}
	return err.Error()
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

// Validate rejects a policy that would silently disable a protection: no attempts, a zero backoff (hot
// retries) or a zero cap on Retry-After (which would make the worker ignore it).
func (p Policy) Validate() error {
	switch {
	case p.MaxAttempts < 1:
		return errors.New("max attempts must be at least 1")
	case p.BaseDelay <= 0:
		return errors.New("base delay must be positive")
	case p.MaxDelay < p.BaseDelay:
		return errors.New("max delay must not be below the base delay")
	case p.MaxRetryAfter <= 0:
		return errors.New("max retry-after must be positive, or a 429's Retry-After would be ignored")
	}
	return nil
}

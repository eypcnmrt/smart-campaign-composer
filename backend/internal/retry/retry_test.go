package retry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

var epoch = time.Date(2025, 11, 13, 12, 0, 0, 0, time.UTC)

func TestClassify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     int
		retryAfter string
		err        error
		want       Kind
		wantWait   time.Duration
	}{
		{"200", 200, "", nil, Delivered, 0},
		{"202", 202, "", nil, Delivered, 0},
		{"429 with seconds", 429, "3", nil, RateLimited, 3 * time.Second},
		{"429 without header", 429, "", nil, RateLimited, 0},
		{"429 with garbage", 429, "soon", nil, RateLimited, 0},
		{"500", 500, "", nil, Transient, 0},
		{"503 ignores Retry-After here", 503, "9", nil, Transient, 0},
		{"408 is a timeout", 408, "", nil, Transient, 0},
		{"400", 400, "", nil, Permanent, 0},
		{"401", 401, "", nil, Permanent, 0},
		{"404", 404, "", nil, Permanent, 0},
		{"422", 422, "", nil, Permanent, 0},
		{"301 is not followed", 301, "", nil, Permanent, 0},
		{"transport error", 0, "", errors.New("connection reset"), Transient, 0},
		{"deadline exceeded", 0, "", context.DeadlineExceeded, Transient, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Classify(tt.status, tt.retryAfter, tt.err, epoch)
			if got.Kind != tt.want || got.RetryAfter != tt.wantWait {
				t.Fatalf("Classify = {%v, wait %v}, want {%v, wait %v}", got.Kind, got.RetryAfter, tt.want, tt.wantWait)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, value string
		want        time.Duration
	}{
		{"seconds", "7", 7 * time.Second},
		{"with spaces", " 2 ", 2 * time.Second},
		{"zero", "0", 0},
		{"negative", "-5", 0},
		{"empty", "", 0},
		{"fraction is not valid", "1.5", 0},
		{"overflow", "99999999999999999999", 0},
		{"huge but parseable", "9223372036854775807", 0},
		{"http date in the future", epoch.Add(90 * time.Second).UTC().Format(http.TimeFormat), 90 * time.Second},
		{"http date in the past", epoch.Add(-time.Minute).UTC().Format(http.TimeFormat), 0},
		{"garbage", "tomorrow", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseRetryAfter(tt.value, epoch); got != tt.want {
				t.Fatalf("ParseRetryAfter(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestPolicy_Backoff(t *testing.T) {
	t.Parallel()
	p := Policy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: 10 * time.Second}
	tests := []struct {
		name    string
		attempt int
		jitter  float64
		want    time.Duration
	}{
		{"first retry, lowest jitter", 1, 0, 500 * time.Millisecond},
		{"first retry, half jitter", 1, 0.5, 750 * time.Millisecond},
		{"second retry doubles", 2, 0, time.Second},
		{"third retry", 3, 0, 2 * time.Second},
		{"fourth retry", 4, 0, 4 * time.Second},
		{"capped", 5, 0, 5 * time.Second}, // 16s capped to 10s, half of it
		{"capped, middle of the range", 9, 0.5, 7500 * time.Millisecond},
		{"jitter below zero is clamped", 1, -3, 500 * time.Millisecond},
		{"attempt zero behaves like one", 0, 0, 500 * time.Millisecond},
		{"huge attempt does not overflow", 5000, 0, 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := p.Backoff(tt.attempt, tt.jitter)
			if got != tt.want {
				t.Fatalf("Backoff(%d, %v) = %v, want %v", tt.attempt, tt.jitter, got, tt.want)
			}
		})
	}
}

func TestPolicy_Backoff_staysInRange(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	for attempt := 1; attempt <= 20; attempt++ {
		full := min(time.Duration(float64(p.BaseDelay)*float64(int64(1)<<min(attempt-1, 40))), p.MaxDelay)
		for _, j := range []float64{-1, 0, 0.25, 0.5, 0.75, 0.999999, 1, 5} {
			got := p.Backoff(attempt, j)
			if got < full/2 || got >= full+1 {
				t.Fatalf("Backoff(%d, %v) = %v outside [%v, %v)", attempt, j, got, full/2, full)
			}
		}
	}
}

func TestPolicy_Decide(t *testing.T) {
	t.Parallel()
	p := Policy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: time.Minute, MaxRetryAfter: 5 * time.Minute}
	timeout := fmtErr(context.DeadlineExceeded)
	tests := []struct {
		name       string
		attempt    int
		res        Result
		want       Action
		wantDelay  time.Duration
		wantReason string
	}{
		{"delivered", 1, Result{Kind: Delivered, Status: 200}, Done, 0, ""},
		{"delivered on the last attempt", 5, Result{Kind: Delivered, Status: 200}, Done, 0, ""},
		{"500 is retried with backoff", 1, Result{Kind: Transient, Status: 500}, Retry, 500 * time.Millisecond, "http 500"},
		{"500 on the second attempt waits longer", 2, Result{Kind: Transient, Status: 500}, Retry, time.Second, "http 500"},
		{"timeout is retried", 1, Result{Kind: Transient, Err: timeout}, Retry, 500 * time.Millisecond, "timeout"},
		{"500 on the last attempt gives up", 5, Result{Kind: Transient, Status: 500}, Fail, 0, "gave up after 5 attempts: http 500"},
		{"timeout on the last attempt is confirmed once more", 5, Result{Kind: Transient, Err: timeout}, Retry, 8 * time.Second, "timeout"},
		{"connection error on the last attempt is confirmed too", 5, Result{Kind: Transient, Err: errors.New("connection reset")}, Retry, 8 * time.Second, "connection reset"},
		{"timeout on the confirmation: outcome unknown", 6, Result{Kind: Transient, Err: timeout}, Fail, 0,
			"outcome unknown: attempt 5 and the confirmation request with the same idempotency key (attempt 6) both ended without an answer (timeout); the provider may have delivered the message"},
		{"connection error on the confirmation: outcome unknown", 6, Result{Kind: Transient, Err: errors.New("connection reset")}, Fail, 0,
			"outcome unknown: attempt 5 and the confirmation request"},
		{"500 on the confirmation is an answer, not unknown", 6, Result{Kind: Transient, Status: 500}, Fail, 0, "gave up after 6 attempts: http 500"},
		{"429 on the confirmation is an answer, not unknown", 6, Result{Kind: RateLimited, Status: 429, RetryAfter: time.Second}, Fail, 0, "gave up after 6 attempts"},
		{"permanent 4xx fails at once", 1, Result{Kind: Permanent, Status: 400}, Fail, 0, "permanent error: http 400"},
		{"429 honours Retry-After longer than the backoff", 1, Result{Kind: RateLimited, Status: 429, RetryAfter: 7 * time.Second}, Retry, 7 * time.Second, "retry after 7s"},
		{"429 keeps the backoff when it is longer", 4, Result{Kind: RateLimited, Status: 429, RetryAfter: time.Second}, Retry, 4 * time.Second, "retry after 1s"},
		{"429 without Retry-After uses the backoff", 2, Result{Kind: RateLimited, Status: 429}, Retry, time.Second, "http 429"},
		{"429 Retry-After is capped", 1, Result{Kind: RateLimited, Status: 429, RetryAfter: time.Hour}, Retry, 5 * time.Minute, "retry after 1h0m0s"},
		{"429 on the last attempt gives up", 5, Result{Kind: RateLimited, Status: 429, RetryAfter: time.Second}, Fail, 0, "gave up after 5 attempts"},
		{"attempt beyond the maximum gives up", 9, Result{Kind: Transient, Status: 503}, Fail, 0, "gave up after 9 attempts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := p.Decide(tt.attempt, tt.res, 0) // jitter 0: the shortest backoff, easy to read
			if got.Action != tt.want || got.Delay != tt.wantDelay {
				t.Fatalf("Decide = %+v, want action %v delay %v", got, tt.want, tt.wantDelay)
			}
			if !strings.Contains(got.Reason, tt.wantReason) {
				t.Fatalf("reason = %q, want it to contain %q", got.Reason, tt.wantReason)
			}
		})
	}
}

// A delivery that never got an answer must not be reported as an ordinary failure, and one that did get an answer
// must not be reported as unknown.
func TestPolicy_Decide_unknownOutcomeWording(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	noAnswerRes := Result{Kind: Transient, Err: context.DeadlineExceeded}
	d := p.Decide(p.MaxTotalAttempts(), noAnswerRes, 0)
	if d.Action != Fail || !strings.HasPrefix(d.Reason, UnknownReasonPrefix) || strings.Contains(d.Reason, "gave up") {
		t.Fatalf("no answer on the confirmation: %+v", d)
	}
	if !strings.Contains(d.Reason, "may have delivered") {
		t.Fatalf("the reason must say the message may have been delivered: %q", d.Reason)
	}
	d = p.Decide(p.MaxTotalAttempts(), Result{Kind: Transient, Status: 503}, 0)
	if d.Action != Fail || strings.HasPrefix(d.Reason, UnknownReasonPrefix) {
		t.Fatalf("a 503 is an answer: %+v", d)
	}
	// the helper used by the queue for an expired lease on the last attempt says the same thing
	if got := UnknownOutcomeReason(5, 6, "the lease expired"); !strings.HasPrefix(got, UnknownReasonPrefix+": attempt 5 and the confirmation request") {
		t.Fatalf("UnknownOutcomeReason = %q", got)
	}
}

// A 429 must never be retried before its Retry-After, whatever the jitter.
func TestPolicy_Decide_retryAfterIsAFloor(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	for attempt := 1; attempt < p.MaxAttempts; attempt++ {
		for _, wait := range []time.Duration{time.Second, 3 * time.Second, 45 * time.Second} {
			for _, j := range []float64{0, 0.3, 0.99} {
				d := p.Decide(attempt, Result{Kind: RateLimited, Status: 429, RetryAfter: wait}, j)
				if d.Action != Retry || d.Delay < wait {
					t.Fatalf("attempt %d, Retry-After %v, jitter %v: %+v", attempt, wait, j, d)
				}
			}
		}
	}
}

// The retries alone must ride out an outage of a few minutes: the waits of the default policy add up to between two
// and four minutes (equal jitter: each wait is between half of its full length and the full length).
func TestDefaultPolicy_retryWindowIsAFewMinutes(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	var shortest, longest time.Duration
	for attempt := 1; attempt < p.MaxAttempts; attempt++ { // the waits after attempts 1..4
		shortest += p.Backoff(attempt, 0)
		longest += p.Backoff(attempt, 0.999999)
	}
	if shortest < 110*time.Second || longest > 226*time.Second {
		t.Fatalf("the four waits add up to %v..%v, want about 112s..225s", shortest, longest)
	}
	if got := p.Backoff(p.MaxAttempts-1, 0.999999); got > p.MaxDelay {
		t.Fatalf("a wait of %v exceeds the cap %v", got, p.MaxDelay)
	}
}

func TestPolicy_Decide_totalAttempts(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	tests := []struct {
		name string
		res  Result
		want int
	}{
		{"5xx: five attempts", Result{Kind: Transient, Status: 500}, 5},
		{"429: five attempts", Result{Kind: RateLimited, Status: 429, RetryAfter: time.Second}, 5},
		{"timeouts: five attempts and one confirmation", Result{Kind: Transient, Err: context.DeadlineExceeded}, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			attempts := 0
			for {
				attempts++
				if p.Decide(attempts, tt.res, 0.5).Action == Fail {
					break
				}
				if attempts > 100 {
					t.Fatal("never gave up")
				}
			}
			if attempts != tt.want {
				t.Fatalf("gave up after %d attempts, want %d", attempts, tt.want)
			}
			if attempts > p.MaxTotalAttempts() {
				t.Fatalf("%d attempts exceed MaxTotalAttempts %d", attempts, p.MaxTotalAttempts())
			}
		})
	}
}

// Only a missing answer is ambiguous: an answer, even a bad one, says the message was not accepted.
func TestPolicy_confirms(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	tests := []struct {
		name    string
		attempt int
		res     Result
		want    bool
	}{
		{"timeout on the last attempt", 5, Result{Kind: Transient, Err: context.DeadlineExceeded}, true},
		{"timeout earlier", 3, Result{Kind: Transient, Err: context.DeadlineExceeded}, false},
		{"timeout on the confirmation", 6, Result{Kind: Transient, Err: context.DeadlineExceeded}, false},
		{"500 on the last attempt", 5, Result{Kind: Transient, Status: 500}, false},
		{"429 on the last attempt", 5, Result{Kind: RateLimited, Status: 429}, false},
		{"success", 5, Result{Kind: Delivered, Status: 200}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := p.confirms(tt.attempt, tt.res); got != tt.want {
				t.Fatalf("confirms = %v, want %v", got, tt.want)
			}
		})
	}
	if got := p.MaxTotalAttempts(); got != 6 {
		t.Fatalf("MaxTotalAttempts = %d, want 6", got)
	}
}

func TestDescribe_timeouts(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"deadline exceeded": context.DeadlineExceeded,
		"net timeout":       &net.DNSError{IsTimeout: true},
		"wrapped":           fmtErr(&net.DNSError{IsTimeout: true}),
	} {
		if got := describe(Result{Kind: Transient, Err: err}); got != "timeout" {
			t.Errorf("%s: describe = %q, want timeout", name, got)
		}
	}
	if got := describe(Result{Kind: Transient, Err: errors.New("connection refused")}); !strings.Contains(got, "connection refused") {
		t.Errorf("describe = %q", got)
	}
}

func fmtErr(err error) error { return errors.Join(errors.New("post"), err) }

func TestPolicy_Validate(t *testing.T) {
	t.Parallel()
	good := DefaultPolicy()
	if err := good.Validate(); err != nil {
		t.Fatalf("default policy: %v", err)
	}
	tests := []struct {
		name   string
		change func(*Policy)
	}{
		{"no attempts", func(p *Policy) { p.MaxAttempts = 0 }},
		{"zero base delay would retry at once", func(p *Policy) { p.BaseDelay = 0 }},
		{"max delay below base", func(p *Policy) { p.MaxDelay = p.BaseDelay - 1 }},
		{"zero Retry-After cap would ignore Retry-After", func(p *Policy) { p.MaxRetryAfter = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := good
			tt.change(&p)
			if err := p.Validate(); err == nil {
				t.Fatalf("policy %+v was accepted", p)
			}
		})
	}
}

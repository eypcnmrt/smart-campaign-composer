package mockprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	smsBody   = `{"to":"+900000000001","text":"hello"}`
	emailBody = `{"to":"a@example.com","subject":"s","body":"b"}`
)

func newServer(t *testing.T, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

type reply struct {
	status     int
	retryAfter string
	id         string
}

func post(t *testing.T, base, path, key, body string) reply {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set(HeaderIdempotencyKey, key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("POST %s: %v", path, err)
		return reply{}
	}
	defer resp.Body.Close()
	var out response
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return reply{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"), id: out.ID}
}

func TestSend_validation(t *testing.T) {
	t.Parallel()
	_, ts := newServer(t, Config{})
	tests := []struct {
		name, path, key, body string
		want                  int
	}{
		{"sms ok", "/sms", "k1", smsBody, 200},
		{"email ok", "/email", "k2", emailBody, 200},
		{"missing key", "/sms", "", smsBody, 400},
		{"not json", "/sms", "k3", `nope`, 400},
		{"unknown field", "/sms", "k4", `{"to":"x","text":"y","extra":1}`, 400},
		{"sms without text", "/sms", "k5", `{"to":"x"}`, 400},
		{"email without subject", "/email", "k6", `{"to":"x","body":"b"}`, 400},
		{"email body sent to sms", "/sms", "k7", emailBody, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := post(t, ts.URL, tt.path, tt.key, tt.body).status; got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSend_sameKeyReturnsFirstReplyAndCountsOnce(t *testing.T) {
	t.Parallel()
	s, ts := newServer(t, Config{})
	first := post(t, ts.URL, "/sms", "key-1", smsBody)
	second := post(t, ts.URL, "/sms", "key-1", smsBody)
	other := post(t, ts.URL, "/sms", "key-2", smsBody)
	if first.status != 200 || second.status != 200 || first.id == "" || first.id != second.id {
		t.Fatalf("first %+v, second %+v: the repeated key must return the first reply", first, second)
	}
	if other.id == first.id {
		t.Fatalf("a different key must create a different message")
	}
	st := s.Stats().SMS
	if st.UniqueAccepted != 2 || st.DuplicateKeys != 1 || st.Requests != 3 {
		t.Fatalf("sms stats = %+v, want 2 unique, 1 duplicate, 3 requests", st)
	}
}

func TestSend_channelsAreIndependent(t *testing.T) {
	t.Parallel()
	s, ts := newServer(t, Config{})
	post(t, ts.URL, "/sms", "same", smsBody)
	post(t, ts.URL, "/email", "same", emailBody)
	st := s.Stats()
	if st.SMS.UniqueAccepted != 1 || st.Email.UniqueAccepted != 1 || st.SMS.DuplicateKeys != 0 || st.Email.DuplicateKeys != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestStatsEndpoint(t *testing.T) {
	t.Parallel()
	_, ts := newServer(t, Config{})
	post(t, ts.URL, "/email", "e1", emailBody)
	resp, err := http.Get(ts.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got Stats
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || got.Email.UniqueAccepted != 1 || got.SMS.UniqueAccepted != 0 {
		t.Fatalf("status %d, stats %+v", resp.StatusCode, got)
	}
}

func TestSend_serverError(t *testing.T) {
	t.Parallel()
	s, ts := newServer(t, Config{ErrorRate: 1})
	if got := post(t, ts.URL, "/sms", "k", smsBody); got.status != 500 {
		t.Fatalf("status = %d, want 500", got.status)
	}
	st := s.Stats().SMS
	if st.Errors500 != 1 || st.UniqueAccepted != 0 {
		t.Fatalf("stats = %+v: a 500 must not accept the message", st)
	}
}

func TestSend_throttleSendsRetryAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		retryAfter time.Duration
		want       string
	}{
		{"one second", time.Second, "1"},
		{"two seconds", 2 * time.Second, "2"},
		{"rounded up", 1500 * time.Millisecond, "2"},
		{"never zero", 0, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, ts := newServer(t, Config{ThrottleRate: 1, RetryAfter: tt.retryAfter})
			got := post(t, ts.URL, "/sms", "k", smsBody)
			if got.status != 429 || got.retryAfter != tt.want {
				t.Fatalf("status %d, Retry-After %q, want 429 and %q", got.status, got.retryAfter, tt.want)
			}
			if st := s.Stats().SMS; st.Throttled429 != 1 || st.UniqueAccepted != 0 {
				t.Fatalf("stats = %+v", st)
			}
		})
	}
}

// fakeClock lets a test move time without sleeping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestSend_retryAfterViolationIsCounted(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	s, ts := newServer(t, Config{ThrottleRate: 1, RetryAfter: 2 * time.Second})
	s.now = clock.now

	post(t, ts.URL, "/sms", "early", smsBody)
	clock.advance(time.Second) // less than the 2 s the provider asked for
	post(t, ts.URL, "/sms", "early", smsBody)

	post(t, ts.URL, "/sms", "patient", smsBody)
	clock.advance(2 * time.Second)
	post(t, ts.URL, "/sms", "patient", smsBody)

	if got := s.Stats().SMS.RetryAfterViolations; got != 1 {
		t.Fatalf("violations = %d, want 1 (only the client that came back after 1 s)", got)
	}
}

func TestSend_ownRateLimit(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{t: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	s, ts := newServer(t, Config{RateLimit: 5})
	s.now = clock.now

	var ok, limited int
	for i := range 8 {
		switch post(t, ts.URL, "/sms", string(rune('a'+i)), smsBody).status {
		case 200:
			ok++
		case 429:
			limited++
		}
	}
	if ok != 5 || limited != 3 {
		t.Fatalf("ok = %d, limited = %d, want 5 and 3 within one second", ok, limited)
	}
	// The other channel has its own budget.
	if got := post(t, ts.URL, "/email", "e", emailBody).status; got != 200 {
		t.Fatalf("email status = %d, want 200", got)
	}
	// A second later the window has moved on.
	clock.advance(time.Second)
	if got := post(t, ts.URL, "/sms", "later", smsBody).status; got != 200 {
		t.Fatalf("status after the window = %d, want 200", got)
	}
}

func TestSend_lateReplyStillDelivers(t *testing.T) {
	t.Parallel()
	s, ts := newServer(t, Config{LateRate: 1, LateDelay: 400 * time.Millisecond})

	// A client with a short timeout gives up before the reply...
	short := &http.Client{Timeout: 50 * time.Millisecond}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/sms", strings.NewReader(smsBody))
	req.Header.Set(HeaderIdempotencyKey, "late-1")
	if resp, err := short.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("the client was expected to time out")
	}
	// ...but the message was accepted.
	if st := s.Stats().SMS; st.UniqueAccepted != 1 || st.LateResponses != 1 {
		t.Fatalf("stats = %+v, want the message accepted although the reply was late", st)
	}
	// The retry with the same key gets the first reply at once and is not delivered again.
	started := time.Now()
	retry := post(t, ts.URL, "/sms", "late-1", smsBody)
	if retry.status != 200 || time.Since(started) > 200*time.Millisecond {
		t.Fatalf("retry = %+v after %v, want an immediate 200", retry, time.Since(started))
	}
	if st := s.Stats().SMS; st.UniqueAccepted != 1 || st.DuplicateKeys != 1 {
		t.Fatalf("stats = %+v, want 1 unique and 1 duplicate", st)
	}
}

func TestSend_concurrentSameKeyAcceptsOnce(t *testing.T) {
	t.Parallel()
	s, ts := newServer(t, Config{DelayMin: 5 * time.Millisecond, DelayMax: 20 * time.Millisecond})
	const n = 40
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { ids[i] = post(t, ts.URL, "/sms", "one-key", smsBody).id })
	}
	wg.Wait()
	for _, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("ids = %v, want the same id for every request", ids)
		}
	}
	if st := s.Stats().SMS; st.UniqueAccepted != 1 || st.DuplicateKeys != n-1 {
		t.Fatalf("stats = %+v, want 1 unique and %d duplicates", st, n-1)
	}
}

func TestDecide_reproducible(t *testing.T) {
	t.Parallel()
	cfg := Config{Seed: 7, ErrorRate: 0.2, ThrottleRate: 0.2, LateRate: 0.2, DelayMin: time.Millisecond, DelayMax: 50 * time.Millisecond}
	a, _ := New(cfg, nil)
	b, _ := New(cfg, nil)
	cfg.Seed = 8
	c, _ := New(cfg, nil)

	differs := false
	var throttles, fails int
	for i := range 500 {
		key := "key-" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		da, db, dc := a.decide("sms", key, 0), b.decide("sms", key, 0), c.decide("sms", key, 0)
		if da != db {
			t.Fatalf("same seed, different decision for %s: %+v vs %+v", key, da, db)
		}
		differs = differs || da != dc
		if da.throttle {
			throttles++
		}
		if da.fail {
			fails++
		}
		if da.throttle && da.fail {
			t.Fatalf("a request cannot be both throttled and failed")
		}
		if da.delay < cfg.DelayMin || da.delay > cfg.DelayMax {
			t.Fatalf("delay %v outside the range", da.delay)
		}
	}
	if !differs {
		t.Fatal("a different seed should change some decisions")
	}
	if throttles < 60 || throttles > 140 || fails < 60 || fails > 140 {
		t.Fatalf("throttles = %d, fails = %d of 500, want about 100 each", throttles, fails)
	}
}

func TestDecide_attemptsDifferForTheSameKey(t *testing.T) {
	t.Parallel()
	s, _ := New(Config{Seed: 3, ErrorRate: 0.5}, nil)
	fate := map[bool]bool{}
	for nth := range 40 {
		fate[s.decide("sms", "retry-me", nth).fail] = true
	}
	if len(fate) != 2 {
		t.Fatal("successive attempts with one key must not all share the same fate, or a retry could never succeed")
	}
}

func TestMaxInWindow(t *testing.T) {
	t.Parallel()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(ms ...int) []time.Time {
		out := make([]time.Time, len(ms))
		for i, m := range ms {
			out[i] = base.Add(time.Duration(m) * time.Millisecond)
		}
		return out
	}
	tests := []struct {
		name  string
		times []time.Time
		want  int
	}{
		{"empty", nil, 0},
		{"one", at(0), 1},
		{"all within one second", at(0, 100, 999), 3},
		{"half-open: exactly one second apart is another window", at(0, 1000), 1},
		{"busiest window in the middle", at(0, 2000, 2100, 2200, 3300), 3},
		{"sliding, not fixed buckets", at(900, 950, 1000, 1100, 1950), 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := maxInWindow(tt.times, time.Second); got != tt.want {
				t.Fatalf("maxInWindow = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSend_delayHonoursClientCancel(t *testing.T) {
	t.Parallel()
	_, ts := newServer(t, Config{DelayMin: 2 * time.Second, DelayMax: 2 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/sms", strings.NewReader(smsBody))
	req.Header.Set(HeaderIdempotencyKey, "slow")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("expected the client to time out")
	}
}

func TestAcceptedEndpoint(t *testing.T) {
	t.Parallel()
	_, ts := newServer(t, Config{})
	post(t, ts.URL, "/sms", "b-key", smsBody)
	post(t, ts.URL, "/sms", "a-key", smsBody)

	get := func(path string) (int, []string) {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var keys []string
		_ = json.NewDecoder(resp.Body).Decode(&keys)
		return resp.StatusCode, keys
	}
	if status, keys := get("/accepted/sms"); status != 200 || len(keys) != 2 || keys[0] != "a-key" || keys[1] != "b-key" {
		t.Fatalf("sms: status %d, keys %v, want the two keys sorted", status, keys)
	}
	if status, keys := get("/accepted/email"); status != 200 || keys == nil || len(keys) != 0 {
		t.Fatalf("email: status %d, keys %#v, want an empty array", status, keys)
	}
	if status, _ := get("/accepted/fax"); status != 404 {
		t.Fatalf("unknown channel: status %d, want 404", status)
	}
}

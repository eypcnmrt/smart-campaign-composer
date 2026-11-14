//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/retry"
	"smartcampaign/internal/testdb"
	"smartcampaign/internal/worker"
)

// A test environment: a migrated database, a mock provider in this process and a queue.
type env struct {
	pool     *pgxpool.Pool
	q        *queue.Queue
	provider *mockprovider.Server
	url      string
}

func newEnv(t *testing.T, cfg mockprovider.Config) *env {
	t.Helper()
	provider, err := mockprovider.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(provider.Handler())
	t.Cleanup(ts.Close)
	pool := testdb.New(t)
	return &env{pool: pool, q: queue.New(pool), provider: provider, url: ts.URL}
}

// campaign adds users 1..n and an approved campaign over the channels, and enqueues it.
func (e *env) campaign(t *testing.T, users int, channels ...string) int64 {
	t.Helper()
	testdb.AddUsers(t, e.pool, users)
	id := testdb.AddApprovedCampaign(t, e.pool, channels...)
	if _, err := e.q.Enqueue(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	return id
}

// fastRetry keeps the backoff short so chaos tests finish quickly; the attempt limit is the real one.
func fastRetry() retry.Policy {
	return retry.Policy{MaxAttempts: 5, BaseDelay: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond, MaxRetryAfter: time.Minute}
}

// start runs a worker until the returned stop function is called, which waits for Run to return.
func (e *env) start(t *testing.T, cfg worker.Config) (w *worker.Worker, stop func() error) {
	t.Helper()
	w, err := worker.New(e.q, worker.NewHTTPSender(e.url), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	var stopped bool
	stop = func() error {
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(30 * time.Second):
			t.Error("worker did not stop within 30s of the cancel")
			return nil
		}
	}
	t.Cleanup(func() { _ = stop() })
	return w, stop
}

// waitFinished waits until the campaign is completed (with or without failures) and returns its status.
func (e *env) waitFinished(t *testing.T, id int64, within time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(within)
	var status string
	for time.Now().Before(deadline) {
		if err := e.pool.QueryRow(context.Background(), `SELECT status FROM campaigns WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(status, "completed") {
			return status
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("campaign %d is still %q after %v; deliveries: %s", id, status, within, e.summary(t))
	return ""
}

// summary describes the delivery states for failure messages.
func (e *env) summary(t *testing.T) string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT channel, status, count(*) FROM deliveries GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var ch, st string
		var n int
		if err := rows.Scan(&ch, &st, &n); err != nil {
			return err.Error()
		}
		parts = append(parts, fmt.Sprintf("%s/%s=%d", ch, st, n))
	}
	return strings.Join(parts, " ")
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// keys returns the sorted idempotency keys of the channel's deliveries in the status.
func (e *env) keys(t *testing.T, channel, status string) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT idempotency_key::text FROM deliveries WHERE channel = $1 AND status = $2`, channel, status)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// requireProviderMatchesDatabase is the central claim: what the provider accepted is exactly what the
// database calls sent, key for key, and no key was accepted twice.
func (e *env) requireProviderMatchesDatabase(t *testing.T) {
	t.Helper()
	stats := e.provider.Stats()
	for channel, st := range map[string]mockprovider.ChannelStats{"sms": stats.SMS, "email": stats.Email} {
		sent := e.keys(t, channel, "sent")
		accepted := e.provider.AcceptedKeys(channel)
		if st.UniqueAccepted != len(sent) {
			t.Errorf("%s: provider accepted %d unique keys, the database has %d sent", channel, st.UniqueAccepted, len(sent))
		}
		if !slices.Equal(accepted, sent) {
			t.Errorf("%s: accepted keys differ from the idempotency keys of the sent deliveries (provider %d, database %d)", channel, len(accepted), len(sent))
		}
	}
}

func TestWorker_exactlyOnceUnderChaos(t *testing.T) {
	e := newEnv(t, mockprovider.Config{
		Seed: 11, DelayMin: time.Millisecond, DelayMax: 8 * time.Millisecond,
		ErrorRate: 0.12, ThrottleRate: 0.10, RetryAfter: time.Second,
		LateRate: 0.05, LateDelay: 700 * time.Millisecond,
	})
	id := e.campaign(t, 400, "sms", "email") // 800 deliveries
	w, stop := e.start(t, worker.Config{
		RatePerSecond: 300, RequestTimeout: 300 * time.Millisecond, Lease: 15 * time.Second,
		MaintenanceInterval: 100 * time.Millisecond, Retry: fastRetry(),
	})

	status := e.waitFinished(t, id, 90*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	sent := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent'`)
	failed := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'failed'`)
	if sent+failed != 800 {
		t.Fatalf("sent %d + failed %d != 800; %s", sent, failed, e.summary(t))
	}
	if wantFailures := failed > 0; wantFailures != (status == "completed_with_failures") {
		t.Fatalf("campaign is %q with %d failed deliveries", status, failed)
	}
	if failed > 800/50 { // five attempts at about a 22 % rejection rate: well under 1 %
		t.Fatalf("%d deliveries failed, far more than five bad attempts in a row would explain", failed)
	}
	e.requireProviderMatchesDatabase(t)

	stats := e.provider.Stats()
	for channel, st := range map[string]mockprovider.ChannelStats{"sms": stats.SMS, "email": stats.Email} {
		if st.Errors500 == 0 || st.Throttled429 == 0 || st.LateResponses == 0 {
			t.Errorf("%s: the chaos did not happen (%+v); the test proves nothing", channel, st)
		}
		// a message accepted behind a client timeout must come back as a duplicate, not as a second message
		if st.DuplicateKeys < st.LateResponses {
			t.Errorf("%s: %d late replies but only %d duplicate keys; retries after a timeout are missing", channel, st.LateResponses, st.DuplicateKeys)
		}
		if st.RetryAfterViolations != 0 {
			t.Errorf("%s: %d retries came before their Retry-After", channel, st.RetryAfterViolations)
		}
	}
	if c := w.Counters(); c.Sent != int64(sent) || c.Failed != int64(failed) || c.Retried == 0 {
		t.Errorf("worker counters = %+v, database says sent %d failed %d", c, sent, failed)
	}
}

func TestWorker_perChannelRateStaysUnderTheCap(t *testing.T) {
	e := newEnv(t, mockprovider.Config{DelayMin: time.Millisecond, DelayMax: 4 * time.Millisecond})
	id := e.campaign(t, 350, "sms", "email")
	started := time.Now()
	_, stop := e.start(t, worker.Config{}) // defaults: 100 per second and channel, burst 1

	e.waitFinished(t, id, 60*time.Second)
	elapsed := time.Since(started)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	stats := e.provider.Stats()
	for channel, st := range map[string]mockprovider.ChannelStats{"sms": stats.SMS, "email": stats.Email} {
		if st.MaxRequestsPerSecond > 100 {
			t.Errorf("%s: %d requests arrived within one second, the cap is 100", channel, st.MaxRequestsPerSecond)
		}
		if st.MaxRequestsPerSecond < 80 {
			t.Errorf("%s: busiest second had only %d requests; the worker is not using its budget", channel, st.MaxRequestsPerSecond)
		}
		if st.UniqueAccepted != 350 {
			t.Errorf("%s: %d accepted, want 350", channel, st.UniqueAccepted)
		}
	}
	// 350 messages per channel at no more than 100 per second need at least 3.5 s; the two channels run in parallel.
	if elapsed < 3*time.Second {
		t.Errorf("350 messages per channel took only %v", elapsed)
	}
	e.requireProviderMatchesDatabase(t)
}

func TestWorker_honoursRetryAfter(t *testing.T) {
	e := newEnv(t, mockprovider.Config{Seed: 5, ThrottleRate: 0.35, RetryAfter: time.Second})
	id := e.campaign(t, 60, "sms")
	// The backoff is tiny, so only the Retry-After can keep a retry from coming early.
	_, stop := e.start(t, worker.Config{
		RatePerSecond: 200, Retry: retry.Policy{MaxAttempts: 5, BaseDelay: 5 * time.Millisecond, MaxDelay: 20 * time.Millisecond, MaxRetryAfter: time.Minute},
		MaintenanceInterval: 100 * time.Millisecond,
	})
	e.waitFinished(t, id, 60*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	st := e.provider.Stats().SMS
	if st.Throttled429 < 5 {
		t.Fatalf("only %d throttled requests; the scenario did not happen", st.Throttled429)
	}
	if st.RetryAfterViolations != 0 {
		t.Fatalf("%d of %d 429s were retried before their Retry-After", st.RetryAfterViolations, st.Throttled429)
	}
	e.requireProviderMatchesDatabase(t)
}

// A worker died after claiming deliveries: some had already reached the provider, some had not.
func TestWorker_recoversExpiredLeases(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	id := e.campaign(t, 40, "sms")

	// The "crashed worker" claimed 20 deliveries; its leases ran out.
	ctx := context.Background()
	var crashed []queue.Delivery
	for range 20 {
		d, ok, err := e.q.Claim(ctx, "sms", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim = %v, %v", ok, err)
		}
		crashed = append(crashed, d)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE deliveries SET lease_until = now() - interval '1 second' WHERE status = 'sending'`); err != nil {
		t.Fatal(err)
	}
	// Ten of them had reached the provider before the crash (same key the worker uses).
	sender := worker.NewHTTPSender(e.url)
	for _, d := range crashed[:10] {
		res := sender.Send(ctx, worker.SendRequest{Channel: "sms", Key: d.Key(), To: d.Recipient, Body: "sent before the crash"})
		if res.Kind != retry.Delivered {
			t.Fatalf("pre-send: %+v", res)
		}
	}

	_, stop := e.start(t, worker.Config{MaintenanceInterval: 50 * time.Millisecond, Retry: fastRetry()})
	if status := e.waitFinished(t, id, 30*time.Second); status != "completed" {
		t.Fatalf("campaign = %q, want completed; %s", status, e.summary(t))
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent'`); n != 40 {
		t.Fatalf("%d sent, want all 40; %s", n, e.summary(t))
	}
	st := e.provider.Stats().SMS
	if st.UniqueAccepted != 40 {
		t.Fatalf("provider accepted %d messages, want 40: nobody may get two", st.UniqueAccepted)
	}
	if st.DuplicateKeys != 10 {
		t.Fatalf("provider saw %d duplicate keys, want the 10 that were re-sent after the crash", st.DuplicateKeys)
	}
	e.requireProviderMatchesDatabase(t)
}

func TestWorker_expiredLeaseOnTheConfirmationAttemptFails(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	id := e.campaign(t, 6, "sms")
	ctx := context.Background()
	d, ok, err := e.q.Claim(ctx, "sms", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE deliveries SET attempts = 6, lease_until = now() - interval '1 second' WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}

	_, stop := e.start(t, worker.Config{MaintenanceInterval: 50 * time.Millisecond, Retry: fastRetry()})
	if status := e.waitFinished(t, id, 30*time.Second); status != "completed_with_failures" {
		t.Fatalf("campaign = %q, want completed_with_failures; %s", status, e.summary(t))
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if got := e.provider.AcceptedKeys("sms"); slices.Contains(got, d.Key()) {
		t.Fatal("a delivery that used up its attempts must not be sent again")
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE id = $1 AND status = 'failed' AND last_error LIKE 'outcome unknown: %'`, d.ID); n != 1 {
		t.Fatal("the failed delivery must say that its outcome is unknown")
	}
}

// silentProvider reads each request and closes the connection without a reply, like a provider that dies mid-request.
// It records the requests per idempotency key and when they arrived.
type silentProvider struct {
	srv      *httptest.Server
	mu       sync.Mutex
	keys     map[string]int
	arrivals []time.Time
}

func newSilentProvider(t *testing.T) *silentProvider {
	t.Helper()
	p := &silentProvider{keys: map[string]int{}}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		p.mu.Lock()
		p.keys[r.Header.Get("Idempotency-Key")]++
		p.arrivals = append(p.arrivals, time.Now())
		p.mu.Unlock()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijacking the connection: %v", err)
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// requestsPerKey copies the per-key request counts.
func (p *silentProvider) requestsPerKey() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.keys)
}

// maxPerSecond is the largest number of requests that arrived in any half-open second [t, t+1s).
func (p *silentProvider) maxPerSecond() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	best, lo := 0, 0
	for hi, at := range p.arrivals {
		for at.Sub(p.arrivals[lo]) >= time.Second {
			lo++
		}
		best = max(best, hi-lo+1)
	}
	return best
}

// The fifth attempt and the confirmation request both get no answer at all: the delivery fails, its last_error says
// that the outcome is unknown (not just "gave up"), the provider saw exactly two requests with the same key, and the
// confirmation request went through the rate limiter like every other send.
func TestWorker_noAnswerToTheConfirmationFailsWithUnknownOutcome(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	silent := newSilentProvider(t)
	e.url = silent.srv.URL
	const users = 150
	id := e.campaign(t, users, "sms")
	// four attempts were already used up: the next claim is the fifth, the one after it the confirmation request
	if _, err := e.pool.Exec(context.Background(), "UPDATE deliveries SET attempts = 4"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, stop := e.start(t, worker.Config{RatePerSecond: 100, Retry: fastRetry(), MaintenanceInterval: 50 * time.Millisecond})
	status := e.waitFinished(t, id, 60*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	if status != "completed_with_failures" {
		t.Fatalf("campaign = %q, want completed_with_failures; %s", status, e.summary(t))
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'failed' AND attempts = 6
		AND last_error LIKE 'outcome unknown: attempt 5 and the confirmation request with the same idempotency key (attempt 6) both ended without an answer (%); the provider may have delivered the message'`); n != users {
		t.Fatalf("%d deliveries failed with an \"outcome unknown\" reason after the confirmation request, want %d; %s", n, users, e.summary(t))
	}
	perKey := silent.requestsPerKey()
	if len(perKey) != users {
		t.Fatalf("the provider saw %d distinct keys, want %d", len(perKey), users)
	}
	for key, n := range perKey {
		if n != 2 {
			t.Fatalf("key %s was sent %d times, want the 5th attempt and one confirmation request", key, n)
		}
	}
	// 2 x 150 requests: only a limiter that also paces the confirmation requests keeps every second at or under 100
	if got := silent.maxPerSecond(); got > 100 {
		t.Fatalf("%d requests arrived within one second, the cap is 100", got)
	}
	if took := time.Since(started); took < 2500*time.Millisecond {
		t.Fatalf("300 requests at 100 per second took only %v", took)
	}
}

func TestWorker_noSendAfterConsentIsWithdrawn(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	id := e.campaign(t, 50, "sms", "email")
	// Between approval and sending, users 1-10 withdraw SMS consent and 11-15 withdraw e-mail consent.
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE users SET sms_consent = false WHERE id <= 10`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE users SET email_consent = false WHERE id BETWEEN 11 AND 15`); err != nil {
		t.Fatal(err)
	}
	withdrawn := map[string][]string{}
	for channel, cond := range map[string]string{"sms": "user_id <= 10", "email": "user_id BETWEEN 11 AND 15"} {
		rows, err := e.pool.Query(ctx, `SELECT idempotency_key::text FROM deliveries WHERE channel = $1 AND `+cond, channel)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				t.Fatal(err)
			}
			withdrawn[channel] = append(withdrawn[channel], key)
		}
		rows.Close()
	}

	_, stop := e.start(t, worker.Config{RatePerSecond: 200})
	status := e.waitFinished(t, id, 30*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	if status != "completed" {
		t.Fatalf("campaign = %q: skipping a withdrawn consent is not a failure", status)
	}
	for channel, keys := range withdrawn {
		accepted := e.provider.AcceptedKeys(channel)
		for _, key := range keys {
			if slices.Contains(accepted, key) {
				t.Errorf("%s delivery with key %s was sent although the user withdrew consent", channel, key)
			}
		}
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE channel = 'sms' AND status = 'skipped'`); n != 10 {
		t.Errorf("%d sms deliveries skipped, want 10", n)
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE channel = 'email' AND status = 'skipped'`); n != 5 {
		t.Errorf("%d email deliveries skipped, want 5", n)
	}
	if st := e.provider.Stats(); st.SMS.UniqueAccepted != 40 || st.Email.UniqueAccepted != 45 {
		t.Errorf("accepted sms %d (want 40), email %d (want 45)", st.SMS.UniqueAccepted, st.Email.UniqueAccepted)
	}
	e.requireProviderMatchesDatabase(t)
}

// The database is reset (or a second one is used) while the provider keeps its memory: delivery ids start from 1
// again, but the idempotency keys do not repeat, so no new message is mistaken for an old one.
func TestWorker_keysDoNotCollideWhenIdsRestartAgainstALongLivedProvider(t *testing.T) {
	first := newEnv(t, mockprovider.Config{})
	const users = 20
	id1 := first.campaign(t, users, "sms")
	_, stop1 := first.start(t, worker.Config{RatePerSecond: 200})
	first.waitFinished(t, id1, 30*time.Second)
	if err := stop1(); err != nil {
		t.Fatal(err)
	}

	// "the same system after a reset": a fresh database, the same provider
	pool2 := testdb.New(t)
	second := &env{pool: pool2, q: queue.New(pool2), provider: first.provider, url: first.url}
	id2 := second.campaign(t, users, "sms")
	if a, b := first.count(t, `SELECT min(id) FROM deliveries`), second.count(t, `SELECT min(id) FROM deliveries`); a != b {
		t.Fatalf("the two databases do not start at the same id (%d, %d); the test would prove nothing", a, b)
	}
	_, stop2 := second.start(t, worker.Config{RatePerSecond: 200})
	second.waitFinished(t, id2, 30*time.Second)
	if err := stop2(); err != nil {
		t.Fatal(err)
	}

	st := first.provider.Stats().SMS
	if st.UniqueAccepted != 2*users || st.DuplicateKeys != 0 {
		t.Fatalf("provider accepted %d messages with %d duplicates, want %d and 0: ids repeated, keys must not", st.UniqueAccepted, st.DuplicateKeys, 2*users)
	}
	if n := second.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent'`); n != users {
		t.Fatalf("%d deliveries of the second database are sent, want %d", n, users)
	}
}

func TestWorker_permanentErrorIsNotRetried(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	id := e.campaign(t, 5, "sms")
	// The provider rejects an empty recipient with 400.
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET phone = '' WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	_, stop := e.start(t, worker.Config{Retry: fastRetry()})
	status := e.waitFinished(t, id, 30*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	if status != "completed_with_failures" {
		t.Fatalf("campaign = %q, want completed_with_failures", status)
	}
	var attempts int
	var lastError string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT attempts, last_error FROM deliveries WHERE user_id = 3`).Scan(&attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || !strings.Contains(lastError, "permanent error: http 400") {
		t.Fatalf("attempts = %d, last_error = %q; a 400 must fail at the first attempt", attempts, lastError)
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent'`); n != 4 {
		t.Fatalf("%d sent, want the 4 others", n)
	}
	e.requireProviderMatchesDatabase(t)
}

func TestWorker_retriesGiveUpAfterFiveAttempts(t *testing.T) {
	e := newEnv(t, mockprovider.Config{ErrorRate: 1}) // the provider never accepts
	id := e.campaign(t, 4, "email")
	_, stop := e.start(t, worker.Config{Retry: fastRetry(), MaintenanceInterval: 50 * time.Millisecond})
	status := e.waitFinished(t, id, 30*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	if status != "completed_with_failures" {
		t.Fatalf("campaign = %q", status)
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'failed' AND attempts = 5 AND last_error LIKE 'gave up after 5 attempts%'`); n != 4 {
		t.Fatalf("%d deliveries failed after exactly 5 attempts, want 4; %s", n, e.summary(t))
	}
	if st := e.provider.Stats().Email; st.Requests != 20 || st.UniqueAccepted != 0 {
		t.Fatalf("provider saw %d requests and accepted %d, want 20 and 0", st.Requests, st.UniqueAccepted)
	}
}

// On shutdown nothing new is claimed, what is in flight is finished and recorded, and nothing stays "sending".
func TestWorker_gracefulShutdown(t *testing.T) {
	e := newEnv(t, mockprovider.Config{DelayMin: 150 * time.Millisecond, DelayMax: 150 * time.Millisecond})
	id := e.campaign(t, 300, "sms")
	_, stop := e.start(t, worker.Config{RatePerSecond: 100, RequestTimeout: 2 * time.Second, Lease: time.Minute})

	// let it get going: several sends in flight, most deliveries untouched
	deadline := time.Now().Add(20 * time.Second)
	for e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent'`) < 10 {
		if time.Now().After(deadline) {
			t.Fatal("the worker never started sending")
		}
		time.Sleep(20 * time.Millisecond)
	}

	started := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("shutdown took %v; in-flight sends are bounded by the 2s request timeout", took)
	}

	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sending'`); n != 0 {
		t.Fatalf("%d deliveries are still 'sending' after Run returned", n)
	}
	sent := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent'`)
	queued := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'queued'`)
	if queued == 0 || sent+queued != 300 {
		t.Fatalf("sent %d, queued %d: expected the rest to stay queued, none lost", sent, queued)
	}
	e.requireProviderMatchesDatabase(t)
	if got := e.count(t, `SELECT count(*) FROM campaigns WHERE id = $1 AND status = 'sending'`, id); got != 1 {
		t.Fatal("an unfinished campaign must stay 'sending'")
	}

	// A second worker picks up exactly where the first stopped.
	_, stop2 := e.start(t, worker.Config{RatePerSecond: 300})
	e.waitFinished(t, id, 60*time.Second)
	if err := stop2(); err != nil {
		t.Fatal(err)
	}
	if st := e.provider.Stats().SMS; st.UniqueAccepted != 300 {
		t.Fatalf("provider accepted %d, want 300", st.UniqueAccepted)
	}
	e.requireProviderMatchesDatabase(t)
}

// A real HTTP reply that never comes back in time: the message is delivered, the worker times out,
// and the retry with the same key must not deliver it twice.
func TestWorker_timeoutOfADeliveredMessageIsNotResent(t *testing.T) {
	e := newEnv(t, mockprovider.Config{LateRate: 1, LateDelay: 600 * time.Millisecond})
	id := e.campaign(t, 30, "sms")
	_, stop := e.start(t, worker.Config{RequestTimeout: 150 * time.Millisecond, Lease: 15 * time.Second, Retry: fastRetry(), RatePerSecond: 200})
	e.waitFinished(t, id, 60*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	st := e.provider.Stats().SMS
	if st.UniqueAccepted != 30 || st.LateResponses != 30 {
		t.Fatalf("accepted %d, late %d; want 30 and 30", st.UniqueAccepted, st.LateResponses)
	}
	if st.DuplicateKeys != 30 {
		t.Fatalf("%d duplicate keys, want each of the 30 retried exactly once", st.DuplicateKeys)
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent' AND attempts = 2`); n != 30 {
		t.Fatalf("%d deliveries sent on the second attempt, want 30; %s", n, e.summary(t))
	}
	e.requireProviderMatchesDatabase(t)
}

// The message the provider received is the frozen template with the user's values filled in.
func TestWorker_rendersThePersonalisedMessage(t *testing.T) {
	var bodies []string
	var ch = make(chan string, 10)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- r.URL.Path + " " + r.Header.Get("Idempotency-Key") + " " + string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer provider.Close()

	pool := testdb.New(t)
	testdb.AddUsers(t, pool, 1)
	if _, err := pool.Exec(context.Background(), `UPDATE users SET first_name = 'Ayşe', city = 'İzmir'`); err != nil {
		t.Fatal(err)
	}
	id := testdb.AddApprovedCampaign(t, pool, "sms", "email")
	q := queue.New(pool)
	if _, err := q.Enqueue(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	w, err := worker.New(q, worker.NewHTTPSender(provider.URL), worker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	for range 2 {
		select {
		case b := <-ch:
			bodies = append(bodies, b)
		case <-time.After(20 * time.Second):
			t.Fatal("the provider did not receive both messages")
		}
	}
	cancel()
	<-done

	slices.Sort(bodies)
	// "/email" sorts before "/sms"
	wantEmail := `{"body":"Merhaba Ayşe","subject":"Fırsat İzmir","to":"u1@example.com"}`
	wantSMS := `{"text":"Merhaba Ayşe, İzmir için fırsat","to":"+900000000001"}`
	if !strings.HasPrefix(bodies[0], "/email ") || !strings.HasSuffix(bodies[0], " "+wantEmail) {
		t.Errorf("email request = %q, want body %s", bodies[0], wantEmail)
	}
	if !strings.HasPrefix(bodies[1], "/sms ") || !strings.HasSuffix(bodies[1], " "+wantSMS) {
		t.Errorf("sms request = %q, want body %s", bodies[1], wantSMS)
	}
}

// The fifth attempt times out although the provider accepted the message (its reply was late): that is not a
// failure. One confirmation attempt with the same key finds the message and the delivery is sent, not failed.
func TestWorker_timeoutOnTheLastAttemptIsConfirmed(t *testing.T) {
	e := newEnv(t, mockprovider.Config{LateRate: 1, LateDelay: 600 * time.Millisecond})
	id := e.campaign(t, 12, "sms")
	// pretend four attempts were already used up: the next claim is the fifth
	if _, err := e.pool.Exec(context.Background(), "UPDATE deliveries SET attempts = 4"); err != nil {
		t.Fatal(err)
	}
	_, stop := e.start(t, worker.Config{RequestTimeout: 150 * time.Millisecond, Lease: 15 * time.Second, Retry: fastRetry(), RatePerSecond: 200})
	status := e.waitFinished(t, id, 60*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	if status != "completed" {
		t.Fatalf("campaign = %q, want completed: nothing failed; %s", status, e.summary(t))
	}
	if n := e.count(t, "SELECT count(*) FROM deliveries WHERE status = 'sent' AND attempts = 6"); n != 12 {
		t.Fatalf("%d deliveries sent by the confirmation attempt, want 12; %s", n, e.summary(t))
	}
	if st := e.provider.Stats().SMS; st.UniqueAccepted != 12 || st.DuplicateKeys != 12 {
		t.Fatalf("provider accepted %d, duplicates %d; want 12 and 12", st.UniqueAccepted, st.DuplicateKeys)
	}
	e.requireProviderMatchesDatabase(t)
}

// An answer is not ambiguous: a 500 on the last attempt fails the delivery, there is no sixth attempt.
func TestWorker_serverErrorOnTheLastAttemptFailsWithoutConfirmation(t *testing.T) {
	e := newEnv(t, mockprovider.Config{ErrorRate: 1})
	id := e.campaign(t, 3, "email")
	if _, err := e.pool.Exec(context.Background(), "UPDATE deliveries SET attempts = 4"); err != nil {
		t.Fatal(err)
	}
	_, stop := e.start(t, worker.Config{Retry: fastRetry()})
	status := e.waitFinished(t, id, 30*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if status != "completed_with_failures" {
		t.Fatalf("campaign = %q", status)
	}
	if n := e.count(t, "SELECT count(*) FROM deliveries WHERE status = 'failed' AND attempts = 5"); n != 3 {
		t.Fatalf("%d deliveries failed at attempt 5, want 3; %s", n, e.summary(t))
	}
}

// A campaign whose stored content cannot be read must fail its deliveries at once and say why, not wait for
// leases to expire again and again.
func TestWorker_unusableContentFailsWithItsCause(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	id := e.campaign(t, 4, "sms")
	if _, err := e.pool.Exec(context.Background(), `UPDATE campaigns SET content = '[1, 2]'::jsonb WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, stop := e.start(t, worker.Config{Retry: fastRetry(), MaintenanceInterval: 50 * time.Millisecond})
	status := e.waitFinished(t, id, 30*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	if status != "completed_with_failures" {
		t.Fatalf("campaign = %q", status)
	}
	if time.Since(started) > 20*time.Second {
		t.Fatalf("took %v: the deliveries must fail at once, not after lease expiry", time.Since(started))
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'failed' AND attempts = 1 AND last_error LIKE 'unusable campaign content%'`); n != 4 {
		t.Fatalf("%d deliveries failed with the cause, want 4; %s", n, e.summary(t))
	}
	if got := e.provider.Stats().SMS.Requests; got != 0 {
		t.Fatalf("the provider got %d requests, want none", got)
	}
}

// The channel locks held in this test's database (advisory locks of the queue's class, 0x5343).
func (e *env) channelLocks(t *testing.T) int {
	t.Helper()
	return e.count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND objsubid = 2 AND classid::bigint = 21315
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`)
}

func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("after %v: %s", within, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// With two workers on one database only one of them serves a channel, so the 100 per second cap still holds at the
// provider; the other waits, and takes the channel over when the first one stops.
func TestWorker_twoWorkersKeepTheChannelCapAndHandOver(t *testing.T) {
	e := newEnv(t, mockprovider.Config{DelayMin: time.Millisecond, DelayMax: 4 * time.Millisecond})
	const users = 450 // per channel
	id := e.campaign(t, users, "sms", "email")
	cfg := worker.Config{LockRetryInterval: 100 * time.Millisecond} // the default rate: 100 per second and channel

	w1, stop1 := e.start(t, cfg)
	eventually(t, 10*time.Second, "the first worker did not take both channel locks", func() bool { return e.channelLocks(t) == 2 })
	w2, stop2 := e.start(t, cfg)

	eventually(t, 20*time.Second, "the first worker never got going", func() bool { return w1.Counters().Sent >= 100 })
	if got := w2.Counters().Sent; got != 0 {
		t.Fatalf("the second worker sent %d messages while the first one held the locks", got)
	}
	if n := e.channelLocks(t); n != 2 {
		t.Fatalf("%d channel locks held with two workers running, want one per channel", n)
	}

	// hand-over: the first worker stops (its in-flight sends finish) and the second takes both channels
	if err := stop1(); err != nil {
		t.Fatal(err)
	}
	e.waitFinished(t, id, 60*time.Second)
	if err := stop2(); err != nil {
		t.Fatal(err)
	}

	sent1, sent2 := w1.Counters().Sent, w2.Counters().Sent
	if sent2 == 0 {
		t.Fatal("the second worker never took over")
	}
	if total := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent'`); int64(total) != sent1+sent2 || total != 2*users {
		t.Fatalf("database has %d sent, workers report %d + %d, want %d", total, sent1, sent2, 2*users)
	}
	stats := e.provider.Stats()
	for channel, st := range map[string]mockprovider.ChannelStats{"sms": stats.SMS, "email": stats.Email} {
		if st.MaxRequestsPerSecond > 100 {
			t.Errorf("%s: %d requests arrived within one second with two workers, the cap is 100", channel, st.MaxRequestsPerSecond)
		}
		if st.UniqueAccepted != users || st.DuplicateKeys != 0 {
			t.Errorf("%s: provider accepted %d with %d duplicates, want %d and 0", channel, st.UniqueAccepted, st.DuplicateKeys, users)
		}
	}
	if n := e.channelLocks(t); n != 0 {
		t.Errorf("%d channel locks still held after both workers stopped", n)
	}
	e.requireProviderMatchesDatabase(t)
}

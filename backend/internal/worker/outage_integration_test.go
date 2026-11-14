//go:build integration

package worker_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/worker"
)

// outageProvider puts a provider outage in front of the mock provider: in "drop" mode it closes every connection
// without a reply, in "503" mode it answers 503, otherwise the request reaches the mock provider.
type outageProvider struct {
	inner http.Handler
	mode  atomic.Int32 // outageUp, outageDrop or outage503
	down  atomic.Int64 // requests that arrived while it was down
}

const (
	outageUp int32 = iota
	outageDrop
	outage503
)

func (p *outageProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch p.mode.Load() {
	case outageDrop:
		p.down.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	case outage503:
		p.down.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		p.inner.ServeHTTP(w, r)
	}
}

// status is the worker's last report of the channel, if it made one.
func (e *env) status(t *testing.T, channel string) (queue.ChannelStatus, bool) {
	t.Helper()
	all, err := e.q.ChannelStatuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s, ok := all[channel]
	return s, ok
}

// The provider is away for 20 seconds (10 s of dropped connections, then 10 s of 503): no delivery may fail. With the
// short backoff of fastRetry, five attempts are used up within a second, so only the circuit breaker, which stops the
// claiming and gives the attempts of the requests that hit the outage back, can get this campaign through.
func TestWorker_aProviderOutageOf20SecondsFailsNothing(t *testing.T) {
	e := newEnv(t, mockprovider.Config{DelayMin: time.Millisecond, DelayMax: 4 * time.Millisecond})
	out := &outageProvider{inner: e.provider.Handler()}
	out.mode.Store(outageDrop)
	srv := httptest.NewServer(out)
	t.Cleanup(srv.Close)
	e.url = srv.URL

	const users = 300 // per channel
	id := e.campaign(t, users, "sms", "email")
	started := time.Now()
	w, stop := e.start(t, worker.Config{
		Retry:               fastRetry(),
		Breaker:             worker.BreakerConfig{Threshold: 5, Cooldown: 300 * time.Millisecond},
		HeartbeatInterval:   100 * time.Millisecond,
		MaintenanceInterval: 100 * time.Millisecond,
	})

	// The circuit opens, and the dashboard can tell why: an open circuit and the reason, from a fresh heartbeat.
	for _, channel := range []string{"sms", "email"} {
		eventually(t, 15*time.Second, channel+": the circuit did not open", func() bool {
			s, ok := e.status(t, channel)
			return ok && s.Circuit != queue.CircuitClosed && s.LastError != "" && s.Age < time.Second
		})
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'failed'`); n != 0 {
		t.Fatalf("%d deliveries failed during the outage", n)
	}

	time.Sleep(time.Until(started.Add(10 * time.Second)))
	out.mode.Store(outage503)
	time.Sleep(time.Until(started.Add(20 * time.Second)))
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'failed'`); n != 0 {
		t.Fatalf("%d deliveries failed by the end of the outage; %s", n, e.summary(t))
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent'`); n != 0 {
		t.Fatalf("%d deliveries were sent while the provider was down", n)
	}
	out.mode.Store(outageUp)

	status := e.waitFinished(t, id, 60*time.Second)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	if status != "completed" {
		t.Fatalf("campaign = %q, want completed; %s", status, e.summary(t))
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE status = 'sent'`); n != 2*users {
		t.Fatalf("%d sent, want %d; %s", n, 2*users, e.summary(t))
	}
	if n := e.count(t, `SELECT max(attempts) FROM deliveries`); n > 5 {
		t.Fatalf("a delivery used %d attempts: the outage burnt attempts it should have been refunded", n)
	}
	e.requireProviderMatchesDatabase(t)
	stats := e.provider.Stats()
	if stats.SMS.UniqueAccepted != users || stats.Email.UniqueAccepted != users {
		t.Fatalf("provider accepted %d sms and %d email, want %d each", stats.SMS.UniqueAccepted, stats.Email.UniqueAccepted, users)
	}
	// While it was down the breaker held the requests back: a threshold's worth, then one probe per cooldown and
	// channel (about 70 each), far fewer than the 600 deliveries.
	if down := out.down.Load(); down < 10 || down > 400 {
		t.Fatalf("%d requests reached the provider while it was down, want a few probes (10..400)", down)
	}
	if c := w.Counters(); c.Failed != 0 || c.HeartbeatErrors != 0 {
		t.Fatalf("worker counters = %+v", c)
	}
	// after the recovery the heartbeat says so
	for _, channel := range []string{"sms", "email"} {
		eventually(t, 5*time.Second, channel+": the circuit did not report closed", func() bool {
			s, _ := e.status(t, channel)
			return s.Circuit == queue.CircuitClosed && s.LastError == ""
		})
	}
}

func TestWorker_heartbeatReportsTheChannelsAndStopsWithTheWorker(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	_, stop := e.start(t, worker.Config{HeartbeatInterval: 100 * time.Millisecond})

	for _, channel := range []string{"sms", "email"} {
		eventually(t, 10*time.Second, channel+": no heartbeat", func() bool {
			s, ok := e.status(t, channel)
			return ok && s.Circuit == queue.CircuitClosed && s.LastError == ""
		})
		// the configured cap (the default here) is part of the report, for the dashboard's limit line
		if s, _ := e.status(t, channel); s.RatePerSecond != 100 {
			t.Fatalf("%s: reported rate = %v, want the configured 100", channel, s.RatePerSecond)
		}
	}
	first, _ := e.status(t, "sms")
	eventually(t, 5*time.Second, "the heartbeat does not move", func() bool {
		s, _ := e.status(t, "sms")
		return s.UpdatedAt.After(first.UpdatedAt)
	})

	if err := stop(); err != nil {
		t.Fatal(err)
	}
	frozen, _ := e.status(t, "sms")
	time.Sleep(500 * time.Millisecond)
	later, _ := e.status(t, "sms")
	if !later.UpdatedAt.Equal(frozen.UpdatedAt) {
		t.Fatal("the heartbeat went on after the worker stopped")
	}
	if later.Age < 400*time.Millisecond {
		t.Fatalf("age of the stopped worker's heartbeat = %v after 500ms: the dashboard could not tell it is gone", later.Age)
	}
}

// A worker that is waiting for another one's channel lock reports nothing for that channel, and starts to report once it
// takes the channel over, so two workers never overwrite each other's status.
func TestWorker_aStandbyWorkerDoesNotReportOnTheChannel(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	ctx := context.Background()
	var held []*queue.ChannelLock
	for _, channel := range []string{"sms", "email"} {
		l, err := e.q.LockChannel(ctx, channel) // "the other worker"
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, l)
	}
	_, stop := e.start(t, worker.Config{HeartbeatInterval: 50 * time.Millisecond, LockRetryInterval: 100 * time.Millisecond})

	time.Sleep(700 * time.Millisecond)
	if all, err := e.q.ChannelStatuses(ctx); err != nil || len(all) != 0 {
		t.Fatalf("a standby worker wrote %v (%v)", all, err)
	}

	for _, l := range held { // the other worker goes away
		if err := l.Release(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, channel := range []string{"sms", "email"} {
		eventually(t, 5*time.Second, channel+": the standby did not take over and report", func() bool {
			_, ok := e.status(t, channel)
			return ok
		})
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

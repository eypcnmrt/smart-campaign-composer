//go:build integration

package campaign_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"smartcampaign/internal/campaign"
)

// bigCampaign approves a campaign that reaches `n` users (so about 2n deliveries) and returns its id.
func bigCampaign(t *testing.T, e *env, n int) int64 {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO users (first_name, email, phone, city, language, total_orders, total_spent_kurus, sms_consent, email_consent)
		SELECT 'U' || g, 'x' || g || '@example.com', '+90000' || lpad(g::text, 7, '0'), 'Ankara', 'tr', 0, 0, true, true FROM generate_series(1000, 999 + $1::int) g`, n); err != nil {
		t.Fatal(err)
	}
	c := e.withContent(t, e.withSegment(t, everyoneRule))
	if _, err := e.store.Approve(context.Background(), c.ID, c.Version, true); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// The stats of one request come from one snapshot: while the worker records messages as sent, the counts of a channel, its
// timeline and the summary must agree with each other. Without a single REPEATABLE READ transaction the counts query and the
// timeline query see different moments, and the sent count is behind the sum of the bars.
func TestStore_statsAreOneSnapshot(t *testing.T) {
	e := newEnv(t)
	id := bigCampaign(t, e, 2000)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var written atomic.Int64
	wg.Add(1)
	go func() { // the worker: one message at a time, each its own transaction
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			tag, err := e.pool.Exec(context.Background(), `UPDATE deliveries SET status = 'sent', attempts = 1, sent_at = clock_timestamp()
				WHERE id = (SELECT id FROM deliveries WHERE campaign_id = $1 AND status = 'queued' ORDER BY id LIMIT 1)`, id)
			if err != nil || tag.RowsAffected() == 0 {
				return
			}
			written.Add(1)
		}
	}()

	deadline := time.Now().Add(4 * time.Second)
	reads := 0
	for time.Now().Before(deadline) && written.Load() < 3000 {
		st, err := e.store.Stats(context.Background(), e.q, id, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		reads++
		sent, bars := 0, 0
		for ch, c := range st.Channels {
			barsOfChannel := 0
			for _, n := range c.Timeline.Accepted {
				barsOfChannel += n
			}
			if barsOfChannel != c.Sent {
				t.Fatalf("read %d: %s has %d sent but its timeline adds up to %d: the reads are not one snapshot", reads, ch, c.Sent, barsOfChannel)
			}
			sent += c.Sent
			bars += barsOfChannel
		}
		if st.Summary.TotalAttempts != sent {
			t.Fatalf("read %d: %d messages sent, one attempt each, but the summary counts %d attempts", reads, sent, st.Summary.TotalAttempts)
		}
	}
	close(stop)
	wg.Wait()
	if reads < 5 || written.Load() < 100 {
		t.Fatalf("the test did not overlap reads and writes: %d reads, %d messages written", reads, written.Load())
	}
	t.Logf("%d reads while %d messages were recorded, every one consistent", reads, written.Load())
}

// Measures the time of a stats request (what the dashboard polls every second) on a campaign of about 6,000 deliveries.
// The numbers go to the log (go test -run StatsLatency -v); the assertion is only a loose guard.
func TestStore_statsLatency(t *testing.T) {
	e := newEnv(t)
	id := bigCampaign(t, e, 3000)
	if _, err := e.pool.Exec(context.Background(), `UPDATE deliveries SET status = 'sent', attempts = 1 + id % 2, sent_at = now() - (id % 20) * interval '1 second' WHERE campaign_id = $1 AND id % 3 <> 0`, id); err != nil {
		t.Fatal(err)
	}
	const runs = 60
	var durs []time.Duration
	for i := 0; i < runs+5; i++ {
		start := time.Now()
		if _, err := e.store.Stats(context.Background(), e.q, id, time.Minute); err != nil {
			t.Fatal(err)
		}
		if i >= 5 { // the first calls warm the pool
			durs = append(durs, time.Since(start))
		}
	}
	slices.Sort(durs)
	median, p95 := durs[len(durs)/2], durs[len(durs)*95/100]
	t.Logf("stats latency over %d requests: median %v, p95 %v, max %v", runs, median, p95, durs[len(durs)-1])
	if median > time.Second {
		t.Fatalf("median stats latency %v is far above anything the polling can live with", median)
	}
	_ = campaign.StatsSQL
}

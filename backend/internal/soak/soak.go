// Package soak drives a long, chaotic run against the real stack (worker and mock provider in their
// containers) and checks the delivery guarantees at the end: nobody got a message twice, the rate cap
// held and every delivery reached a final state.
//
// The driver inserts its own users and one approved campaign, enqueues it, waits until the worker has
// finished it and compares the database with the provider's /stats.
package soak

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/queue"
)

// Config of a soak run.
type Config struct {
	// Users is the number of users, each with both channels, so the run sends 2*Users messages.
	Users int
	// ProviderURL is the base URL of the mock provider (its /stats is read).
	ProviderURL string
	// RateCap is the per-channel limit the run must stay under, requests per second.
	RateCap int
	// Timeout bounds the whole wait for the campaign to finish.
	Timeout time.Duration
}

// ChannelResult is what the database and the provider say about one channel.
type ChannelResult struct {
	Channel                                   string
	Deliveries, Sent, Failed, Skipped, Active int // Active: still queued or sending
	Provider                                  mockprovider.ChannelStats
}

// Verify returns every guarantee the run broke; an empty list means the run passed.
func Verify(rate int, results ...ChannelResult) []string {
	var v []string
	for _, r := range results {
		p := r.Provider
		bad := func(format string, args ...any) { v = append(v, r.Channel+": "+fmt.Sprintf(format, args...)) }
		if r.Active != 0 {
			bad("%d deliveries are not finished", r.Active)
		}
		if r.Sent+r.Failed+r.Skipped+r.Active != r.Deliveries {
			bad("states add up to %d, there are %d deliveries", r.Sent+r.Failed+r.Skipped+r.Active, r.Deliveries)
		}
		if p.UniqueAccepted != r.Sent {
			bad("the provider accepted %d messages but the database has %d sent (a difference is a double or a lost send)", p.UniqueAccepted, r.Sent)
		}
		if p.MaxRequestsPerSecond > rate {
			bad("%d requests arrived within one second, the cap is %d", p.MaxRequestsPerSecond, rate)
		}
		if p.RetryAfterViolations != 0 {
			bad("%d retries came before their Retry-After", p.RetryAfterViolations)
		}
		if p.Errors500+p.Throttled429 == 0 {
			bad("the provider injected no failures, so the run proves nothing about retries")
		}
	}
	return v
}

// Report writes the result table.
func Report(w io.Writer, rate int, results ...ChannelResult) {
	fmt.Fprintf(w, "\n%-6s %10s %7s %7s %8s | %9s %9s %6s %6s %6s %9s %10s\n",
		"", "deliveries", "sent", "failed", "skipped", "accepted", "dup keys", "429", "500", "late", "max req/s", "RA viol.")
	for _, r := range results {
		p := r.Provider
		fmt.Fprintf(w, "%-6s %10d %7d %7d %8d | %9d %9d %6d %6d %6d %9d %10d\n",
			r.Channel, r.Deliveries, r.Sent, r.Failed, r.Skipped,
			p.UniqueAccepted, p.DuplicateKeys, p.Throttled429, p.Errors500, p.LateResponses, p.MaxRequestsPerSecond, p.RetryAfterViolations)
	}
	if v := Verify(rate, results...); len(v) > 0 {
		fmt.Fprintf(w, "\nFAIL\n  - %s\n", strings.Join(v, "\n  - "))
		return
	}
	fmt.Fprintf(w, "\nPASS: provider accepted exactly the %s messages the database calls sent (no double sends), at most %d requests in any second and channel, no early retries\n",
		total(results), rate)
}

func total(results []ChannelResult) string {
	n := 0
	for _, r := range results {
		n += r.Sent
	}
	return fmt.Sprint(n)
}

// Run executes the soak and writes progress and the report to out. It returns an error when setup
// fails, the campaign does not finish in time, or Verify finds a broken guarantee.
func Run(ctx context.Context, pool *pgxpool.Pool, cfg Config, out io.Writer) error {
	if cfg.Users < 1 || cfg.RateCap < 1 || cfg.Timeout <= 0 {
		return fmt.Errorf("invalid soak config %+v", cfg)
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	id, err := prepare(ctx, pool, cfg.Users)
	if err != nil {
		return err
	}
	q := queue.New(pool)
	res, err := q.Enqueue(ctx, id)
	if err != nil {
		return fmt.Errorf("enqueueing the soak campaign: %w", err)
	}
	fmt.Fprintf(out, "soak: campaign %d enqueued, %d sms + %d email deliveries\n", id, res.Created["sms"], res.Created["email"])

	started := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for lastPrint := time.Now(); ; {
		status, err := campaignStatus(ctx, pool, id)
		if err != nil {
			return err
		}
		if strings.HasPrefix(status, "completed") {
			fmt.Fprintf(out, "soak: campaign %s after %s\n", status, time.Since(started).Round(time.Second))
			break
		}
		if time.Since(lastPrint) >= 5*time.Second {
			lastPrint = time.Now()
			counts, _ := summary(ctx, pool, id)
			fmt.Fprintf(out, "soak: %5.0fs  %s\n", time.Since(started).Seconds(), counts)
		}
		select {
		case <-ctx.Done():
			counts, _ := summary(context.WithoutCancel(ctx), pool, id)
			return fmt.Errorf("campaign %d did not finish within %s (%s)", id, cfg.Timeout, counts)
		case <-tick.C:
		}
	}

	stats, err := fetchStats(ctx, cfg.ProviderURL)
	if err != nil {
		return err
	}
	var results []ChannelResult
	for _, ch := range []string{mockprovider.ChannelSMS, mockprovider.ChannelEmail} {
		r, err := channelResult(ctx, pool, id, ch)
		if err != nil {
			return err
		}
		r.Provider = stats.SMS
		if ch == mockprovider.ChannelEmail {
			r.Provider = stats.Email
		}
		results = append(results, r)
	}
	Report(out, cfg.RateCap, results...)
	if v := Verify(cfg.RateCap, results...); len(v) > 0 {
		return fmt.Errorf("%d guarantee(s) broken", len(v))
	}
	return nil
}

// The soak users get ids far above the seed's and a total_orders no seed user reaches, so the campaign's
// rule selects them and nobody else, even in a database that has been seeded.
const (
	soakIDBase = 5_000_000
	soakOrders = 1_000_000

	soakRule    = `{"conditions":[{"type":"attribute","field":"total_orders","op":"gte","value":1000000}],"unsupported_criteria":[],"explanation":"soak users"}`
	soakContent = `{"sms":"Merhaba {{first_name}}, {{city}} için fırsat","email_subject":"Fırsat {{city}}","email_body":"Merhaba {{first_name}}"}`
)

// prepare inserts the users and the approved campaign for the run and returns the campaign id.
func prepare(ctx context.Context, pool *pgxpool.Pool, users int) (int64, error) {
	batch := &pgx.Batch{}
	for i := 1; i <= users; i++ {
		id := soakIDBase + i
		batch.Queue(`INSERT INTO users (id, first_name, email, phone, city, language, total_orders, total_spent_kurus, sms_consent, email_consent)
			VALUES ($1, $2, $3, $4, 'Ankara', 'tr', $5, 0, true, true) ON CONFLICT DO NOTHING`,
			id, fmt.Sprintf("Soak%d", i), fmt.Sprintf("soak%06d@example.com", i), fmt.Sprintf("+90000%07d", id), soakOrders)
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, fmt.Errorf("inserting soak users: %w", err)
	}
	var id int64
	err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, status, channels, segment_rule, content, approved_at)
		VALUES ('soak', 'approved', ARRAY['sms','email'], $1::jsonb, $2::jsonb, now()) RETURNING id`,
		soakRule, soakContent).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("inserting the soak campaign: %w", err)
	}
	return id, nil
}

func campaignStatus(ctx context.Context, pool *pgxpool.Pool, id int64) (string, error) {
	var s string
	if err := pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, id).Scan(&s); err != nil {
		return "", fmt.Errorf("reading campaign %d: %w", id, err)
	}
	return s, nil
}

func summary(ctx context.Context, pool *pgxpool.Pool, id int64) (string, error) {
	rows, err := pool.Query(ctx, `SELECT status, count(*) FROM deliveries WHERE campaign_id = $1 GROUP BY 1 ORDER BY 1`, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s=%d", st, n))
	}
	return strings.Join(parts, " "), rows.Err()
}

func channelResult(ctx context.Context, pool *pgxpool.Pool, id int64, channel string) (ChannelResult, error) {
	r := ChannelResult{Channel: channel}
	err := pool.QueryRow(ctx, `SELECT count(*),
			count(*) FILTER (WHERE status = 'sent'),
			count(*) FILTER (WHERE status = 'failed'),
			count(*) FILTER (WHERE status = 'skipped'),
			count(*) FILTER (WHERE status IN ('queued', 'sending'))
		FROM deliveries WHERE campaign_id = $1 AND channel = $2`, id, channel).
		Scan(&r.Deliveries, &r.Sent, &r.Failed, &r.Skipped, &r.Active)
	if err != nil {
		return r, fmt.Errorf("counting %s deliveries: %w", channel, err)
	}
	return r, nil
}

func fetchStats(ctx context.Context, base string) (mockprovider.Stats, error) {
	var s mockprovider.Stats
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/stats", nil)
	if err != nil {
		return s, fmt.Errorf("building the stats request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return s, fmt.Errorf("reading the provider stats: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return s, fmt.Errorf("provider stats answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return s, fmt.Errorf("decoding the provider stats: %w", err)
	}
	return s, nil
}

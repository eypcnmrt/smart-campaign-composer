package campaign

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"smartcampaign/internal/queue"
)

// StatsSQL counts the deliveries of one campaign per channel, status, "was it tried yet" and "was it tried again", and adds up
// the attempts. It is the whole cost of the counts of a stats request: an index-only scan on
// deliveries_campaign_status_channel_idx (campaign_id, status, channel, attempts), a test checks the plan. "retrying" is not a
// stored status: it is a queued delivery with attempts > 0.
const StatsSQL = `SELECT channel, status, attempts > 0 AS tried, attempts > 1 AS retried, count(*), sum(attempts)::int
	FROM deliveries WHERE campaign_id = $1 GROUP BY channel, status, attempts > 0, attempts > 1`

// ChannelCounts are the deliveries of one channel by state, plus what the worker says about the channel.
type ChannelCounts struct {
	Queued   int `json:"queued"`
	Sending  int `json:"sending"`
	Retrying int `json:"retrying"`
	Sent     int `json:"sent"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
	Total    int `json:"total"`
	// Provider tells "the provider is unreachable, waiting" from "the worker is not running".
	Provider ProviderInfo `json:"provider"`
	// RateLimit is the cap the worker works under for this channel, in messages per second, as it reported it; null when it
	// never did (the screen then draws no limit line).
	RateLimit *float64 `json:"rate_limit"`
	// Timeline is how many messages the provider accepted per second (see timelines); null before the approval.
	Timeline *Timeline `json:"timeline"`
}

// Stats is what the dashboard polls.
type Stats struct {
	CampaignID int64                    `json:"campaign_id"`
	Status     State                    `json:"status"`
	Channels   map[string]ChannelCounts `json:"channels"`
	// Summary is null before the approval.
	Summary *SendSummary `json:"summary"`
}

// Provider states, as shown in ChannelCounts.Provider.State.
const (
	// ProviderOK: the worker reported recently and the circuit is closed.
	ProviderOK = "ok"
	// ProviderUnreachable: the worker reported recently and the circuit is open (or probing): the provider does not answer,
	// the deliveries wait and no attempt is used up.
	ProviderUnreachable = "provider_unreachable"
	// ProviderWorkerDown: no report, or an old one: nothing is sending.
	ProviderWorkerDown = "worker_down"
)

// ProviderInfo is the state of one channel as the worker last reported it.
type ProviderInfo struct {
	State   string `json:"state"`
	Circuit string `json:"circuit"` // closed, open, half_open, or unknown when the worker never reported
	// LastError is why the circuit is not closed; empty otherwise.
	LastError string `json:"last_error"`
	// UpdatedAt and AgeSeconds say when the worker last reported (database clock); null when it never did.
	UpdatedAt  *time.Time `json:"updated_at"`
	AgeSeconds *float64   `json:"age_seconds"`
}

// ProviderStatus turns the worker's last report of a channel into what the dashboard shows. A report older than
// staleAfter (or none) means the worker is not running, which wins over what an old report says about the circuit: a
// stale "open" is no information about the provider.
func ProviderStatus(s queue.ChannelStatus, reported bool, staleAfter time.Duration) ProviderInfo {
	if !reported {
		return ProviderInfo{State: ProviderWorkerDown, Circuit: "unknown"}
	}
	age := s.Age.Seconds()
	info := ProviderInfo{Circuit: s.Circuit, LastError: s.LastError, UpdatedAt: &s.UpdatedAt, AgeSeconds: &age}
	switch {
	case s.Age > staleAfter:
		info.State = ProviderWorkerDown
	case s.Circuit != queue.CircuitClosed:
		info.State = ProviderUnreachable
	default:
		info.State = ProviderOK
	}
	return info
}

// reader is what the stats reads need: the queries of a pool or of a transaction.
type reader interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Stats returns the delivery counts of a campaign per channel and the worker's report for each, or ErrNotFound.
// staleAfter is how old a worker report may be before the worker is considered not running.
//
// Every read of the request (the campaign row, the counts, the send times, the timeline and the worker's report) runs in one
// REPEATABLE READ, read-only transaction, so they all see the same moment: a message the worker records meanwhile is in the
// counts and in the chart, or in neither. A read-only snapshot takes no locks and blocks nobody (a poll of one second).
func (s *Store) Stats(ctx context.Context, q *queue.Queue, id int64, staleAfter time.Duration) (Stats, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Stats{}, fmt.Errorf("opening the snapshot for the stats of campaign %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // nothing was written; the rollback only ends the snapshot
	return s.stats(ctx, tx, q, id, staleAfter)
}

func (s *Store) stats(ctx context.Context, db reader, q *queue.Queue, id int64, staleAfter time.Duration) (Stats, error) {
	var status string
	var channels, approved []string
	var approvedAt, completedAt *time.Time
	err := db.QueryRow(ctx, `SELECT status, channels, approved_channels, approved_at, completed_at FROM campaigns WHERE id = $1`, id).
		Scan(&status, &channels, &approved, &approvedAt, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stats{}, ErrNotFound
	}
	if err != nil {
		return Stats{}, fmt.Errorf("loading campaign %d: %w", id, err)
	}
	if approved != nil { // the frozen channels are the ones that were queued
		channels = approved
	}

	out := Stats{CampaignID: id, Status: State(status), Channels: make(map[string]ChannelCounts, len(channels))}
	for _, ch := range channels {
		out.Channels[ch] = ChannelCounts{}
	}

	rows, err := db.Query(ctx, StatsSQL, id)
	if err != nil {
		return Stats{}, fmt.Errorf("counting the deliveries of campaign %d: %w", id, err)
	}
	defer rows.Close()
	var retried, attempts int
	for rows.Next() {
		var channel, st string
		var tried, again bool
		var n, tries int
		if err := rows.Scan(&channel, &st, &tried, &again, &n, &tries); err != nil {
			return Stats{}, fmt.Errorf("counting the deliveries of campaign %d: %w", id, err)
		}
		c := out.Channels[channel]
		switch {
		case st == queue.StatusQueued && !tried:
			c.Queued += n
		case st == queue.StatusQueued:
			c.Retrying += n
		case st == queue.StatusSending:
			c.Sending += n
		case st == queue.StatusSent:
			c.Sent += n
		case st == queue.StatusFailed:
			c.Failed += n
		case st == queue.StatusSkipped:
			c.Skipped += n
		default:
			return Stats{}, fmt.Errorf("campaign %d has a delivery in the unknown status %q", id, st)
		}
		c.Total += n
		out.Channels[channel] = c
		attempts += tries
		if again {
			retried += n
		}
	}
	if err := rows.Err(); err != nil {
		return Stats{}, fmt.Errorf("counting the deliveries of campaign %d: %w", id, err)
	}

	if approvedAt != nil {
		timelines, peak, average, err := s.timelines(ctx, db, id, *approvedAt, completedAt, slices.Collect(maps.Keys(out.Channels)))
		if err != nil {
			return Stats{}, err
		}
		for ch, tl := range timelines {
			c := out.Channels[ch]
			c.Timeline = tl
			out.Channels[ch] = c
		}
		sum := SendSummary{AveragePerSecond: average, PeakPerSecond: peak, RetriedMessages: retried, TotalAttempts: attempts}
		if completedAt != nil {
			d := completedAt.Sub(*approvedAt).Seconds()
			sum.DurationSeconds = &d
		}
		out.Summary = &sum
	}

	reports, err := q.ChannelStatusesFrom(ctx, db)
	if err != nil {
		return Stats{}, err
	}
	for _, ch := range slices.Sorted(maps.Keys(out.Channels)) {
		c := out.Channels[ch]
		report, ok := reports[ch]
		c.Provider = ProviderStatus(report, ok, staleAfter)
		if ok && report.RatePerSecond > 0 {
			rate := report.RatePerSecond
			c.RateLimit = &rate
		}
		out.Channels[ch] = c
	}
	return out, nil
}

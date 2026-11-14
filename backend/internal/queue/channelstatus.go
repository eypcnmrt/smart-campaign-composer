package queue

import (
	"context"
	"fmt"
	"time"
)

// Circuit states as stored in worker_channel_status.circuit.
const (
	CircuitClosed   = "closed"
	CircuitOpen     = "open"
	CircuitHalfOpen = "half_open"
)

// ChannelStatus is the last report of the worker that serves a channel.
type ChannelStatus struct {
	Channel   string
	Circuit   string // CircuitClosed, CircuitOpen or CircuitHalfOpen
	LastError string // why the circuit is not closed; empty when it is closed
	UpdatedAt time.Time
	// RatePerSecond is the cap the worker is configured with for this channel; 0 when it did not say.
	RatePerSecond float64
	// Age is how long ago the row was written, measured by the database clock.
	Age time.Duration
}

// SetChannelStatus writes the heartbeat of a channel: its circuit state and, when the circuit is not closed, the last
// error, and the rate cap the worker works under (messages per second; zero or less is stored as "not reported").
// updated_at is set by the database.
func (q *Queue) SetChannelStatus(ctx context.Context, channel, circuit, lastError string, ratePerSecond float64) error {
	if _, ok := channelLockKey[channel]; !ok {
		return fmt.Errorf("channel status: unknown channel %q", channel)
	}
	var rate *float64 // NULL when the worker did not say
	if ratePerSecond > 0 {
		rate = &ratePerSecond
	}
	if _, err := q.pool.Exec(ctx, `
		INSERT INTO worker_channel_status (channel, circuit, last_error, rate_per_second, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (channel) DO UPDATE
		SET circuit = EXCLUDED.circuit, last_error = EXCLUDED.last_error, rate_per_second = EXCLUDED.rate_per_second,
		    updated_at = EXCLUDED.updated_at`,
		channel, circuit, truncate(lastError, maxErrorText), rate); err != nil {
		return fmt.Errorf("writing the %s channel status: %w", channel, err)
	}
	return nil
}

// ChannelStatuses returns the last report of every channel that has one, by channel. A channel the worker never
// reported is missing from the map.
func (q *Queue) ChannelStatuses(ctx context.Context) (map[string]ChannelStatus, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT channel, circuit, last_error, updated_at, EXTRACT(EPOCH FROM now() - updated_at)::float8, rate_per_second
		FROM worker_channel_status`)
	if err != nil {
		return nil, fmt.Errorf("reading the channel statuses: %w", err)
	}
	defer rows.Close()
	out := map[string]ChannelStatus{}
	for rows.Next() {
		var s ChannelStatus
		var ageSeconds float64
		var rate *float64
		if err := rows.Scan(&s.Channel, &s.Circuit, &s.LastError, &s.UpdatedAt, &ageSeconds, &rate); err != nil {
			return nil, fmt.Errorf("scanning a channel status: %w", err)
		}
		s.Age = time.Duration(ageSeconds * float64(time.Second))
		if rate != nil {
			s.RatePerSecond = *rate
		}
		out[s.Channel] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the channel statuses: %w", err)
	}
	return out, nil
}

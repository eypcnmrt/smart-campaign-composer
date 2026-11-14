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
	// Age is how long ago the row was written, measured by the database clock.
	Age time.Duration
}

// SetChannelStatus writes the heartbeat of a channel: its circuit state and, when the circuit is not closed, the last
// error. updated_at is set by the database.
func (q *Queue) SetChannelStatus(ctx context.Context, channel, circuit, lastError string) error {
	if _, ok := channelLockKey[channel]; !ok {
		return fmt.Errorf("channel status: unknown channel %q", channel)
	}
	if _, err := q.pool.Exec(ctx, `
		INSERT INTO worker_channel_status (channel, circuit, last_error, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (channel) DO UPDATE
		SET circuit = EXCLUDED.circuit, last_error = EXCLUDED.last_error, updated_at = EXCLUDED.updated_at`,
		channel, circuit, truncate(lastError, maxErrorText)); err != nil {
		return fmt.Errorf("writing the %s channel status: %w", channel, err)
	}
	return nil
}

// ChannelStatuses returns the last report of every channel that has one, by channel. A channel the worker never
// reported is missing from the map.
func (q *Queue) ChannelStatuses(ctx context.Context) (map[string]ChannelStatus, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT channel, circuit, last_error, updated_at, EXTRACT(EPOCH FROM now() - updated_at)::float8
		FROM worker_channel_status`)
	if err != nil {
		return nil, fmt.Errorf("reading the channel statuses: %w", err)
	}
	defer rows.Close()
	out := map[string]ChannelStatus{}
	for rows.Next() {
		var s ChannelStatus
		var ageSeconds float64
		if err := rows.Scan(&s.Channel, &s.Circuit, &s.LastError, &s.UpdatedAt, &ageSeconds); err != nil {
			return nil, fmt.Errorf("scanning a channel status: %w", err)
		}
		s.Age = time.Duration(ageSeconds * float64(time.Second))
		out[s.Channel] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the channel statuses: %w", err)
	}
	return out, nil
}

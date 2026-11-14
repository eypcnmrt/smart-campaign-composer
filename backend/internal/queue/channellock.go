package queue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrChannelLocked means another worker holds the channel's lock.
var ErrChannelLocked = errors.New("channel is served by another worker")

// channelLockClass is the first key of the two-integer advisory lock; the second is the channel. The migration
// runner and the seed use the single-bigint form, which is a different lock space, so they cannot collide.
const channelLockClass int32 = 0x5343 // "SC"

// channelLockKey maps a channel to the second key. Like consentColumn, it is the whitelist of channel names.
var channelLockKey = map[string]int32{"sms": 1, "email": 2}

// ChannelLock is the right to send on one channel. The 100 messages per second cap is enforced inside one worker
// process (docs/PLAN.md section 2, assumption 9), so a second worker would double it. To keep the cap with more than
// one worker, each channel is served by the worker that holds a session-level advisory lock for it; a worker that
// cannot get the lock waits and tries again.
//
// The lock belongs to a database session, so the ChannelLock owns one connection for as long as it lives and the
// claims go through that connection: if the connection dies, the server drops the lock, and the next claim fails, so
// the holder finds out within one claim and stops serving the channel instead of sending on without the lock.
//
// A ChannelLock is for one goroutine at a time (a pgx connection is not safe for concurrent use).
type ChannelLock struct {
	conn    *pgxpool.Conn
	channel string
	once    sync.Once
}

// LockChannel tries to take the channel's lock without waiting. It returns ErrChannelLocked when another worker
// holds it. The caller must Release the lock.
func (q *Queue) LockChannel(ctx context.Context, channel string) (*ChannelLock, error) {
	key, ok := channelLockKey[channel]
	if !ok {
		return nil, fmt.Errorf("locking channel %q: unknown channel", channel)
	}
	conn, err := q.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring a connection for the %s lock: %w", channel, err)
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1, $2)`, channelLockClass, key).Scan(&got); err != nil {
		// The state of the lock is unknown: do not hand the session back to the pool, closing it releases any lock.
		_ = conn.Hijack().Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("taking the %s lock: %w", channel, err)
	}
	if !got {
		conn.Release()
		return nil, fmt.Errorf("%s: %w", channel, ErrChannelLocked)
	}
	return &ChannelLock{conn: conn, channel: channel}, nil
}

// Claim is Queue.Claim over the lock's own connection.
func (l *ChannelLock) Claim(ctx context.Context, lease time.Duration) (Delivery, bool, error) {
	return claim(ctx, l.conn, l.channel, lease)
}

// Release gives the lock up. It is safe to call more than once. If the unlock does not succeed, the session is
// destroyed instead of being returned to the pool, because a session that kept the lock would block the channel for
// every other worker until the pool recycled the connection.
func (l *ChannelLock) Release(ctx context.Context) error {
	var err error
	l.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var unlocked bool
		err = l.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1, $2)`, channelLockClass, channelLockKey[l.channel]).Scan(&unlocked)
		if err == nil && !unlocked {
			// Not held by this session: the connection was replaced under us, which cannot be allowed to go unnoticed.
			err = fmt.Errorf("the %s lock was not held by its session", l.channel)
		}
		if err != nil {
			_ = l.conn.Hijack().Close(ctx)
			err = fmt.Errorf("releasing the %s lock (session closed): %w", l.channel, err)
			return
		}
		l.conn.Release()
	})
	return err
}

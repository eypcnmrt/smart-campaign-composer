// Package queue is the delivery queue on top of the deliveries table: enqueueing an approved
// campaign, claiming work with FOR UPDATE SKIP LOCKED and a lease, recording the outcome, taking
// back expired leases and completing finished campaigns.
//
// Time inside the queue (leases, next_attempt_at) always comes from the database clock, so workers
// on machines with different clocks agree.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/content"
	"smartcampaign/internal/retry"
	"smartcampaign/internal/segment"
)

// Delivery statuses, as stored.
const (
	StatusQueued  = "queued"
	StatusSending = "sending"
	StatusSent    = "sent"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// Campaign statuses the queue moves between.
const (
	CampaignApproved             = "approved"
	CampaignSending              = "sending"
	CampaignCompleted            = "completed"
	CampaignCompletedWithFailure = "completed_with_failures"
)

var (
	// ErrCampaignNotFound means there is no campaign with that id.
	ErrCampaignNotFound = errors.New("campaign not found")
	// ErrNotApproved means the campaign is not in the approved state, so it cannot be enqueued.
	ErrNotApproved = errors.New("campaign is not approved")
)

// consentColumn is the only way a channel name reaches SQL: through this whitelist.
var consentColumn = map[string]string{
	"sms":   "u.sms_consent",
	"email": "u.email_consent",
}

// Queue works on a connection pool.
type Queue struct {
	pool *pgxpool.Pool
}

// New returns a Queue.
func New(pool *pgxpool.Pool) *Queue { return &Queue{pool: pool} }

// EnqueueResult says how many deliveries were created per channel.
type EnqueueResult struct {
	Created map[string]int
	// Completed is true when nobody was eligible, so the campaign was finished on the spot.
	Completed bool
}

// Total is the number of deliveries created over all channels.
func (r EnqueueResult) Total() (n int) {
	for _, c := range r.Created {
		n += c
	}
	return n
}

// Enqueue freezes the audience of an approved campaign: one INSERT ... SELECT per channel over the
// compiled segment predicate and the channel's consent, ON CONFLICT DO NOTHING so a second call adds
// nothing. The campaign moves from approved to sending in the same transaction (or straight to
// completed when nobody is eligible). Event windows count back from approved_at, so the audience
// does not depend on when the worker happens to run.
func (q *Queue) Enqueue(ctx context.Context, campaignID int64) (res EnqueueResult, err error) {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
			}
		}
	}()

	var (
		status     string
		channels   []string
		ruleJSON   []byte
		approvedAt *time.Time
	)
	err = tx.QueryRow(ctx,
		`SELECT status, channels, segment_rule, approved_at FROM campaigns WHERE id = $1 FOR UPDATE`, campaignID,
	).Scan(&status, &channels, &ruleJSON, &approvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnqueueResult{}, ErrCampaignNotFound
	}
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("loading campaign %d: %w", campaignID, err)
	}
	if status != CampaignApproved || approvedAt == nil {
		return EnqueueResult{}, fmt.Errorf("campaign %d is %q: %w", campaignID, status, ErrNotApproved)
	}

	rule, err := segment.Decode(ruleJSON)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("decoding the rule of campaign %d: %w", campaignID, err)
	}

	res.Created = make(map[string]int, len(channels))
	for _, channel := range channels {
		col, ok := consentColumn[channel]
		if !ok {
			return EnqueueResult{}, fmt.Errorf("campaign %d has unknown channel %q", campaignID, channel)
		}
		// $1 is the campaign, $2 the channel; the segment's own parameters start at $3.
		frag, err := segment.Compile(rule, *approvedAt, 3)
		if err != nil {
			return EnqueueResult{}, fmt.Errorf("compiling the rule of campaign %d: %w", campaignID, err)
		}
		query := `INSERT INTO deliveries (campaign_id, user_id, channel)
			SELECT $1::bigint, u.id, $2::text FROM users u
			WHERE ` + col + ` AND ` + frag.SQL + `
			ON CONFLICT (campaign_id, user_id, channel) DO NOTHING`
		tag, err := tx.Exec(ctx, query, append([]any{campaignID, channel}, frag.Args...)...)
		if err != nil {
			return EnqueueResult{}, fmt.Errorf("enqueueing %s deliveries of campaign %d: %w", channel, campaignID, err)
		}
		res.Created[channel] = int(tag.RowsAffected())
	}

	next := CampaignSending
	var total int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM deliveries WHERE campaign_id = $1`, campaignID).Scan(&total); err != nil {
		return EnqueueResult{}, fmt.Errorf("counting deliveries of campaign %d: %w", campaignID, err)
	}
	if total == 0 {
		next = CampaignCompleted
		res.Completed = true
	}
	if _, err = tx.Exec(ctx,
		`UPDATE campaigns SET status = $2::text,
			completed_at = CASE WHEN $2::text = 'completed' THEN now() END, updated_at = now()
		 WHERE id = $1`,
		campaignID, next); err != nil {
		return EnqueueResult{}, fmt.Errorf("updating campaign %d: %w", campaignID, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return EnqueueResult{}, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// Delivery is one claimed unit of work, with everything needed to send it.
type Delivery struct {
	ID         int64
	CampaignID int64
	UserID     int64
	Channel    string
	// Attempt is the number of this attempt (1 for the first); it also fences the result: a
	// result is only written while the row still has this attempt number and is still sending.
	Attempt   int
	Recipient string // phone for sms, e-mail address for email
	Vars      content.Vars
	Content   content.Content // the template frozen at approval, not yet rendered
	// Consent is the user's consent for this channel as read at the moment of the claim.
	Consent bool
}

// Key is the idempotency key sent to the provider: the delivery id, the same on every attempt.
func (d Delivery) Key() string { return fmt.Sprintf("%d", d.ID) }

// Claim takes the next due delivery of the channel: status queued and next_attempt_at reached. It moves
// it to sending, counts the attempt and sets a lease of the given length. ok is false when nothing is
// due. FOR UPDATE SKIP LOCKED lets any number of workers claim at once without waiting for or
// duplicating each other.
func (q *Queue) Claim(ctx context.Context, channel string, lease time.Duration) (d Delivery, ok bool, err error) {
	const query = `
WITH due AS (
	SELECT id FROM deliveries
	WHERE channel = $1 AND status = 'queued' AND next_attempt_at <= now()
	ORDER BY next_attempt_at, id
	LIMIT 1
	FOR UPDATE SKIP LOCKED
), claimed AS (
	UPDATE deliveries d
	SET status = 'sending', attempts = d.attempts + 1,
	    lease_until = now() + make_interval(secs => $2), updated_at = now()
	FROM due
	WHERE d.id = due.id
	RETURNING d.id, d.campaign_id, d.user_id, d.channel, d.attempts
)
SELECT c.id, c.campaign_id, c.user_id, c.channel, c.attempts,
       CASE c.channel WHEN 'sms' THEN u.phone ELSE u.email END,
       u.first_name, u.city,
       CASE c.channel WHEN 'sms' THEN u.sms_consent ELSE u.email_consent END,
       camp.content
FROM claimed c
JOIN users u ON u.id = c.user_id
JOIN campaigns camp ON camp.id = c.campaign_id`

	var raw []byte
	err = q.pool.QueryRow(ctx, query, channel, lease.Seconds()).Scan(
		&d.ID, &d.CampaignID, &d.UserID, &d.Channel, &d.Attempt,
		&d.Recipient, &d.Vars.FirstName, &d.Vars.City, &d.Consent, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, false, nil
	}
	if err != nil {
		return Delivery{}, false, fmt.Errorf("claiming a %s delivery: %w", channel, err)
	}
	if err := json.Unmarshal(raw, &d.Content); err != nil {
		// The content was validated at approval; this means the row is corrupt. The delivery stays
		// claimed and its lease will expire, so the caller must be told.
		return d, true, fmt.Errorf("delivery %d: decoding campaign content: %w", d.ID, err)
	}
	return d, true, nil
}

// Outcome is how an attempt ended.
type Outcome struct {
	Status string        // StatusSent, StatusQueued (retry), StatusFailed or StatusSkipped
	Delay  time.Duration // for StatusQueued: wait before the next attempt
	Reason string        // stored in last_error (empty for sent)
}

// ErrLeaseLost means the delivery is no longer the caller's: its lease expired and it was taken back
// (and possibly claimed again). The result is dropped, which is safe because the provider
// de-duplicates by key.
var ErrLeaseLost = errors.New("lease lost")

const maxErrorText = 500

// Finish records the outcome of the attempt d was claimed for.
func (q *Queue) Finish(ctx context.Context, d Delivery, o Outcome) error {
	var query string
	reason := truncate(o.Reason, maxErrorText)
	args := []any{d.ID, d.Attempt}
	switch o.Status {
	case StatusSent:
		query = `UPDATE deliveries SET status = 'sent', sent_at = now(), lease_until = NULL, last_error = '', updated_at = now()
			WHERE id = $1 AND status = 'sending' AND attempts = $2`
	case StatusQueued:
		query = `UPDATE deliveries SET status = 'queued', lease_until = NULL, last_error = $3,
				next_attempt_at = now() + make_interval(secs => $4), updated_at = now()
			WHERE id = $1 AND status = 'sending' AND attempts = $2`
		args = append(args, reason, o.Delay.Seconds())
	case StatusFailed, StatusSkipped:
		query = `UPDATE deliveries SET status = $4, lease_until = NULL, last_error = $3, updated_at = now()
			WHERE id = $1 AND status = 'sending' AND attempts = $2`
		args = append(args, reason, o.Status)
	default:
		return fmt.Errorf("unknown outcome status %q", o.Status)
	}
	tag, err := q.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("recording %s for delivery %d: %w", o.Status, d.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delivery %d attempt %d: %w", d.ID, d.Attempt, ErrLeaseLost)
	}
	return nil
}

// RecoverResult counts what RecoverLeases did.
type RecoverResult struct {
	Requeued int
	Failed   int
}

// RecoverLeases takes back deliveries stuck in sending past their lease (the worker died or hung).
// They go back to queued and are due at once; one whose attempts are already used up fails instead.
// Re-sending is safe because the provider de-duplicates by idempotency key.
//
// maxAttempts is the most attempts a delivery can use (retry.Policy.MaxTotalAttempts: the regular attempts plus the
// confirmation request). A lease that expires on that last attempt leaves nothing to confirm with, so the delivery
// fails and its last_error says the outcome is unknown.
func (q *Queue) RecoverLeases(ctx context.Context, maxAttempts int) (RecoverResult, error) {
	unknown := retry.UnknownOutcomeReason(maxAttempts-1, maxAttempts, "the lease expired")
	rows, err := q.pool.Query(ctx, `
		UPDATE deliveries
		SET status = CASE WHEN attempts >= $1 THEN 'failed' ELSE 'queued' END,
		    next_attempt_at = now(), lease_until = NULL,
		    last_error = CASE WHEN attempts >= $1 THEN $2::text ELSE 'lease expired' END
		                 || CASE WHEN last_error <> '' THEN ' (previous error: ' || last_error || ')' ELSE '' END,
		    updated_at = now()
		WHERE status = 'sending' AND lease_until < now()
		RETURNING status`, maxAttempts, unknown)
	if err != nil {
		return RecoverResult{}, fmt.Errorf("recovering leases: %w", err)
	}
	defer rows.Close()
	var res RecoverResult
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return RecoverResult{}, fmt.Errorf("scanning recovered delivery: %w", err)
		}
		if status == StatusFailed {
			res.Failed++
		} else {
			res.Requeued++
		}
	}
	if err := rows.Err(); err != nil {
		return RecoverResult{}, fmt.Errorf("recovering leases: %w", err)
	}
	return res, nil
}

// CompleteFinished finishes every sending campaign that has no queued or sending delivery left:
// completed_with_failures if any delivery failed, completed otherwise. It returns how many it finished.
func (q *Queue) CompleteFinished(ctx context.Context) (int, error) {
	tag, err := q.pool.Exec(ctx, `
		UPDATE campaigns c
		SET status = CASE WHEN EXISTS (SELECT 1 FROM deliveries d WHERE d.campaign_id = c.id AND d.status = 'failed')
		                  THEN 'completed_with_failures' ELSE 'completed' END,
		    completed_at = now(), updated_at = now()
		WHERE c.status = 'sending'
		  AND NOT EXISTS (SELECT 1 FROM deliveries d WHERE d.campaign_id = c.id AND d.status IN ('queued', 'sending'))`)
	if err != nil {
		return 0, fmt.Errorf("completing campaigns: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

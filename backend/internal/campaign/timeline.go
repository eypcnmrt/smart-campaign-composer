package campaign

import (
	"context"
	"fmt"
	"time"
)

// What a delivery that is finished (or nearly) looks like in numbers: the messages the provider accepted, second by second,
// and a summary. Both come from the time each delivery was recorded as sent (deliveries.sent_at) and from the attempt
// counters; nothing is stored for them, so they can be asked for again after the send, and are the same every time.

// MaxTimelineBuckets bounds the size of a timeline: a campaign that lasts longer than this many seconds is counted in
// wider buckets (see bucketWidth) instead of getting a longer answer.
const MaxTimelineBuckets = 600

// bucketWidths are the widths, in seconds, a timeline may have; the narrowest one that fits MaxTimelineBuckets is used, so the
// bars of a long send do not change width every time the send gets a second longer.
var bucketWidths = []int{1, 2, 5, 10, 30, 60, 120, 300, 600, 1800, 3600, 21600, 86400}

// bucketWidth returns the narrowest bucket width, in seconds, that covers span with at most MaxTimelineBuckets buckets.
func bucketWidth(span time.Duration) int {
	for _, w := range bucketWidths {
		if span < time.Duration(w*MaxTimelineBuckets)*time.Second {
			return w
		}
	}
	return bucketWidths[len(bucketWidths)-1]
}

// Timeline is how many messages of a channel the provider accepted in each bucket of BucketSeconds seconds, starting at Start
// (the second the campaign was approved). It ends with the bucket of the last message, or of now while the send goes on.
type Timeline struct {
	Start         time.Time `json:"start"`
	BucketSeconds int       `json:"bucket_seconds"`
	Accepted      []int     `json:"accepted"`
	// AveragePerSecond is the messages of this channel divided by the time from its first to its last message, at least one
	// second; zero before anything was sent. It is per channel on purpose: each channel has its own cap, and a figure of both
	// channels together looks like a cap that was broken when it was not.
	AveragePerSecond float64 `json:"average_per_second"`
}

// SendSummary describes the send as a whole. It holds no rate: the rates belong to a channel (see Timeline), the caps are per channel.
type SendSummary struct {
	// DurationSeconds is the time from the approval to the completion; null until the campaign is completed.
	DurationSeconds *float64 `json:"duration_seconds"`
	// RetriedMessages is the number of messages that needed more than one attempt, TotalAttempts all attempts made.
	RetriedMessages int `json:"retried_messages"`
	TotalAttempts   int `json:"total_attempts"`
}

// TimelineBoundsSQL reads the first and the last time a message of the campaign was sent: the two ends of
// deliveries_sent_at_idx, and the database clock, so the end of a running send does not depend on the API's clock.
const TimelineBoundsSQL = `SELECT now(),
	(SELECT sent_at FROM deliveries WHERE campaign_id = $1 AND status = 'sent' ORDER BY sent_at ASC LIMIT 1),
	(SELECT sent_at FROM deliveries WHERE campaign_id = $1 AND status = 'sent' ORDER BY sent_at DESC LIMIT 1)`

// TimelineSQL counts the sent messages of a campaign per channel and bucket: $1 the campaign, $2 the start of the first bucket,
// $3 the bucket width in seconds. An index-only scan of deliveries_sent_at_idx (a test checks the plan).
const TimelineSQL = `SELECT channel, GREATEST(floor(extract(epoch FROM sent_at - $2::timestamptz) / $3), 0)::int AS bucket, count(*), min(sent_at), max(sent_at)
	FROM deliveries WHERE campaign_id = $1 AND status = 'sent' GROUP BY channel, bucket`

// timelines reads the per-channel timelines of an approved campaign. approvedAt is the start; completedAt is nil while the send
// goes on. It returns the timelines by channel.
func (s *Store) timelines(ctx context.Context, db reader, id int64, approvedAt time.Time, completedAt *time.Time, channels []string) (map[string]*Timeline, error) {
	var now time.Time
	var first, last *time.Time // only the last is needed here (the end of the timeline); the per-channel ends come with the counts
	if err := db.QueryRow(ctx, TimelineBoundsSQL, id).Scan(&now, &first, &last); err != nil {
		return nil, fmt.Errorf("reading the send times of campaign %d: %w", id, err)
	}
	start := approvedAt.Truncate(time.Second)
	end := now
	if completedAt != nil {
		end = *completedAt
	}
	if last != nil && last.After(end) {
		end = *last // a message recorded after the completion time was read: the timeline must still hold it
	}
	if end.Before(start) {
		end = start
	}
	width := bucketWidth(end.Sub(start))
	n := int(end.Sub(start)/(time.Duration(width)*time.Second)) + 1

	out := make(map[string]*Timeline, len(channels))
	for _, ch := range channels {
		out[ch] = &Timeline{Start: start, BucketSeconds: width, Accepted: make([]int, n)}
	}
	rows, err := db.Query(ctx, TimelineSQL, id, start, width)
	if err != nil {
		return nil, fmt.Errorf("counting the messages of campaign %d per second: %w", id, err)
	}
	defer rows.Close()
	type span struct {
		count       int
		first, last time.Time
	}
	spans := map[string]*span{}
	for rows.Next() {
		var channel string
		var bucket, count int
		var firstSent, lastSent time.Time
		if err := rows.Scan(&channel, &bucket, &count, &firstSent, &lastSent); err != nil {
			return nil, fmt.Errorf("counting the messages of campaign %d per second: %w", id, err)
		}
		tl := out[channel]
		if tl == nil || bucket >= n { // a channel the campaign does not have, or a message from after the end that was read
			continue
		}
		tl.Accepted[bucket] += count
		sp := spans[channel]
		if sp == nil {
			sp = &span{first: firstSent, last: lastSent}
			spans[channel] = sp
		}
		sp.count += count
		if firstSent.Before(sp.first) {
			sp.first = firstSent
		}
		if lastSent.After(sp.last) {
			sp.last = lastSent
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("counting the messages of campaign %d per second: %w", id, err)
	}

	for ch, sp := range spans {
		out[ch].AveragePerSecond = float64(sp.count) / max(1, sp.last.Sub(sp.first).Seconds())
	}
	return out, nil
}

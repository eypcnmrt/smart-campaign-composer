-- The rate timeline of a campaign (accepted messages per second, from the moment each delivery was recorded as sent) and the
-- first and last time a message was sent. Partial on the sent rows, ordered by time inside a campaign: the bounds are the two
-- ends of the index, and the count per second is answered from the index alone (campaign, time and channel are all in it).
-- Only sent rows are indexed, so it stays small next to the queue's own indexes and costs the worker one more index entry
-- per delivery, written in the statement that already sets sent_at.
CREATE INDEX deliveries_sent_at_idx ON deliveries (campaign_id, sent_at, channel) WHERE status = 'sent';

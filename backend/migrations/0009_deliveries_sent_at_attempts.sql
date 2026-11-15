-- The rate timeline now also separates the messages the provider accepted on the first request from those that needed a retry
-- (a retry waits out a backoff, and counting it made a channel's average look far below its cap). That needs `attempts` in the
-- partial index of migration 0008, as a payload column, so the count per second is still answered from the index alone.
DROP INDEX deliveries_sent_at_idx;
CREATE INDEX deliveries_sent_at_idx ON deliveries (campaign_id, sent_at, channel) INCLUDE (attempts) WHERE status = 'sent';

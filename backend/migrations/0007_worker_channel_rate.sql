-- The rate cap the worker that serves a channel is configured with (messages per second), reported with the heartbeat so the
-- dashboard can draw the real limit instead of a number written in the page. NULL: the worker did not say (a row written
-- before this migration, or by older code); the screen then draws no limit line.
ALTER TABLE worker_channel_status
    ADD COLUMN rate_per_second DOUBLE PRECISION CHECK (rate_per_second IS NULL OR rate_per_second > 0);

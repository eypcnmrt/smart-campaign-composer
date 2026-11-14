-- What the worker knows about each channel: the state of its circuit breaker and a heartbeat. The worker that serves a
-- channel (the holder of its advisory lock) rewrites the row every couple of seconds and whenever the circuit changes,
-- so the API and the dashboard can tell "the provider is unreachable, waiting" (the circuit is open and the row is
-- fresh) from "the worker is not running" (the row is old or missing) without asking the worker anything.
--
-- updated_at always comes from the database clock (now()), and the age is computed by the database too, so the clocks of
-- the worker and the API machines do not have to agree.
CREATE TABLE worker_channel_status (
    channel    TEXT        PRIMARY KEY CHECK (channel IN ('sms', 'email')),
    circuit    TEXT        NOT NULL CHECK (circuit IN ('closed', 'open', 'half_open')),
    last_error TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

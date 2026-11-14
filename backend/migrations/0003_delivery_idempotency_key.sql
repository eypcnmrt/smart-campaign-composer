-- The idempotency key sent to the provider is its own value, generated when the delivery is enqueued, and no
-- longer the delivery id. If the database is reset (ids start from 1 again) while a long-lived provider keeps its
-- memory, a repeated id would look like a duplicate and a new message would be treated as already sent; a random
-- UUID cannot repeat. The key never changes for a delivery: the same value is sent on every attempt, on a retry
-- after a timeout and after a lease recovery.
--
-- gen_random_uuid() is volatile, so existing rows each get their own value when the column is added.
ALTER TABLE deliveries ADD COLUMN idempotency_key UUID NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE deliveries ADD CONSTRAINT deliveries_idempotency_key_unique UNIQUE (idempotency_key);

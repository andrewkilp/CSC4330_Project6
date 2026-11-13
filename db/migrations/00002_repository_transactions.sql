-- +goose Up
-- Row locking on calls serializes this counter with entity writes and events.
ALTER TABLE calls ADD COLUMN last_event_seq bigint NOT NULL DEFAULT 0
    CHECK (last_event_seq >= 0);
UPDATE calls c SET last_event_seq = COALESCE(
    (SELECT max(e.seq) FROM call_events e WHERE e.call_id = c.id), 0);

-- Replays return the original action result, rather than the call's later state.
ALTER TABLE call_idempotency_keys ADD COLUMN result_json jsonb NOT NULL DEFAULT '{}'
    CHECK (jsonb_typeof(result_json) = 'object');

-- +goose Down
ALTER TABLE call_idempotency_keys DROP COLUMN result_json;
ALTER TABLE calls DROP COLUMN last_event_seq;

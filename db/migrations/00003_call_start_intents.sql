-- +goose Up
-- One durable external start intent per call, independent of client retries/keys.
-- Intent is committed BEFORE contacting a provider. It is never removed/rearmed.
CREATE TABLE call_starts (
    call_id uuid PRIMARY KEY REFERENCES calls(id) ON DELETE CASCADE,
    attempt_id uuid NOT NULL UNIQUE,
    start_key uuid NOT NULL,
    mode text NOT NULL CHECK (mode IN ('mock','real')),
    destination text NOT NULL,
    account_sid text NOT NULL,
    callback_token_hash bytea NOT NULL CHECK (octet_length(callback_token_hash)=32),
    provider_sid text UNIQUE,
    state text NOT NULL CHECK (state IN ('reserved','confirmed','ambiguous')),
    result_json jsonb CHECK (jsonb_typeof(result_json)='object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((state='confirmed') = (provider_sid IS NOT NULL AND result_json IS NOT NULL))
);

-- +goose Down
DROP TABLE call_starts;

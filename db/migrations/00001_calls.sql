-- +goose Up
CREATE TABLE session_owners (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE calls (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES session_owners (id),
    mode text NOT NULL CHECK (mode IN ('mock', 'real')),
    destination text NOT NULL CHECK (destination ~ '^[+][1-9][0-9]{1,14}$'),
    business_name text NOT NULL CHECK (char_length(business_name) BETWEEN 1 AND 120),
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'dialing', 'connected', 'paused', 'ended', 'failed', 'cancelled')),
    approval_mode text NOT NULL DEFAULT 'always' CHECK (approval_mode IN ('always', 'uncertain')),
    consent_acknowledged boolean NOT NULL,
    context_revision bigint NOT NULL DEFAULT 0 CHECK (context_revision >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, id)
);

CREATE INDEX calls_owner_history_idx ON calls (owner_id, created_at DESC, id DESC);

CREATE TABLE call_goals (
    call_id uuid NOT NULL REFERENCES calls (id) ON DELETE CASCADE,
    context_revision bigint NOT NULL CHECK (context_revision >= 0),
    kind text NOT NULL CHECK (kind IN ('restaurant', 'appointment')),
    preferred_at timestamptz NOT NULL,
    timezone text NOT NULL,
    party_size integer,
    service text,
    constraints text[] NOT NULL DEFAULT '{}'
        CHECK (cardinality(constraints) <= 12 AND array_position(constraints, NULL) IS NULL),
    contact_name text NOT NULL,
    contact_phone text,
    PRIMARY KEY (call_id, context_revision),
    CHECK (
        (kind = 'restaurant' AND party_size IS NOT NULL AND party_size BETWEEN 1 AND 20)
        OR (kind = 'appointment' AND service IS NOT NULL AND char_length(service) BETWEEN 1 AND 160)
    )
);

CREATE TABLE call_instructions (
    id uuid PRIMARY KEY,
    call_id uuid NOT NULL REFERENCES calls (id) ON DELETE CASCADE,
    text text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 1000),
    context_revision bigint NOT NULL CHECK (context_revision >= 0),
    status text NOT NULL CHECK (status IN ('pending', 'applied', 'failed')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX call_instructions_revision_idx ON call_instructions (call_id, context_revision);
CREATE UNIQUE INDEX call_instructions_applied_revision_idx
    ON call_instructions (call_id, context_revision) WHERE status = 'applied';

CREATE TABLE call_responses (
    id uuid PRIMARY KEY,
    call_id uuid NOT NULL REFERENCES calls (id) ON DELETE CASCADE,
    text text NOT NULL CHECK (char_length(text) <= 1500),
    context_revision bigint NOT NULL CHECK (context_revision >= 0),
    status text NOT NULL
        CHECK (status IN ('generating', 'suggested', 'approved', 'speaking', 'spoken', 'cancelled', 'failed')),
    needs_approval boolean NOT NULL,
    reason text,
    UNIQUE (call_id, id)
);

CREATE TABLE call_turns (
    id uuid PRIMARY KEY,
    call_id uuid NOT NULL REFERENCES calls (id) ON DELETE CASCADE,
    speaker text NOT NULL CHECK (speaker IN ('assistant', 'business')),
    text text NOT NULL,
    is_final boolean NOT NULL,
    started_at_ms bigint NOT NULL CHECK (started_at_ms >= 0),
    ended_at_ms bigint CHECK (ended_at_ms >= started_at_ms),
    response_id uuid,
    FOREIGN KEY (call_id, response_id) REFERENCES call_responses (call_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX call_turns_timeline_idx ON call_turns (call_id, started_at_ms, id);

CREATE TABLE call_events (
    id uuid PRIMARY KEY,
    call_id uuid NOT NULL REFERENCES calls (id) ON DELETE CASCADE,
    seq bigint NOT NULL CHECK (seq > 0),
    type text NOT NULL CHECK (type IN (
        'call.updated', 'turn.updated', 'instruction.updated', 'response.updated',
        'summary.ready', 'provider.error', 'usage.updated'
    )),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    context_revision bigint NOT NULL CHECK (context_revision >= 0),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    UNIQUE (call_id, seq)
);

-- +goose StatementBegin
CREATE FUNCTION reject_call_event_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'call events are append-only' USING ERRCODE = 'check_violation';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER call_events_no_update
    BEFORE UPDATE ON call_events
    FOR EACH ROW EXECUTE FUNCTION reject_call_event_update();

CREATE TABLE call_idempotency_keys (
    owner_id uuid NOT NULL REFERENCES session_owners (id),
    action text NOT NULL,
    -- The request scope is NULL when creating a call and its ID is still unknown.
    scope_call_id uuid,
    key uuid NOT NULL,
    request_hash bytea NOT NULL,
    result_call_id uuid NOT NULL,
    FOREIGN KEY (owner_id, scope_call_id) REFERENCES calls (owner_id, id) ON DELETE CASCADE,
    FOREIGN KEY (owner_id, result_call_id) REFERENCES calls (owner_id, id) ON DELETE CASCADE,
    CHECK (scope_call_id IS NULL OR scope_call_id = result_call_id),
    UNIQUE NULLS NOT DISTINCT (owner_id, action, scope_call_id, key)
);

CREATE INDEX call_idempotency_result_idx ON call_idempotency_keys (owner_id, result_call_id);

-- +goose Down
DROP TABLE call_idempotency_keys;
DROP TABLE call_events;
DROP FUNCTION reject_call_event_update();
DROP TABLE call_turns;
DROP TABLE call_responses;
DROP TABLE call_instructions;
DROP TABLE call_goals;
DROP TABLE calls;
DROP TABLE session_owners;

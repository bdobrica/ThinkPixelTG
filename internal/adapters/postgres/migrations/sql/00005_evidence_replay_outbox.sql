-- +goose Up

CREATE TABLE trusted_usage_events (
    tenant_id uuid NOT NULL,
    event_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    accounting_key text NOT NULL CHECK (accounting_key <> ''),
    dimension text NOT NULL CHECK (dimension <> ''),
    quantity numeric NOT NULL CHECK (quantity >= 0),
    unit text NOT NULL CHECK (unit <> ''),
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(attributes) = 'object'),
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, event_id),
    UNIQUE (tenant_id, accounting_key, dimension),
    FOREIGN KEY (tenant_id, invocation_id)
        REFERENCES invocations (tenant_id, invocation_id) ON DELETE RESTRICT,
    CHECK (recorded_at >= occurred_at)
);

CREATE INDEX trusted_usage_events_invocation_idx
    ON trusted_usage_events (tenant_id, invocation_id, occurred_at);

CREATE TABLE audit_events (
    tenant_id uuid NOT NULL REFERENCES tenants (tenant_id) ON DELETE RESTRICT,
    event_id uuid NOT NULL,
    invocation_id uuid,
    event_type text NOT NULL CHECK (event_type <> ''),
    evidence_profile text NOT NULL CHECK (evidence_profile <> ''),
    actor_class text NOT NULL CHECK (actor_class <> ''),
    actor_ref text,
    outcome text NOT NULL CHECK (outcome <> ''),
    reason_code text,
    correlation jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(correlation) = 'object'),
    safe_payload jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(safe_payload) = 'object'),
    payload_digest bytea NOT NULL CHECK (octet_length(payload_digest) = 32),
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, event_id),
    FOREIGN KEY (tenant_id, invocation_id)
        REFERENCES invocations (tenant_id, invocation_id) ON DELETE RESTRICT,
    CHECK (actor_ref IS NULL OR actor_ref <> ''),
    CHECK (reason_code IS NULL OR reason_code <> ''),
    CHECK (recorded_at >= occurred_at)
);

CREATE INDEX audit_events_invocation_idx
    ON audit_events (tenant_id, invocation_id, occurred_at, event_id);

-- +goose StatementBegin
CREATE FUNCTION protect_append_only_event() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION '% records are append-only', TG_TABLE_NAME
        USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trusted_usage_events_append_only
BEFORE UPDATE OR DELETE ON trusted_usage_events
FOR EACH ROW EXECUTE FUNCTION protect_append_only_event();

CREATE TRIGGER audit_events_append_only
BEFORE UPDATE OR DELETE ON audit_events
FOR EACH ROW EXECUTE FUNCTION protect_append_only_event();

CREATE TABLE idempotency_records (
    tenant_id uuid NOT NULL REFERENCES tenants (tenant_id) ON DELETE RESTRICT,
    idempotency_scope text NOT NULL CHECK (idempotency_scope <> ''),
    idempotency_key text NOT NULL CHECK (idempotency_key <> ''),
    invocation_id uuid,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    state text NOT NULL CHECK (state IN ('claimed', 'completed', 'conflict', 'expired')),
    claim_owner text,
    claim_expires_at timestamptz,
    replay_status_code integer CHECK (replay_status_code BETWEEN 100 AND 599),
    replay_result_digest bytea
        CHECK (replay_result_digest IS NULL OR octet_length(replay_result_digest) = 32),
    safe_replay_payload jsonb
        CHECK (safe_replay_payload IS NULL OR jsonb_typeof(safe_replay_payload) = 'object'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, idempotency_scope, idempotency_key),
    FOREIGN KEY (tenant_id, invocation_id)
        REFERENCES invocations (tenant_id, invocation_id) ON DELETE RESTRICT,
    CHECK (
        (claim_owner IS NULL AND claim_expires_at IS NULL)
        OR (claim_owner IS NOT NULL AND claim_owner <> '' AND claim_expires_at IS NOT NULL)
    ),
    CHECK (
        state <> 'completed'
        OR (invocation_id IS NOT NULL AND replay_status_code IS NOT NULL
            AND replay_result_digest IS NOT NULL)
    ),
    CHECK (updated_at >= created_at),
    CHECK (expires_at > created_at)
);

CREATE INDEX idempotency_records_claim_idx
    ON idempotency_records (claim_expires_at, tenant_id, idempotency_scope, idempotency_key)
    WHERE state = 'claimed';

CREATE TABLE outbox_messages (
    tenant_id uuid NOT NULL REFERENCES tenants (tenant_id) ON DELETE RESTRICT,
    outbox_id uuid NOT NULL,
    event_id uuid NOT NULL,
    topic text NOT NULL CHECK (topic <> ''),
    event_type text NOT NULL CHECK (event_type <> ''),
    safe_payload jsonb NOT NULL CHECK (jsonb_typeof(safe_payload) = 'object'),
    payload_digest bytea NOT NULL CHECK (octet_length(payload_digest) = 32),
    created_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    claim_owner text,
    claim_until timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error_class text CHECK (last_error_class IN (
        'transient', 'permanent', 'poison', 'unknown'
    )),
    last_error_at timestamptz,
    published_at timestamptz,
    publication_ref text,
    dead_lettered_at timestamptz,
    dead_letter_reason text,
    PRIMARY KEY (tenant_id, outbox_id),
    UNIQUE (tenant_id, topic, event_id),
    CHECK (available_at >= created_at),
    CHECK (
        (claim_owner IS NULL AND claim_until IS NULL)
        OR (claim_owner IS NOT NULL AND claim_owner <> '' AND claim_until IS NOT NULL)
    ),
    CHECK ((last_error_class IS NULL) = (last_error_at IS NULL)),
    CHECK ((published_at IS NULL) OR (dead_lettered_at IS NULL)),
    CHECK (publication_ref IS NULL OR published_at IS NOT NULL),
    CHECK (dead_letter_reason IS NULL OR dead_lettered_at IS NOT NULL),
    CHECK (
        (published_at IS NULL AND dead_lettered_at IS NULL)
        OR (claim_owner IS NULL AND claim_until IS NULL)
    )
);

CREATE INDEX outbox_messages_claim_idx
    ON outbox_messages (available_at, tenant_id, outbox_id)
    WHERE published_at IS NULL AND dead_lettered_at IS NULL;

CREATE INDEX outbox_messages_dead_letter_idx
    ON outbox_messages (tenant_id, dead_lettered_at, outbox_id)
    WHERE dead_lettered_at IS NOT NULL;

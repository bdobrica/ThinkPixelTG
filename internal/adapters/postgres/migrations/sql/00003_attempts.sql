-- +goose Up

CREATE TABLE invocation_attempts (
    tenant_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    attempt_no integer NOT NULL CHECK (attempt_no > 0),
    fence bigint NOT NULL CHECK (fence > 0),
    owner_id text,
    claimed_at timestamptz,
    lease_expires_at timestamptz,
    downstream_request_ref text,
    downstream_result_ref text,
    outcome_classification text CHECK (outcome_classification IN (
        'not_sent',
        'definitely_rejected',
        'confirmed_success',
        'transient_safe',
        'unknown'
    )),
    retry_classification text CHECK (retry_classification IN (
        'not_retryable',
        'retry_safe',
        'reconcile_required',
        'manual_review_required'
    )),
    ambiguity_classification text CHECK (ambiguity_classification IN (
        'none',
        'possibly_applied',
        'provider_state_unknown',
        'evidence_conflict'
    )),
    evidence jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(evidence) = 'object'),
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    PRIMARY KEY (tenant_id, invocation_id, attempt_no),
    UNIQUE (tenant_id, invocation_id, fence),
    UNIQUE (tenant_id, invocation_id, attempt_no, fence),
    FOREIGN KEY (tenant_id, invocation_id)
        REFERENCES invocations (tenant_id, invocation_id) ON DELETE RESTRICT,
    CHECK (
        (owner_id IS NULL AND claimed_at IS NULL AND lease_expires_at IS NULL)
        OR
        (owner_id IS NOT NULL AND owner_id <> '' AND claimed_at IS NOT NULL
            AND lease_expires_at IS NOT NULL AND lease_expires_at > claimed_at)
    ),
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (
        (finished_at IS NULL AND outcome_classification IS NULL)
        OR (finished_at IS NOT NULL AND outcome_classification IS NOT NULL)
    ),
    CHECK (
        outcome_classification IS DISTINCT FROM 'unknown'
        OR ambiguity_classification IS NOT NULL
    )
);

-- +goose StatementBegin
CREATE FUNCTION require_next_invocation_attempt() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    next_attempt integer;
    greatest_fence bigint;
BEGIN
    PERFORM 1 FROM invocations
    WHERE tenant_id = NEW.tenant_id AND invocation_id = NEW.invocation_id
    FOR UPDATE;

    SELECT COALESCE(MAX(attempt_no), 0) + 1, COALESCE(MAX(fence), 0)
    INTO next_attempt, greatest_fence
    FROM invocation_attempts
    WHERE tenant_id = NEW.tenant_id AND invocation_id = NEW.invocation_id;

    IF NEW.attempt_no <> next_attempt THEN
        RAISE EXCEPTION 'attempt number must be the next monotonic sequence value'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.fence <= greatest_fence THEN
        RAISE EXCEPTION 'attempt fence must increase monotonically'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER invocation_attempts_require_next_sequence
BEFORE INSERT ON invocation_attempts
FOR EACH ROW EXECUTE FUNCTION require_next_invocation_attempt();

CREATE INDEX invocation_attempts_claim_idx
    ON invocation_attempts (lease_expires_at, tenant_id, invocation_id, attempt_no)
    WHERE finished_at IS NULL;

CREATE TABLE invocation_reconciliations (
    tenant_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    sequence integer NOT NULL CHECK (sequence > 0),
    attempt_no integer NOT NULL,
    fence bigint NOT NULL,
    owner_id text NOT NULL CHECK (owner_id <> ''),
    outcome text NOT NULL CHECK (outcome IN (
        'confirmed_success',
        'confirmed_not_applied',
        'still_unknown',
        'unsafe_to_retry'
    )),
    evidence_ref text,
    evidence_digest bytea NOT NULL CHECK (octet_length(evidence_digest) = 32),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, invocation_id, sequence),
    FOREIGN KEY (tenant_id, invocation_id, attempt_no, fence)
        REFERENCES invocation_attempts (tenant_id, invocation_id, attempt_no, fence)
        ON DELETE RESTRICT
);

-- +goose StatementBegin
CREATE FUNCTION require_next_reconciliation_sequence() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    next_sequence integer;
BEGIN
    PERFORM 1 FROM invocations
    WHERE tenant_id = NEW.tenant_id AND invocation_id = NEW.invocation_id
    FOR UPDATE;

    SELECT COALESCE(MAX(sequence), 0) + 1 INTO next_sequence
    FROM invocation_reconciliations
    WHERE tenant_id = NEW.tenant_id AND invocation_id = NEW.invocation_id;

    IF NEW.sequence <> next_sequence THEN
        RAISE EXCEPTION 'reconciliation number must be the next monotonic sequence value'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER invocation_reconciliations_require_next_sequence
BEFORE INSERT ON invocation_reconciliations
FOR EACH ROW EXECUTE FUNCTION require_next_reconciliation_sequence();

CREATE INDEX invocation_reconciliations_outcome_idx
    ON invocation_reconciliations (tenant_id, outcome, created_at);

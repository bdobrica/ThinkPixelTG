-- +goose Up

CREATE TABLE invocations (
    tenant_id uuid NOT NULL REFERENCES tenants (tenant_id) ON DELETE RESTRICT,
    invocation_id uuid NOT NULL,
    run_id text NOT NULL CHECK (run_id <> ''),
    tool_call_id text NOT NULL CHECK (tool_call_id <> ''),
    tool_id text NOT NULL,
    tool_version text NOT NULL,
    argument_profile text NOT NULL CHECK (argument_profile <> ''),
    argument_digest bytea NOT NULL CHECK (octet_length(argument_digest) = 32),
    resource_projection jsonb NOT NULL
        CHECK (jsonb_typeof(resource_projection) = 'object'),
    resource_digest bytea NOT NULL CHECK (octet_length(resource_digest) = 32),
    retry_class text NOT NULL CHECK (retry_class IN (
        'safe',
        'downstream_idempotency',
        'gateway_deduplicated',
        'reconcile_before_retry',
        'at_least_once_accepted',
        'non_retryable'
    )),
    state text NOT NULL CHECK (state IN (
        'received',
        'validated',
        'authorized',
        'pre_tool_passed',
        'waiting_for_approval',
        'ready',
        'executing',
        'retry_wait',
        'reconciling',
        'ambiguous',
        'manual_review',
        'post_tool',
        'succeeded',
        'failed',
        'denied',
        'blocked',
        'cancelled'
    )),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    terminal_code text,
    terminal_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, invocation_id),
    UNIQUE (tenant_id, run_id, tool_call_id),
    FOREIGN KEY (tool_id, tool_version)
        REFERENCES tool_versions (tool_id, version) ON DELETE RESTRICT,
    CHECK (updated_at >= created_at),
    CHECK (
        (state IN ('succeeded', 'failed', 'denied', 'blocked', 'cancelled')
            AND terminal_code IS NOT NULL AND terminal_code <> ''
            AND terminal_at IS NOT NULL AND terminal_at >= created_at)
        OR
        (state NOT IN ('succeeded', 'failed', 'denied', 'blocked', 'cancelled')
            AND terminal_code IS NULL AND terminal_at IS NULL)
    )
);

CREATE INDEX invocations_claim_idx
    ON invocations (state, updated_at, tenant_id, invocation_id)
    WHERE state IN ('ready', 'retry_wait', 'reconciling');

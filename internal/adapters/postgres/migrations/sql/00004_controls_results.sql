-- +goose Up

ALTER TABLE credential_bindings
    ADD CONSTRAINT credential_bindings_connector_identity_key
    UNIQUE (tenant_id, credential_binding_id, connector_instance_id);

CREATE TABLE authorization_decisions (
    tenant_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    decision_id text NOT NULL CHECK (decision_id <> ''),
    context_digest bytea NOT NULL CHECK (octet_length(context_digest) = 32),
    argument_digest bytea NOT NULL CHECK (octet_length(argument_digest) = 32),
    resource_digest bytea NOT NULL CHECK (octet_length(resource_digest) = 32),
    outcome text NOT NULL CHECK (outcome IN ('allow', 'deny')),
    policy_ref text NOT NULL CHECK (policy_ref <> ''),
    constraints jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(constraints) = 'object'),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL,
    revocation_checkpoint text,
    PRIMARY KEY (tenant_id, invocation_id, decision_id),
    FOREIGN KEY (tenant_id, invocation_id)
        REFERENCES invocations (tenant_id, invocation_id) ON DELETE RESTRICT,
    CHECK (expires_at > issued_at),
    CHECK (revocation_checkpoint IS NULL OR revocation_checkpoint <> '')
);

CREATE INDEX authorization_decisions_freshness_idx
    ON authorization_decisions (tenant_id, invocation_id, expires_at DESC);

CREATE TABLE gr_evaluations (
    tenant_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    evaluation_id text NOT NULL CHECK (evaluation_id <> ''),
    phase text NOT NULL CHECK (phase IN ('pre_tool', 'post_tool')),
    content_digest bytea NOT NULL CHECK (octet_length(content_digest) = 32),
    transformed_content_digest bytea
        CHECK (transformed_content_digest IS NULL
            OR octet_length(transformed_content_digest) = 32),
    decision text NOT NULL CHECK (decision IN ('allow', 'block', 'transform')),
    policy_ref text NOT NULL CHECK (policy_ref <> ''),
    safe_metadata jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(safe_metadata) = 'object'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, invocation_id, evaluation_id),
    FOREIGN KEY (tenant_id, invocation_id)
        REFERENCES invocations (tenant_id, invocation_id) ON DELETE RESTRICT,
    CHECK (
        (decision = 'transform' AND transformed_content_digest IS NOT NULL)
        OR (decision <> 'transform' AND transformed_content_digest IS NULL)
    )
);

CREATE INDEX gr_evaluations_phase_idx
    ON gr_evaluations (tenant_id, invocation_id, phase, created_at DESC);

CREATE TABLE action_approvals (
    tenant_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    approval_id text NOT NULL CHECK (approval_id <> ''),
    approval_ref text NOT NULL CHECK (approval_ref <> ''),
    binding_digest bytea NOT NULL CHECK (octet_length(binding_digest) = 32),
    argument_digest bytea NOT NULL CHECK (octet_length(argument_digest) = 32),
    resource_digest bytea NOT NULL CHECK (octet_length(resource_digest) = 32),
    authorization_decision_id text NOT NULL,
    status text NOT NULL CHECK (status IN (
        'requested', 'approved', 'denied', 'expired', 'revoked', 'consumed'
    )),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    revocation_checkpoint text,
    PRIMARY KEY (tenant_id, invocation_id, approval_id),
    UNIQUE (tenant_id, approval_id),
    FOREIGN KEY (tenant_id, invocation_id)
        REFERENCES invocations (tenant_id, invocation_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, invocation_id, authorization_decision_id)
        REFERENCES authorization_decisions (tenant_id, invocation_id, decision_id)
        ON DELETE RESTRICT,
    CHECK (expires_at > issued_at),
    CHECK (
        (status = 'consumed' AND consumed_at IS NOT NULL
            AND consumed_at >= issued_at)
        OR (status <> 'consumed' AND consumed_at IS NULL)
    ),
    CHECK (revocation_checkpoint IS NULL OR revocation_checkpoint <> '')
);

CREATE INDEX action_approvals_status_idx
    ON action_approvals (tenant_id, invocation_id, status, expires_at);

CREATE TABLE execution_results (
    tenant_id uuid NOT NULL,
    invocation_id uuid NOT NULL,
    attempt_no integer,
    fence bigint,
    connector_instance_id uuid,
    credential_binding_id uuid,
    result_digest bytea CHECK (result_digest IS NULL OR octet_length(result_digest) = 32),
    safe_result jsonb CHECK (safe_result IS NULL OR jsonb_typeof(safe_result) = 'object'),
    classification text NOT NULL CHECK (classification IN (
        'confirmed_success', 'confirmed_failure', 'blocked', 'ambiguous'
    )),
    downstream_result_ref text,
    data_classification text NOT NULL CHECK (data_classification IN ('C0', 'C1', 'C2')),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, invocation_id),
    FOREIGN KEY (tenant_id, invocation_id)
        REFERENCES invocations (tenant_id, invocation_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, invocation_id, attempt_no, fence)
        REFERENCES invocation_attempts (tenant_id, invocation_id, attempt_no, fence)
        ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, credential_binding_id, connector_instance_id)
        REFERENCES credential_bindings (
            tenant_id, credential_binding_id, connector_instance_id
        )
        ON DELETE RESTRICT,
    CHECK ((attempt_no IS NULL) = (fence IS NULL)),
    CHECK ((connector_instance_id IS NULL) = (credential_binding_id IS NULL)),
    CHECK (classification <> 'confirmed_success' OR result_digest IS NOT NULL)
);

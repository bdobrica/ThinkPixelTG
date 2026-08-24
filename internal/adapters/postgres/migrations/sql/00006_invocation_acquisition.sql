-- +goose Up

ALTER TABLE idempotency_records
    ADD COLUMN recovery_count integer NOT NULL DEFAULT 0 CHECK (recovery_count >= 0),
    ADD COLUMN max_recoveries integer NOT NULL DEFAULT 0 CHECK (max_recoveries >= 0),
    ADD CONSTRAINT idempotency_recovery_bound CHECK (recovery_count <= max_recoveries);

-- Recovery is only meaningful while a logical invocation is being acquired.
CREATE INDEX idempotency_records_recovery_idx
    ON idempotency_records (claim_expires_at, tenant_id, idempotency_scope, idempotency_key)
    WHERE state = 'claimed' AND recovery_count < max_recoveries;

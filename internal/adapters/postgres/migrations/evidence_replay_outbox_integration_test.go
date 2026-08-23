//go:build integration

package migrations

import (
	"context"
	"testing"
)

func TestMigrationEvidenceReplayOutboxSchema(t *testing.T) {
	db := migratedTestDatabase(t)
	ctx := context.Background()
	assertSchemaObjects(t, ctx, db,
		[]string{"trusted_usage_events", "audit_events", "idempotency_records", "outbox_messages"},
		[]string{"trusted_usage_events_invocation_idx", "audit_events_invocation_idx",
			"idempotency_records_claim_idx", "outbox_messages_claim_idx", "outbox_messages_dead_letter_idx"},
	)
	seedInvocationDependencies(t, ctx, db)
	if _, err := db.ExecContext(ctx, invocationInsertSQL(testInvocationOne,
		"run-1", "call-1", "received", "NULL", "NULL")); err != nil {
		t.Fatalf("insert invocation: %v", err)
	}

	const usage = `INSERT INTO trusted_usage_events
		(tenant_id, event_id, invocation_id, accounting_key, dimension,
		 quantity, unit, attributes, occurred_at, recorded_at)
		VALUES ($1, $2, $3, 'run-1:call-1', 'tool_calls', 1, 'call', '{}', now(), now())`
	if _, err := db.ExecContext(ctx, usage, testTenantOne, testUsageEvent, testInvocationOne); err != nil {
		t.Fatalf("insert usage event: %v", err)
	}
	assertStatementFails(t, ctx, db, `INSERT INTO trusted_usage_events
		(tenant_id, event_id, invocation_id, accounting_key, dimension,
		 quantity, unit, attributes, occurred_at, recorded_at)
		VALUES ('`+testTenantOne+`', '019b0000-0000-7000-8000-000000000022',
		'`+testInvocationOne+`', 'run-1:call-1', 'tool_calls', 1, 'call', '{}', now(), now())`,
		"duplicate usage accounting boundary")

	const audit = `INSERT INTO audit_events
		(tenant_id, event_id, invocation_id, event_type, evidence_profile,
		 actor_class, actor_ref, outcome, correlation, safe_payload, payload_digest,
		 occurred_at, recorded_at)
		VALUES ($1, $2, $3, 'invocation.received', 'tg.evidence/v1alpha1',
		 'workload', 'worker-1', 'accepted', '{}', '{}', $4, now(), now())`
	if _, err := db.ExecContext(ctx, audit,
		testTenantOne, testAuditEvent, testInvocationOne, make([]byte, 32)); err != nil {
		t.Fatalf("insert audit event: %v", err)
	}
	assertStatementFails(t, ctx, db, `UPDATE audit_events SET outcome = 'changed'
		WHERE tenant_id = '`+testTenantOne+`' AND event_id = '`+testAuditEvent+`'`,
		"mutate append-only audit event")

	const idempotency = `INSERT INTO idempotency_records
		(tenant_id, idempotency_scope, idempotency_key, invocation_id,
		 request_digest, state, replay_status_code, replay_result_digest,
		 safe_replay_payload, created_at, updated_at, expires_at)
		VALUES ($1, 'logical_tool_call', 'run-1:call-1', $2, $3, 'completed',
		 200, $3, '{"state":"received"}', now(), now(), now() + interval '1 day')`
	if _, err := db.ExecContext(ctx, idempotency,
		testTenantOne, testInvocationOne, make([]byte, 32)); err != nil {
		t.Fatalf("insert idempotency record: %v", err)
	}
	assertStatementFails(t, ctx, db, `INSERT INTO idempotency_records
		(tenant_id, idempotency_scope, idempotency_key, request_digest, state,
		 created_at, updated_at, expires_at)
		VALUES ('`+testTenantOne+`', 'logical_tool_call', 'run-1:call-1',
		decode(repeat('00', 32), 'hex'), 'claimed', now(), now(), now() + interval '1 day')`,
		"reuse idempotency key")

	const outbox = `INSERT INTO outbox_messages
		(tenant_id, outbox_id, event_id, topic, event_type, safe_payload,
		 payload_digest, created_at, available_at, claim_owner, claim_until)
		VALUES ($1, $2, $3, 'audit', 'invocation.received', '{}', $4,
		 now(), now(), 'publisher-1', now() + interval '1 minute')`
	if _, err := db.ExecContext(ctx, outbox,
		testTenantOne, testOutbox, testAuditEvent, make([]byte, 32)); err != nil {
		t.Fatalf("insert outbox message: %v", err)
	}
	assertStatementFails(t, ctx, db, `UPDATE outbox_messages
		SET published_at = now(), publication_ref = 'sink:1'
		WHERE tenant_id = '`+testTenantOne+`' AND outbox_id = '`+testOutbox+`'`,
		"publish while retaining a claim")
	assertStatementFails(t, ctx, db, `INSERT INTO outbox_messages
		(tenant_id, outbox_id, event_id, topic, event_type, safe_payload,
		 payload_digest, created_at, available_at, claim_owner)
		VALUES ('`+testTenantOne+`', '019b0000-0000-7000-8000-000000000024',
		'019b0000-0000-7000-8000-000000000025', 'audit', 'test', '{}',
		decode(repeat('00', 32), 'hex'), now(), now(), 'publisher-1')`,
		"store incomplete outbox claim")
}

const (
	testUsageEvent = "019b0000-0000-7000-8000-000000000020"
	testAuditEvent = "019b0000-0000-7000-8000-000000000021"
	testOutbox     = "019b0000-0000-7000-8000-000000000023"
)

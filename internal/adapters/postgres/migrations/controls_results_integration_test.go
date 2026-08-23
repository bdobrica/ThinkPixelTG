//go:build integration

package migrations

import (
	"context"
	"testing"
)

func TestMigrationControlResultSchema(t *testing.T) {
	db := migratedTestDatabase(t)
	ctx := context.Background()
	assertSchemaObjects(t, ctx, db,
		[]string{"authorization_decisions", "gr_evaluations", "action_approvals", "execution_results"},
		[]string{"authorization_decisions_freshness_idx", "gr_evaluations_phase_idx", "action_approvals_status_idx"},
	)
	seedInvocationDependencies(t, ctx, db)
	if _, err := db.ExecContext(ctx, invocationInsertSQL(testInvocationOne,
		"run-1", "call-1", "received", "NULL", "NULL")); err != nil {
		t.Fatalf("insert invocation: %v", err)
	}

	const decision = `INSERT INTO authorization_decisions
		(tenant_id, invocation_id, decision_id, context_digest, argument_digest,
		 resource_digest, outcome, policy_ref, constraints, issued_at, expires_at, recorded_at)
		VALUES ($1, $2, 'decision-1', $3, $3, $3, 'allow', 'policy:v1', '{}',
		 now(), now() + interval '5 minutes', now())`
	if _, err := db.ExecContext(ctx, decision,
		testTenantOne, testInvocationOne, make([]byte, 32)); err != nil {
		t.Fatalf("insert authorization decision: %v", err)
	}

	const evaluation = `INSERT INTO gr_evaluations
		(tenant_id, invocation_id, evaluation_id, phase, content_digest,
		 transformed_content_digest, decision, policy_ref, safe_metadata, created_at)
		VALUES ($1, $2, 'evaluation-1', 'pre_tool', $3, $3,
		 'transform', 'guardrail:v1', '{}', now())`
	if _, err := db.ExecContext(ctx, evaluation,
		testTenantOne, testInvocationOne, make([]byte, 32)); err != nil {
		t.Fatalf("insert GR evaluation: %v", err)
	}

	const approval = `INSERT INTO action_approvals
		(tenant_id, invocation_id, approval_id, approval_ref, binding_digest,
		 argument_digest, resource_digest, authorization_decision_id, status,
		 issued_at, expires_at)
		VALUES ($1, $2, 'approval-1', 'ag:approval-1', $3, $3, $3,
		 'decision-1', 'approved', now(), now() + interval '5 minutes')`
	if _, err := db.ExecContext(ctx, approval,
		testTenantOne, testInvocationOne, make([]byte, 32)); err != nil {
		t.Fatalf("insert action approval: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO execution_results
		(tenant_id, invocation_id, result_digest, safe_result, classification,
		 data_classification, created_at)
		VALUES ($1, $2, $3, '{"status":"ok"}', 'confirmed_success', 'C1', now())`,
		testTenantOne, testInvocationOne, make([]byte, 32)); err != nil {
		t.Fatalf("insert execution result: %v", err)
	}

	assertStatementFails(t, ctx, db, `INSERT INTO gr_evaluations
		(tenant_id, invocation_id, evaluation_id, phase, content_digest,
		 decision, policy_ref, safe_metadata, created_at)
		VALUES ('`+testTenantOne+`', '`+testInvocationOne+`', 'evaluation-2',
		'pre_tool', decode(repeat('00', 32), 'hex'), 'transform', 'guardrail:v1', '{}', now())`,
		"store transform without transformed digest")
	assertStatementFails(t, ctx, db, `INSERT INTO action_approvals
		(tenant_id, invocation_id, approval_id, approval_ref, binding_digest,
		 argument_digest, resource_digest, authorization_decision_id, status,
		 issued_at, expires_at, consumed_at)
		VALUES ('`+testTenantOne+`', '`+testInvocationOne+`', 'approval-2', 'ag:approval-2',
		decode(repeat('00', 32), 'hex'), decode(repeat('00', 32), 'hex'),
		decode(repeat('00', 32), 'hex'), 'decision-1', 'approved', now(),
		now() + interval '5 minutes', now())`, "consume an unconsumed approval status")
	assertStatementFails(t, ctx, db, `INSERT INTO authorization_decisions
		(tenant_id, invocation_id, decision_id, context_digest, argument_digest,
		 resource_digest, outcome, policy_ref, constraints, issued_at, expires_at, recorded_at)
		VALUES ('`+testTenantOne+`', '`+testInvocationOne+`', 'decision-2',
		decode('00', 'hex'), decode(repeat('00', 32), 'hex'),
		decode(repeat('00', 32), 'hex'), 'allow', 'policy:v1', '{}', now(),
		now() + interval '5 minutes', now())`, "store malformed security digest")
}

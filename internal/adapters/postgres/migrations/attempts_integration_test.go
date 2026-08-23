//go:build integration

package migrations

import (
	"context"
	"strconv"
	"testing"
)

func TestMigrationAttemptSchema(t *testing.T) {
	db := migratedTestDatabase(t)
	ctx := context.Background()
	assertSchemaObjects(t, ctx, db,
		[]string{"invocation_attempts", "invocation_reconciliations"},
		[]string{"invocation_attempts_claim_idx", "invocation_reconciliations_outcome_idx"},
	)
	seedInvocationDependencies(t, ctx, db)
	if _, err := db.ExecContext(ctx, invocationInsertSQL(testInvocationOne,
		"run-1", "call-1", "received", "NULL", "NULL")); err != nil {
		t.Fatalf("insert invocation: %v", err)
	}

	const attempt = `INSERT INTO invocation_attempts
		(tenant_id, invocation_id, attempt_no, fence, owner_id, claimed_at,
		 lease_expires_at, evidence, started_at)
		VALUES ($1, $2, $3, $4, 'worker-1', now(), now() + interval '1 minute', '{}', now())`
	if _, err := db.ExecContext(ctx, attempt, testTenantOne, testInvocationOne, 1, 10); err != nil {
		t.Fatalf("insert first attempt: %v", err)
	}
	assertStatementFails(t, ctx, db, attemptLiteral(3, 30), "skip attempt sequence")
	assertStatementFails(t, ctx, db, attemptLiteral(2, 9), "decrease attempt fence")

	if _, err := db.ExecContext(ctx, `UPDATE invocation_attempts
		SET finished_at = now(), outcome_classification = 'unknown',
		    ambiguity_classification = 'possibly_applied',
		    retry_classification = 'reconcile_required',
		    downstream_request_ref = 'provider-request-1'
		WHERE tenant_id = $1 AND invocation_id = $2 AND attempt_no = 1`,
		testTenantOne, testInvocationOne); err != nil {
		t.Fatalf("finish ambiguous attempt: %v", err)
	}

	const reconciliation = `INSERT INTO invocation_reconciliations
		(tenant_id, invocation_id, sequence, attempt_no, fence, owner_id,
		 outcome, evidence_ref, evidence_digest, metadata, created_at)
		VALUES ($1, $2, 1, 1, 10, 'reconciler-1', 'still_unknown',
		 'provider-check-1', $3, '{}', now())`
	if _, err := db.ExecContext(ctx, reconciliation,
		testTenantOne, testInvocationOne, make([]byte, 32)); err != nil {
		t.Fatalf("insert reconciliation: %v", err)
	}
	assertStatementFails(t, ctx, db, `INSERT INTO invocation_reconciliations
		(tenant_id, invocation_id, sequence, attempt_no, fence, owner_id,
		 outcome, evidence_digest, metadata, created_at)
		VALUES ('`+testTenantOne+`', '`+testInvocationOne+`', 3, 1, 10,
		'reconciler-1', 'still_unknown', decode(repeat('00', 32), 'hex'), '{}', now())`,
		"skip reconciliation sequence")
}

func attemptLiteral(attemptNo int, fence int64) string {
	return `INSERT INTO invocation_attempts
		(tenant_id, invocation_id, attempt_no, fence, owner_id, claimed_at,
		 lease_expires_at, evidence, started_at)
		VALUES ('` + testTenantOne + `', '` + testInvocationOne + `', ` +
		strconv.Itoa(attemptNo) + `, ` + strconv.FormatInt(fence, 10) + `, 'worker-2', now(),
		now() + interval '1 minute', '{}', now())`
}

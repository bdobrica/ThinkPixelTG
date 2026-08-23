//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"testing"
)

func TestMigrationInvocationSchema(t *testing.T) {
	db := migratedTestDatabase(t)
	ctx := context.Background()

	assertSchemaObjects(t, ctx, db,
		[]string{"invocations"},
		[]string{"invocations_claim_idx"},
	)
	seedInvocationDependencies(t, ctx, db)

	const insert = `INSERT INTO invocations
		(tenant_id, invocation_id, run_id, tool_call_id, tool_id, tool_version,
		 argument_profile, argument_digest, resource_projection, resource_digest,
		 retry_class, state, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'github.pull.comment', '1.0.0',
		 'tg-cjson-v1', $5, '{"repository":"acme/widgets"}', $5,
		 'reconcile_before_retry', 'received', now(), now())`
	if _, err := db.ExecContext(ctx, insert, testTenantOne, testInvocationOne,
		"run-1", "call-1", make([]byte, 32)); err != nil {
		t.Fatalf("insert invocation: %v", err)
	}

	assertStatementFails(t, ctx, db, invocationInsertSQL(testInvocationTwo,
		"run-1", "call-1", "received", "NULL", "NULL"),
		"reuse logical invocation identity")
	assertStatementFails(t, ctx, db, invocationInsertSQL(testInvocationTwo,
		"run-2", "call-2", "succeeded", "NULL", "NULL"),
		"store terminal state without classification")
	assertStatementFails(t, ctx, db, invocationInsertSQL(testInvocationTwo,
		"run-2", "call-2", "received", "'not-terminal'", "now()"),
		"store terminal classification on nonterminal state")
	assertStatementFails(t, ctx, db, `INSERT INTO invocations
		(tenant_id, invocation_id, run_id, tool_call_id, tool_id, tool_version,
		 argument_profile, argument_digest, resource_projection, resource_digest,
		 retry_class, state, created_at, updated_at)
		VALUES ('019b0000-0000-7000-8000-000000000001',
		'019b0000-0000-7000-8000-000000000012', 'run-3', 'call-3',
		'github.pull.comment', '1.0.0', 'tg-cjson-v1', decode('00', 'hex'),
		'{}', decode(repeat('00', 32), 'hex'), 'safe', 'received', now(), now())`,
		"store malformed argument digest")
}

func invocationInsertSQL(invocationID, runID, callID, state, terminalCode, terminalAt string) string {
	return `INSERT INTO invocations
		(tenant_id, invocation_id, run_id, tool_call_id, tool_id, tool_version,
		 argument_profile, argument_digest, resource_projection, resource_digest,
		 retry_class, state, terminal_code, terminal_at, created_at, updated_at)
		VALUES ('` + testTenantOne + `', '` + invocationID + `', '` + runID + `',
		'` + callID + `', 'github.pull.comment', '1.0.0', 'tg-cjson-v1',
		decode(repeat('00', 32), 'hex'), '{}', decode(repeat('00', 32), 'hex'),
		'safe', '` + state + `', ` + terminalCode + `, ` + terminalAt + `, now(), now())`
}

func seedInvocationDependencies(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		"INSERT INTO tenants (tenant_id, created_at) VALUES ($1, now())", testTenantOne); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO tools (tool_id, created_at) VALUES ('github.pull.comment', now())"); err != nil {
		t.Fatalf("insert tool: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tool_versions
		(tool_id, version, state, definition, definition_digest, published_at)
		VALUES ('github.pull.comment', '1.0.0', 'published', '{}', $1, now())`,
		make([]byte, 32)); err != nil {
		t.Fatalf("insert tool version: %v", err)
	}
}

const (
	testTenantOne     = "019b0000-0000-7000-8000-000000000001"
	testInvocationOne = "019b0000-0000-7000-8000-000000000010"
	testInvocationTwo = "019b0000-0000-7000-8000-000000000011"
)

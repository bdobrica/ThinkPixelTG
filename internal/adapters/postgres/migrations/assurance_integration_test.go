//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMigrationEmptyDatabaseAndRepeatAreCurrent(t *testing.T) {
	db := emptyTestDatabase(t)
	provider, err := NewProvider(db, Files())
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := Up(t.Context(), provider); err != nil {
		t.Fatalf("migrate empty database: %v", err)
	}
	if err := Up(t.Context(), provider); err != nil {
		t.Fatalf("repeat current migration: %v", err)
	}
	version, err := provider.GetDBVersion(t.Context())
	if err != nil || version != 6 {
		t.Fatalf("database version = %d, %v; want 6", version, err)
	}
}

func TestMigrationPriorFixturePreservesData(t *testing.T) {
	db := emptyTestDatabase(t)
	provider, err := NewProvider(db, Files())
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := provider.UpTo(t.Context(), 5); err != nil {
		t.Fatalf("migrate prior fixture schema: %v", err)
	}
	const tenantID = "019b0000-0000-7000-8000-000000000016"
	if _, err := db.ExecContext(t.Context(), "INSERT INTO tenants (tenant_id, created_at) VALUES ($1, now())", tenantID); err != nil {
		t.Fatalf("seed prior tenant: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO idempotency_records
		(tenant_id, idempotency_scope, idempotency_key, request_digest, state,
		 created_at, updated_at, expires_at)
		VALUES ($1, 'tool-call', 'fixture', $2, 'expired', now(), now(), now() + interval '1 hour')`,
		tenantID, make([]byte, 32)); err != nil {
		t.Fatalf("seed prior idempotency record: %v", err)
	}
	if err := Up(t.Context(), provider); err != nil {
		t.Fatalf("upgrade prior fixture: %v", err)
	}
	var recoveryCount, maxRecoveries int
	if err := db.QueryRowContext(t.Context(), `SELECT recovery_count, max_recoveries
		FROM idempotency_records WHERE tenant_id = $1 AND idempotency_key = 'fixture'`, tenantID).
		Scan(&recoveryCount, &maxRecoveries); err != nil {
		t.Fatalf("read upgraded fixture: %v", err)
	}
	if recoveryCount != 0 || maxRecoveries != 0 {
		t.Fatalf("upgrade defaults = (%d, %d), want (0, 0)", recoveryCount, maxRecoveries)
	}
}

func TestMigrationForwardRecoveryAfterTransactionalFailure(t *testing.T) {
	db := emptyTestDatabase(t)
	files := fstest.MapFS{
		"00001_recovery.sql": {Data: []byte("-- +goose Up\nCREATE TABLE recovered (id bigint PRIMARY KEY REFERENCES prerequisite(id));\n")},
	}
	provider, err := newProvider(db, files, testManifest(t, files))
	if err != nil {
		t.Fatalf("create recovery provider: %v", err)
	}
	if err := Up(t.Context(), provider); err == nil {
		t.Fatal("migration without prerequisite error = nil")
	}
	assertRelationAbsent(t, db, "recovered")
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE prerequisite (id bigint PRIMARY KEY)"); err != nil {
		t.Fatalf("apply forward prerequisite: %v", err)
	}
	if err := Up(t.Context(), provider); err != nil {
		t.Fatalf("retry unchanged migration after forward recovery: %v", err)
	}
}

func TestMigrationSchemaIsBackupFriendlyAndIndexedForRuntimePaths(t *testing.T) {
	db := migratedTestDatabase(t)
	ctx := context.Background()
	var nonPermanent int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relkind IN ('r','p') AND c.relpersistence <> 'p'`).Scan(&nonPermanent); err != nil {
		t.Fatalf("inspect relation persistence: %v", err)
	}
	if nonPermanent != 0 {
		t.Fatalf("non-permanent application tables = %d, want 0", nonPermanent)
	}
	var sequenceCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=current_schema() AND c.relkind='S'
		AND c.relname NOT LIKE 'goose_db_version%'`).Scan(&sequenceCount); err != nil {
		t.Fatalf("inspect sequences: %v", err)
	}
	if sequenceCount != 0 {
		t.Fatalf("database-generated sequences = %d, want 0", sequenceCount)
	}
	assertIndexPlan(t, db, "SELECT invocation_id FROM invocations WHERE state='ready' ORDER BY updated_at, tenant_id, invocation_id LIMIT 1", "invocations_claim_idx")
	assertIndexPlan(t, db, "SELECT outbox_id FROM outbox_messages WHERE published_at IS NULL AND dead_lettered_at IS NULL ORDER BY available_at, tenant_id, outbox_id LIMIT 1", "outbox_messages_claim_idx")
	assertIndexPlan(t, db, "SELECT idempotency_key FROM idempotency_records WHERE state='claimed' AND recovery_count < max_recoveries ORDER BY claim_expires_at, tenant_id, idempotency_scope, idempotency_key LIMIT 1", "idempotency_records_recovery_idx")
}

func assertRelationAbsent(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var relation *string
	if err := db.QueryRowContext(t.Context(), "SELECT to_regclass($1)::text", name).Scan(&relation); err != nil {
		t.Fatalf("inspect relation %s: %v", name, err)
	}
	if relation != nil {
		t.Fatalf("relation %s survived failed transactional migration", name)
	}
}

func assertIndexPlan(t *testing.T, db *sql.DB, query, index string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), "SET enable_seqscan = off"); err != nil {
		t.Fatalf("disable sequential scans: %v", err)
	}
	rows, err := db.QueryContext(t.Context(), "EXPLAIN "+query)
	if err != nil {
		t.Fatalf("explain access path: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read query plan: %v", err)
	}
	if !strings.Contains(plan.String(), index) {
		t.Fatalf("query plan does not use %s:\n%s", index, plan.String())
	}
}

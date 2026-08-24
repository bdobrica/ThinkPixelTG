//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestMigrationCatalogSchema(t *testing.T) {
	db := migratedTestDatabase(t)
	ctx := context.Background()

	assertCatalogTables(t, ctx, db)
	assertCatalogIntegrity(t, ctx, db)
}

func migratedTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db := emptyTestDatabase(t)
	provider, err := NewProvider(db, Files())
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if err := Up(context.Background(), provider); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func emptyTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := os.Getenv("TPTG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TPTG_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open administration database: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	schema := "tptg_migration_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})

	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func assertSchemaObjects(t *testing.T, ctx context.Context, db *sql.DB, tables, indexes []string) {
	t.Helper()
	for _, table := range tables {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s does not exist", table)
		}
	}
	for _, index := range indexes {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = current_schema() AND indexname = $1)`, index).Scan(&exists); err != nil {
			t.Fatalf("query index %s: %v", index, err)
		}
		if !exists {
			t.Errorf("index %s does not exist", index)
		}
	}
}

func assertCatalogTables(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	const query = `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		AND table_name IN ('tenants', 'tools', 'tool_versions',
			'tenant_tool_exposures', 'connector_instances', 'credential_bindings')`
	var count int
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatalf("query catalog tables: %v", err)
	}
	if count != 6 {
		t.Fatalf("catalog table count = %d, want 6", count)
	}

	const indexQuery = `SELECT count(*) FROM pg_indexes
		WHERE schemaname = current_schema()
		AND indexname IN ('tenant_tool_exposures_enabled_idx',
			'connector_instances_enabled_type_idx',
			'credential_bindings_enabled_connector_idx')`
	if err := db.QueryRowContext(ctx, indexQuery).Scan(&count); err != nil {
		t.Fatalf("query catalog indexes: %v", err)
	}
	if count != 3 {
		t.Fatalf("catalog index count = %d, want 3", count)
	}
}

func assertCatalogIntegrity(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	const (
		tenantOne    = "019b0000-0000-7000-8000-000000000001"
		tenantTwo    = "019b0000-0000-7000-8000-000000000002"
		connectorOne = "019b0000-0000-7000-8000-000000000003"
		bindingOne   = "019b0000-0000-7000-8000-000000000004"
	)
	for _, tenant := range []string{tenantOne, tenantTwo} {
		if _, err := db.ExecContext(ctx,
			"INSERT INTO tenants (tenant_id, created_at) VALUES ($1, now())", tenant,
		); err != nil {
			t.Fatalf("insert tenant: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO tools (tool_id, created_at) VALUES ('github.pull.comment', now())",
	); err != nil {
		t.Fatalf("insert tool: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tool_versions
		(tool_id, version, state, definition, definition_digest, published_at)
		VALUES ('github.pull.comment', '1.0.0', 'published', '{}', $1, now())`,
		make([]byte, 32)); err != nil {
		t.Fatalf("insert published tool version: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tool_versions
		(tool_id, version, state, definition, definition_digest, published_at)
		VALUES ('github.pull.comment', '2.0.0', 'draft', '{}', $1, NULL)`,
		make([]byte, 32)); err != nil {
		t.Fatalf("insert draft tool version: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_tool_exposures
		(tenant_id, tool_id, version, enabled, updated_at)
		VALUES ($1, 'github.pull.comment', '1.0.0', true, now())`, tenantOne); err != nil {
		t.Fatalf("insert tool exposure: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO connector_instances
		(tenant_id, connector_instance_id, connector_type, destination_config,
		 config_digest, enabled, created_at, updated_at)
		VALUES ($1, $2, 'github', '{}', $3, true, now(), now())`,
		tenantOne, connectorOne, make([]byte, 32)); err != nil {
		t.Fatalf("insert connector instance: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO credential_bindings
		(tenant_id, credential_binding_id, connector_instance_id, provider_ref,
		 capability_metadata, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, 'vault:github/team-a', '{}', true, now(), now())`,
		tenantOne, bindingOne, connectorOne); err != nil {
		t.Fatalf("insert credential binding: %v", err)
	}

	assertStatementFails(t, ctx, db,
		"UPDATE tool_versions SET definition = '{\"changed\":true}' WHERE tool_id = 'github.pull.comment' AND version = '1.0.0'",
		"mutate published tool version")
	assertStatementFails(t, ctx, db, fmt.Sprintf(`INSERT INTO tenant_tool_exposures
		(tenant_id, tool_id, version, enabled, updated_at)
		VALUES ('%s', 'github.pull.comment', '2.0.0', true, now())`, tenantOne),
		"expose a draft tool version")
	assertStatementFails(t, ctx, db, fmt.Sprintf(`INSERT INTO credential_bindings
		(tenant_id, credential_binding_id, connector_instance_id, provider_ref,
		 capability_metadata, enabled, created_at, updated_at)
		VALUES ('%s', '019b0000-0000-7000-8000-000000000005', '%s',
		'vault:cross-tenant', '{}', true, now(), now())`, tenantTwo, connectorOne),
		"create cross-tenant credential binding")
	assertStatementFails(t, ctx, db, `INSERT INTO connector_instances
		(tenant_id, connector_instance_id, connector_type, destination_config,
		 config_digest, enabled, created_at, updated_at)
		VALUES ('019b0000-0000-7000-8000-000000000001',
		'019b0000-0000-7000-8000-000000000006', 'github', '{}',
		decode('00', 'hex'), true, now(), now())`, "store a malformed digest")
}

func assertStatementFails(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	statement string,
	description string,
) {
	t.Helper()
	if _, err := db.ExecContext(ctx, statement); err == nil {
		t.Fatalf("%s succeeded, want constraint failure", description)
	}
}

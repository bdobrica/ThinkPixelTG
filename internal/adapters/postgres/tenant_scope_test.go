package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestTenantScopedDBRejectsUnsafeQueries(t *testing.T) {
	t.Parallel()

	const tenantID = "019b0000-0000-7000-8000-000000000001"
	db := tenantScopedDB{DBTX: repositoryDBStub{}, tenantID: tenantID}
	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{name: "missing tenant predicate", sql: "SELECT invocation_id FROM invocations WHERE invocation_id=$1", args: []any{tenantID}},
		{name: "missing first placeholder", sql: "SELECT invocation_id FROM invocations WHERE tenant_id=$2", args: []any{tenantID, tenantID}},
		{name: "missing arguments", sql: "SELECT tenant_id FROM invocations WHERE tenant_id=$1"},
		{name: "caller tenant substitution", sql: "SELECT invocation_id FROM invocations WHERE tenant_id=$1", args: []any{"019b0000-0000-7000-8000-000000000002"}},
		{name: "non-string tenant", sql: "SELECT invocation_id FROM invocations WHERE tenant_id=$1", args: []any{[]byte(tenantID)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.Exec(context.Background(), test.sql, test.args...); !errors.Is(err, errUnsafeTenantQuery) {
				t.Fatalf("Exec() error = %v, want unsafe tenant query", err)
			}
			if _, err := db.Query(context.Background(), test.sql, test.args...); !errors.Is(err, errUnsafeTenantQuery) {
				t.Fatalf("Query() error = %v, want unsafe tenant query", err)
			}
			if err := db.QueryRow(context.Background(), test.sql, test.args...).Scan(); !errors.Is(err, errUnsafeTenantQuery) {
				t.Fatalf("QueryRow().Scan() error = %v, want unsafe tenant query", err)
			}
		})
	}
}

func TestTenantScopedDBAllowsBoundRepositoryTenant(t *testing.T) {
	t.Parallel()

	const tenantID = "019b0000-0000-7000-8000-000000000001"
	db := tenantScopedDB{DBTX: repositoryDBStub{}, tenantID: tenantID}
	if _, err := db.Exec(context.Background(), "DELETE FROM invocations WHERE tenant_id=$1", tenantID); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if err := db.QueryRow(context.Background(), "SELECT invocation_id FROM invocations WHERE tenant_id=$1", tenantID).Scan(); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("QueryRow().Scan() error = %v, want stub no rows", err)
	}
}

package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errUnsafeTenantQuery = errors.New("postgres tenant query must reference tenant_id and bind the repository tenant as $1")

// tenantScopedDB is a fail-closed guard around tenant-owned repository SQL. It
// does not replace SQL review or composite tenant keys, but prevents a repository
// method from accidentally omitting the tenant parameter or accepting one from
// its caller. Cross-tenant infrastructure workers intentionally use DBTX directly.
type tenantScopedDB struct {
	DBTX
	tenantID string
}

func (db tenantScopedDB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if !db.safe(sql, arguments) {
		return pgconn.CommandTag{}, errUnsafeTenantQuery
	}
	return db.DBTX.Exec(ctx, sql, arguments...)
}

func (db tenantScopedDB) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	if !db.safe(sql, arguments) {
		return nil, errUnsafeTenantQuery
	}
	return db.DBTX.Query(ctx, sql, arguments...)
}

func (db tenantScopedDB) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	if !db.safe(sql, arguments) {
		return tenantScopeErrorRow{}
	}
	return db.DBTX.QueryRow(ctx, sql, arguments...)
}

func (db tenantScopedDB) safe(sql string, arguments []any) bool {
	if !strings.Contains(strings.ToLower(sql), "tenant_id") || !strings.Contains(sql, "$1") || len(arguments) == 0 {
		return false
	}
	tenantID, ok := arguments[0].(string)
	return ok && tenantID == db.tenantID
}

type tenantScopeErrorRow struct{}

func (tenantScopeErrorRow) Scan(...any) error { return errUnsafeTenantQuery }

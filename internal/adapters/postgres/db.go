// Package postgres provides PostgreSQL-specific persistence building blocks.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is the query surface shared by pgx pools and transactions. Repositories
// accept this interface so the same statements participate in an explicit
// transaction without hiding transaction state in context values.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Transaction is the bounded query and lifecycle surface used by Transactor.
// It intentionally excludes pgx features that repositories do not need.
type Transaction interface {
	DBTX
	Commit(context.Context) error
	Rollback(context.Context) error
}

// Beginner is implemented by pgxpool.Pool and keeps pool construction outside
// the transaction abstraction.
type Beginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

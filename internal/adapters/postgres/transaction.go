package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Transactor owns transaction lifecycle. A callback must finish before the
// transaction can commit, which prevents repositories from leaking a live
// transaction beyond its intended atomic boundary.
type Transactor struct {
	beginner Beginner
}

func NewTransactor(beginner Beginner) (*Transactor, error) {
	if beginner == nil {
		return nil, errors.New("postgres transaction beginner is required")
	}
	return &Transactor{beginner: beginner}, nil
}

// WithinTransaction runs fn once in a transaction with the requested PostgreSQL
// options. It never retries: callers may retry only operations proven replay
// safe. Panics are rolled back and then propagated.
func (t *Transactor) WithinTransaction(
	ctx context.Context,
	options pgx.TxOptions,
	fn func(context.Context, DBTX) error,
) (err error) {
	if fn == nil {
		return errors.New("postgres transaction callback is required")
	}

	tx, err := t.beginner.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("begin postgres transaction: %w", err)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback(ctx)
			panic(recovered)
		}
	}()

	if err := fn(ctx, tx); err != nil {
		return joinRollbackError(err, tx.Rollback(ctx))
	}
	if err := tx.Commit(ctx); err != nil {
		return joinRollbackError(fmt.Errorf("commit postgres transaction: %w", err), tx.Rollback(ctx))
	}
	return nil
}

func joinRollbackError(primary, rollback error) error {
	if rollback == nil || errors.Is(rollback, pgx.ErrTxClosed) {
		return primary
	}
	return errors.Join(primary, fmt.Errorf("rollback postgres transaction: %w", rollback))
}

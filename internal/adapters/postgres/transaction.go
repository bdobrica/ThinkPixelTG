package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Transactor owns transaction lifecycle. A callback must finish before the
// transaction can commit, which prevents repositories from leaking a live
// transaction beyond its intended atomic boundary.
type Transactor struct {
	beginner        Beginner
	timeout         time.Duration
	rollbackTimeout time.Duration
}

func NewTransactor(beginner Beginner) (*Transactor, error) {
	return NewTransactorWithTimeout(beginner, 30*time.Second, 5*time.Second)
}

func NewTransactorWithTimeout(beginner Beginner, timeout, rollbackTimeout time.Duration) (*Transactor, error) {
	if beginner == nil {
		return nil, errors.New("postgres transaction beginner is required")
	}
	if timeout <= 0 || rollbackTimeout <= 0 {
		return nil, errors.New("postgres transaction timeouts must be positive")
	}
	return &Transactor{beginner: beginner, timeout: timeout, rollbackTimeout: rollbackTimeout}, nil
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
	txCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	tx, err := t.beginner.BeginTx(txCtx, options)
	if err != nil {
		return fmt.Errorf("begin postgres transaction: %w", err)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			_ = t.rollback(tx)
			panic(recovered)
		}
	}()

	if err := fn(txCtx, tx); err != nil {
		return joinRollbackError(err, t.rollback(tx))
	}
	if err := tx.Commit(txCtx); err != nil {
		return joinRollbackError(fmt.Errorf("commit postgres transaction: %w", err), t.rollback(tx))
	}
	return nil
}

func (t *Transactor) rollback(tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(context.Background(), t.rollbackTimeout)
	defer cancel()
	return tx.Rollback(ctx)
}

func joinRollbackError(primary, rollback error) error {
	if rollback == nil || errors.Is(rollback, pgx.ErrTxClosed) {
		return primary
	}
	return errors.Join(primary, fmt.Errorf("rollback postgres transaction: %w", rollback))
}

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errTest = errors.New("test error")

type fakeBeginner struct {
	tx         pgx.Tx
	err        error
	options    pgx.TxOptions
	beginCalls int
}

func (f *fakeBeginner) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	f.beginCalls++
	f.options = options
	return f.tx, f.err
}

type fakeTx struct {
	pgx.Tx
	commits     int
	rollbacks   int
	commitErr   error
	rollbackErr error
}

func (f *fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (f *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }

func (f *fakeTx) Commit(context.Context) error {
	f.commits++
	return f.commitErr
}

func (f *fakeTx) Rollback(context.Context) error {
	f.rollbacks++
	return f.rollbackErr
}

func TestTransactorCommitsSuccessfulCallback(t *testing.T) {
	t.Parallel()
	tx := &fakeTx{}
	beginner := &fakeBeginner{tx: tx}
	transactor, err := NewTransactor(beginner)
	if err != nil {
		t.Fatal(err)
	}
	options := pgx.TxOptions{IsoLevel: pgx.Serializable}
	called := false
	err = transactor.WithinTransaction(context.Background(), options, func(_ context.Context, db DBTX) error {
		called = true
		if db != tx {
			t.Fatal("callback did not receive transaction query surface")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithinTransaction() error = %v", err)
	}
	if !called || beginner.beginCalls != 1 || beginner.options != options || tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("unexpected lifecycle: called=%v begins=%d commits=%d rollbacks=%d", called, beginner.beginCalls, tx.commits, tx.rollbacks)
	}
}

func TestTransactorRollsBackCallbackError(t *testing.T) {
	t.Parallel()
	tx := &fakeTx{}
	transactor, _ := NewTransactor(&fakeBeginner{tx: tx})
	err := transactor.WithinTransaction(context.Background(), pgx.TxOptions{}, func(context.Context, DBTX) error {
		return errTest
	})
	if !errors.Is(err, errTest) || tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("error=%v commits=%d rollbacks=%d", err, tx.commits, tx.rollbacks)
	}
}

func TestTransactorRollsBackPanic(t *testing.T) {
	t.Parallel()
	tx := &fakeTx{}
	transactor, _ := NewTransactor(&fakeBeginner{tx: tx})
	defer func() {
		if recover() != "boom" || tx.rollbacks != 1 {
			t.Fatalf("panic was not propagated after rollback; rollbacks=%d", tx.rollbacks)
		}
	}()
	_ = transactor.WithinTransaction(context.Background(), pgx.TxOptions{}, func(context.Context, DBTX) error {
		panic("boom")
	})
}

func TestNewTransactorRejectsNilDependencies(t *testing.T) {
	t.Parallel()
	if _, err := NewTransactor(nil); err == nil {
		t.Fatal("NewTransactor(nil) error = nil")
	}
	transactor, _ := NewTransactor(&fakeBeginner{tx: &fakeTx{}})
	if err := transactor.WithinTransaction(context.Background(), pgx.TxOptions{}, nil); err == nil {
		t.Fatal("WithinTransaction(nil callback) error = nil")
	}
}

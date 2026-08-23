package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		err       error
		class     ErrorClass
		retriable bool
	}{
		{"nil", nil, ErrorNone, false},
		{"canceled", context.Canceled, ErrorCanceled, false},
		{"deadline", context.DeadlineExceeded, ErrorDeadline, false},
		{"serialization", &pgconn.PgError{Code: "40001"}, ErrorSerialization, true},
		{"deadlock", fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "40P01"}), ErrorDeadlock, true},
		{"constraint", &pgconn.PgError{Code: "23505"}, ErrorConstraint, false},
		{"connection", &pgconn.PgError{Code: "08006"}, ErrorConnection, true},
		{"other", errors.New("boom"), ErrorOther, false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyError(test.err); got != test.class {
				t.Errorf("ClassifyError() = %q, want %q", got, test.class)
			}
			if got := IsRetriable(test.err); got != test.retriable {
				t.Errorf("IsRetriable() = %v, want %v", got, test.retriable)
			}
		})
	}
}

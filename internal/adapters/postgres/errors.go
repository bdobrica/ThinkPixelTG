package postgres

import (
	"context"
	"errors"
	"net"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrorClass is a bounded operational classification. It is safe to use as a
// metric label and deliberately excludes SQL text and database object names.
type ErrorClass string

const (
	ErrorNone          ErrorClass = "none"
	ErrorCanceled      ErrorClass = "canceled"
	ErrorDeadline      ErrorClass = "deadline"
	ErrorSerialization ErrorClass = "serialization"
	ErrorDeadlock      ErrorClass = "deadlock"
	ErrorConnection    ErrorClass = "connection"
	ErrorConstraint    ErrorClass = "constraint"
	ErrorOther         ErrorClass = "other"
)

func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ErrorNone
	}
	if errors.Is(err, context.Canceled) {
		return ErrorCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorDeadline
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "40001":
			return ErrorSerialization
		case "40P01":
			return ErrorDeadlock
		case "57P01", "57P02", "57P03":
			return ErrorConnection
		}
		if len(postgresError.Code) >= 2 && postgresError.Code[:2] == "23" {
			return ErrorConstraint
		}
		if len(postgresError.Code) >= 2 && postgresError.Code[:2] == "08" {
			return ErrorConnection
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return ErrorConnection
	}
	if pgconn.SafeToRetry(err) {
		return ErrorConnection
	}
	return ErrorOther
}

// IsRetriable reports infrastructure/concurrency failures that may be retried
// only when the calling operation is independently known to be replay safe.
func IsRetriable(err error) bool {
	switch ClassifyError(err) {
	case ErrorSerialization, ErrorDeadlock, ErrorConnection:
		return true
	default:
		return false
	}
}

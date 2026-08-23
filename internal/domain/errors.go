package domain

import "fmt"

type ErrorCode string

const (
	CodeInvalidArgument ErrorCode = "invalid_argument"
	CodeNotFound        ErrorCode = "not_found"
	CodeConflict        ErrorCode = "conflict"
	CodeForbidden       ErrorCode = "forbidden"
	CodeUnavailable     ErrorCode = "unavailable"
	CodeInternal        ErrorCode = "internal"
)

type Error struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (err *Error) Error() string {
	if err.Message == "" {
		return string(err.Code)
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Message)
}
func (err *Error) Unwrap() error { return err.Cause }
func NewError(code ErrorCode, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

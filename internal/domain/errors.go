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

	// Public error codes cross transport boundaries and are therefore stable.
	// Callers may branch on these values; causes and messages remain internal.
	CodeUnauthenticated             ErrorCode = "unauthenticated"
	CodeIdentityProviderUnavailable ErrorCode = "identity_provider_unavailable"
	CodeNotReady                    ErrorCode = "not_ready"
	CodeServiceUnavailable          ErrorCode = "service_unavailable"
	CodeInvalidContext              ErrorCode = "invalid_context"
	CodeToolNotFound                ErrorCode = "tool_not_found"
	CodeToolCallNotFound            ErrorCode = "tool_call_not_found"
	CodeInvalidArguments            ErrorCode = "invalid_arguments"
	CodeReplayConflict              ErrorCode = "replay_conflict"
	CodeAuthorizationDenied         ErrorCode = "authorization_denied"
	CodeApprovalRequired            ErrorCode = "approval_required"
	CodeApprovalInvalid             ErrorCode = "approval_invalid"
	CodeGuardrailBlocked            ErrorCode = "guardrail_blocked"
	CodeCredentialUnavailable       ErrorCode = "credential_unavailable"
	CodeConnectorError              ErrorCode = "connector_error"
	CodeDownstreamRejected          ErrorCode = "downstream_rejected"
	CodeAmbiguousOutcome            ErrorCode = "ambiguous_outcome"
	CodeResultBlocked               ErrorCode = "result_blocked"
	CodeRateLimited                 ErrorCode = "rate_limited"
	CodeBudgetExhausted             ErrorCode = "budget_exhausted"
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

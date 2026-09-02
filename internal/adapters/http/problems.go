package http

import (
	"errors"
	stdhttp "net/http"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

type problemSpec struct {
	status int
	title  string
}

var publicProblems = map[domain.ErrorCode]problemSpec{
	domain.CodeUnauthenticated:             {stdhttp.StatusUnauthorized, "Authentication required"},
	domain.CodeIdentityProviderUnavailable: {stdhttp.StatusServiceUnavailable, "Identity provider is unavailable"},
	domain.CodeNotReady:                    {stdhttp.StatusServiceUnavailable, "Service is not ready"},
	domain.CodeServiceUnavailable:          {stdhttp.StatusServiceUnavailable, "Service is unavailable"},
	domain.CodeInvalidContext:              {stdhttp.StatusUnauthorized, "Authentication context is invalid"},
	domain.CodeToolNotFound:                {stdhttp.StatusNotFound, "Tool version was not found"},
	domain.CodeToolCallNotFound:            {stdhttp.StatusNotFound, "Tool call was not found"},
	domain.CodeInvalidArguments:            {stdhttp.StatusBadRequest, "Request arguments are invalid"},
	domain.CodeReplayConflict:              {stdhttp.StatusConflict, "Idempotency key conflicts with an existing tool call"},
	domain.CodeAuthorizationDenied:         {stdhttp.StatusForbidden, "Tool call is not authorized"},
	domain.CodeApprovalRequired:            {stdhttp.StatusConflict, "Approval is required"},
	domain.CodeApprovalInvalid:             {stdhttp.StatusForbidden, "Approval is invalid"},
	domain.CodeGuardrailBlocked:            {stdhttp.StatusForbidden, "Tool call was blocked"},
	domain.CodeCredentialUnavailable:       {stdhttp.StatusServiceUnavailable, "Credential is unavailable"},
	domain.CodeConnectorError:              {stdhttp.StatusBadGateway, "Connector execution failed"},
	domain.CodeDownstreamRejected:          {stdhttp.StatusBadGateway, "Downstream operation was rejected"},
	domain.CodeAmbiguousOutcome:            {stdhttp.StatusConflict, "Downstream outcome is ambiguous"},
	domain.CodeResultBlocked:               {stdhttp.StatusForbidden, "Tool result was blocked"},
	domain.CodeRateLimited:                 {stdhttp.StatusTooManyRequests, "Rate limit exceeded"},
	domain.CodeBudgetExhausted:             {stdhttp.StatusForbidden, "Budget is exhausted"},
	domain.CodeInternal:                    {stdhttp.StatusInternalServerError, "Internal server error"},
}

func writeDomainProblem(writer stdhttp.ResponseWriter, request *stdhttp.Request, err error, fallback domain.ErrorCode) {
	code := fallback
	var classified *domain.Error
	if errors.As(err, &classified) {
		code = publicErrorCode(classified.Code, fallback)
	}
	writePublicProblem(writer, request, code)
}

func publicErrorCode(code, fallback domain.ErrorCode) domain.ErrorCode {
	if _, ok := publicProblems[code]; ok {
		return code
	}
	switch code {
	case domain.CodeInvalidArgument:
		return domain.CodeInvalidArguments
	case domain.CodeNotFound:
		return domain.CodeToolNotFound
	case domain.CodeConflict:
		return domain.CodeReplayConflict
	case domain.CodeForbidden:
		return domain.CodeAuthorizationDenied
	case domain.CodeUnavailable:
		return fallback
	default:
		return fallback
	}
}

func writePublicProblem(writer stdhttp.ResponseWriter, request *stdhttp.Request, code domain.ErrorCode) {
	writePublicProblemAt(writer, request, code, request.URL.Path)
}

func writePublicProblemAt(writer stdhttp.ResponseWriter, request *stdhttp.Request, code domain.ErrorCode, instance string) {
	spec, ok := publicProblems[code]
	if !ok {
		code = domain.CodeInternal
		spec = publicProblems[code]
	}
	WriteProblem(writer, request, Problem{
		Status:   spec.status,
		Type:     "urn:thinkpixeltg:problem:" + string(code),
		Title:    spec.title,
		Code:     string(code),
		Instance: instance,
	})
}

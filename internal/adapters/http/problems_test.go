package http

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

func TestStablePublicProblemTaxonomy(t *testing.T) {
	tests := map[domain.ErrorCode]int{
		domain.CodeUnauthenticated:             401,
		domain.CodeIdentityProviderUnavailable: 503,
		domain.CodeInvalidContext:              401,
		domain.CodeToolNotFound:                404,
		domain.CodeToolCallNotFound:            404,
		domain.CodeInvalidArguments:            400,
		domain.CodeReplayConflict:              409,
		domain.CodeAuthorizationDenied:         403,
		domain.CodeApprovalRequired:            409,
		domain.CodeApprovalInvalid:             403,
		domain.CodeGuardrailBlocked:            403,
		domain.CodeCredentialUnavailable:       503,
		domain.CodeConnectorError:              502,
		domain.CodeDownstreamRejected:          502,
		domain.CodeAmbiguousOutcome:            409,
		domain.CodeResultBlocked:               403,
		domain.CodeRateLimited:                 429,
		domain.CodeBudgetExhausted:             403,
		domain.CodeNotReady:                    503,
		domain.CodeServiceUnavailable:          503,
		domain.CodeInternal:                    500,
	}
	for code, wantStatus := range tests {
		t.Run(string(code), func(t *testing.T) {
			request := httptest.NewRequest(stdhttp.MethodPost, "/v1/tool-calls", nil)
			response := httptest.NewRecorder()
			response.Header().Set(RequestIDHeader, "request-opaque")
			writeDomainProblem(response, request, domain.NewError(code, "secret provider detail", errors.New("token=canary")), domain.CodeInternal)
			if response.Code != wantStatus || response.Header().Get("Content-Type") != "application/problem+json" || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %d %#v %s", response.Code, response.Header(), response.Body.String())
			}
			var problem Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != string(code) || problem.CorrelationID != "request-opaque" || problem.Instance != "/v1/tool-calls" || !strings.HasSuffix(problem.Type, ":"+string(code)) {
				t.Fatalf("problem = %#v", problem)
			}
			if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "canary") {
				t.Fatalf("internal content leaked: %s", response.Body.String())
			}
		})
	}
}

func TestUnknownProblemClassificationFailsClosed(t *testing.T) {
	request := httptest.NewRequest(stdhttp.MethodGet, "/v1/tools", nil)
	response := httptest.NewRecorder()
	writeDomainProblem(response, request, domain.NewError("provider_secret_error", "sensitive", nil), domain.CodeInternal)
	if response.Code != 500 || !strings.Contains(response.Body.String(), `"code":"internal"`) || strings.Contains(response.Body.String(), "sensitive") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

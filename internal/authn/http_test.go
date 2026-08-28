package authn

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type verifierFunc func(context.Context, string) (Claims, error)

func (verify verifierFunc) Verify(ctx context.Context, token string) (Claims, error) {
	return verify(ctx, token)
}

func testClaims() Claims {
	return Claims{
		Issuer: "https://issuer.example", Subject: "subject-1",
		Raw: map[string]any{
			"tenant_id": "tenant-1", "run": "run-1", "agent": "agent-1",
			"agent_version": "v1", "workload_id": "spiffe://example/workload",
			"act": map[string]any{"sub": "actor-1"},
		},
	}
}

func TestHTTPAuthenticatorBuildsTypedPrincipalAndRestoresBody(t *testing.T) {
	authenticator, err := NewHTTPAuthenticator(verifierFunc(func(_ context.Context, token string) (Claims, error) {
		if token != "valid-token" {
			t.Fatalf("token = %q", token)
		}
		return testClaims(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"tenant_id":"tenant-1","run_id":"run-1","arguments":{"tenant_id":"tool-data"}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	ctx, err := authenticator.Authenticate(request.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.TenantID != "tenant-1" || principal.Subject != "subject-1" || principal.Actor != "actor-1" || principal.AgentID != "agent-1" || principal.AgentVersion != "v1" || principal.RunID != "run-1" || principal.WorkloadID != "spiffe://example/workload" {
		t.Fatalf("principal = %#v, present = %v", principal, ok)
	}
	restored, readErr := io.ReadAll(request.Body)
	if readErr != nil || string(restored) != body {
		t.Fatalf("restored body = %q, error = %v", restored, readErr)
	}
}

func TestHTTPAuthenticatorRejectsMalformedBearerAndForgedHeaders(t *testing.T) {
	authenticator, _ := NewHTTPAuthenticator(verifierFunc(func(context.Context, string) (Claims, error) {
		return testClaims(), nil
	}))
	tests := []struct {
		name   string
		header http.Header
	}{
		{name: "missing", header: http.Header{}},
		{name: "basic", header: http.Header{"Authorization": {"Basic abc"}}},
		{name: "multiple", header: http.Header{"Authorization": {"Bearer one", "Bearer two"}}},
		{name: "combined", header: http.Header{"Authorization": {"Bearer one, Bearer two"}}},
		{name: "forwarded", header: http.Header{"Authorization": {"Bearer valid"}, "Forwarded": {"for=192.0.2.1"}}},
		{name: "x-forwarded", header: http.Header{"Authorization": {"Bearer valid"}, "X-Forwarded-User": {"admin"}}},
		{name: "governance", header: http.Header{"Authorization": {"Bearer valid"}, "X-Tenant-ID": {"tenant-1"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/tools", nil)
			request.Header = test.header
			if _, err := authenticator.Authenticate(request.Context(), request); err == nil {
				t.Fatal("Authenticate() error = nil")
			}
		})
	}
}

func TestHTTPAuthenticatorRejectsAmbiguousClaimsAndConflictingBodyHints(t *testing.T) {
	tests := []struct {
		name   string
		claims Claims
		body   string
	}{
		{name: "missing tenant", claims: Claims{Issuer: "https://issuer.example", Subject: "subject", Raw: map[string]any{}}},
		{name: "tenant aliases disagree", claims: Claims{Issuer: "https://issuer.example", Subject: "subject", Raw: map[string]any{"tenant": "one", "tenant_id": "two"}}},
		{name: "body tenant conflict", claims: testClaims(), body: `{"tenant_id":"other"}`},
		{name: "body unknown trusted dimension", claims: testClaims(), body: `{"principal_id":"other"}`},
		{name: "body malformed hint", claims: testClaims(), body: `{"tenant_id":["tenant-1"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authenticator, _ := NewHTTPAuthenticator(verifierFunc(func(context.Context, string) (Claims, error) {
				return test.claims, nil
			}))
			request := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer token")
			request.Header.Set("Content-Type", "application/json")
			if _, err := authenticator.Authenticate(request.Context(), request); err == nil {
				t.Fatal("Authenticate() error = nil")
			}
		})
	}
}

func TestHTTPAuthenticatorExemptionAndDependencyFailure(t *testing.T) {
	called := false
	authenticator, err := NewHTTPAuthenticator(verifierFunc(func(context.Context, string) (Claims, error) {
		called = true
		return Claims{}, &VerificationError{Code: CodeDependencyUnavailable, err: errors.New("offline")}
	}), "/livez")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.Authenticate(context.Background(), httptest.NewRequest(http.MethodGet, "/livez", nil)); err != nil || called {
		t.Fatalf("exempt request error = %v, verifier called = %v", err, called)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/tools", nil)
	request.Header.Set("Authorization", "Bearer token")
	_, err = authenticator.Authenticate(request.Context(), request)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPStatus() != http.StatusServiceUnavailable || err.Error() != string(CodeDependencyUnavailable) {
		t.Fatalf("error = %#v", err)
	}
}

func TestNewHTTPAuthenticatorValidatesConfiguration(t *testing.T) {
	if _, err := NewHTTPAuthenticator(nil); err == nil {
		t.Fatal("NewHTTPAuthenticator(nil) error = nil")
	}
	verifier := verifierFunc(func(context.Context, string) (Claims, error) { return Claims{}, nil })
	if _, err := NewHTTPAuthenticator(verifier, "relative"); err == nil {
		t.Fatal("relative exemption error = nil")
	}
}

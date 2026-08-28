package authn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxIdentityValueBytes = 512

var governanceHeaders = map[string]struct{}{
	"X-Tenant-Id":     {},
	"X-Principal-Id":  {},
	"X-Subject-Id":    {},
	"X-Actor-Id":      {},
	"X-Agent-Id":      {},
	"X-Agent-Version": {},
	"X-Run-Id":        {},
	"X-Workload-Id":   {},
}

// TokenVerifier verifies a compact bearer credential without interpreting HTTP.
type TokenVerifier interface {
	Verify(context.Context, string) (Claims, error)
}

// Principal is the only authenticated identity representation consumers should
// read. Empty optional fields convey no authority.
type Principal struct {
	Issuer       string
	Subject      string
	Actor        string
	TenantID     string
	AgentID      string
	AgentVersion string
	RunID        string
	WorkloadID   string
}

type principalContextKey struct{}

func withPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns the authenticated principal, if present.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

// HTTPAuthenticator adapts token verification to the HTTP server authentication
// hook. Exempt paths are exact matches and should be limited to operational
// endpoints which intentionally have no caller identity.
type HTTPAuthenticator struct {
	verifier TokenVerifier
	exempt   map[string]struct{}
}

func NewHTTPAuthenticator(verifier TokenVerifier, exemptPaths ...string) (*HTTPAuthenticator, error) {
	if verifier == nil {
		return nil, errors.New("token verifier is required")
	}
	exempt := make(map[string]struct{}, len(exemptPaths))
	for _, path := range exemptPaths {
		if path == "" || path[0] != '/' {
			return nil, fmt.Errorf("invalid authentication-exempt path %q", path)
		}
		exempt[path] = struct{}{}
	}
	return &HTTPAuthenticator{verifier: verifier, exempt: exempt}, nil
}

// Authenticate implements the HTTP server's authentication hook.
func (authenticator *HTTPAuthenticator) Authenticate(ctx context.Context, request *http.Request) (context.Context, error) {
	if request == nil {
		return ctx, newHTTPError(http.StatusUnauthorized, CodeInvalidToken)
	}
	if _, exempt := authenticator.exempt[request.URL.Path]; exempt {
		return ctx, nil
	}
	if hasForgedHeader(request.Header) {
		return ctx, newHTTPError(http.StatusUnauthorized, CodeInvalidToken)
	}
	token, err := bearerToken(request.Header.Values("Authorization"))
	if err != nil {
		return ctx, newHTTPError(http.StatusUnauthorized, CodeInvalidToken)
	}
	claims, err := authenticator.verifier.Verify(ctx, token)
	if err != nil {
		var verification *VerificationError
		if errors.As(err, &verification) && verification.Code == CodeDependencyUnavailable {
			return ctx, newHTTPError(http.StatusServiceUnavailable, verification.Code)
		}
		return ctx, newHTTPError(http.StatusUnauthorized, CodeInvalidToken)
	}
	principal, err := principalFromClaims(claims)
	if err != nil {
		return ctx, newHTTPError(http.StatusUnauthorized, CodeInvalidToken)
	}
	if err := checkBodyIdentityHints(request, principal); err != nil {
		return ctx, newHTTPError(http.StatusUnauthorized, CodeInvalidToken)
	}
	return withPrincipal(ctx, principal), nil
}

type HTTPError struct {
	status int
	code   ErrorCode
}

func newHTTPError(status int, code ErrorCode) *HTTPError {
	return &HTTPError{status: status, code: code}
}
func (err *HTTPError) Error() string   { return string(err.code) }
func (err *HTTPError) HTTPStatus() int { return err.status }

func bearerToken(values []string) (string, error) {
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", errors.New("exactly one authorization value is required")
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.New("authorization value is not a bearer credential")
	}
	return parts[1], nil
}

func hasForgedHeader(headers http.Header) bool {
	for name := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "Forwarded" || strings.HasPrefix(canonical, "X-Forwarded-") {
			return true
		}
		if _, forbidden := governanceHeaders[canonical]; forbidden {
			return true
		}
	}
	return false
}

func principalFromClaims(claims Claims) (Principal, error) {
	principal := Principal{Issuer: claims.Issuer, Subject: claims.Subject}
	if !validIdentityValue(principal.Issuer) || !validIdentityValue(principal.Subject) {
		return Principal{}, errors.New("issuer and subject are required")
	}
	var err error
	if principal.TenantID, err = uniqueStringClaim(claims.Raw, "tenant_id", "tenant"); err != nil || principal.TenantID == "" {
		return Principal{}, errors.New("unambiguous tenant claim is required")
	}
	if principal.RunID, err = uniqueStringClaim(claims.Raw, "run", "run_id"); err != nil {
		return Principal{}, err
	}
	if principal.AgentID, err = uniqueStringClaim(claims.Raw, "agent", "agent_id"); err != nil {
		return Principal{}, err
	}
	if principal.AgentVersion, err = uniqueStringClaim(claims.Raw, "agent_version"); err != nil {
		return Principal{}, err
	}
	if principal.WorkloadID, err = uniqueStringClaim(claims.Raw, "workload_id", "azp"); err != nil {
		return Principal{}, err
	}
	if principal.Actor, err = actorClaim(claims.Raw["act"]); err != nil {
		return Principal{}, err
	}
	return principal, nil
}

func uniqueStringClaim(claims map[string]any, names ...string) (string, error) {
	value := ""
	for _, name := range names {
		candidate, exists := claims[name]
		if !exists {
			continue
		}
		text, ok := candidate.(string)
		if !ok || !validIdentityValue(text) || value != "" && value != text {
			return "", errors.New("identity claim is malformed or ambiguous")
		}
		value = text
	}
	return value, nil
}

func actorClaim(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok && validIdentityValue(text) {
		return text, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return "", errors.New("actor claim is malformed")
	}
	actor, err := uniqueStringClaim(object, "sub")
	if err != nil || actor == "" {
		return "", errors.New("actor claim is malformed")
	}
	return actor, nil
}

func validIdentityValue(value string) bool {
	return value != "" && len(value) <= maxIdentityValueBytes && strings.TrimSpace(value) == value
}

func checkBodyIdentityHints(request *http.Request, principal Principal) error {
	if request.Body == nil || request.Body == http.NoBody || !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		return nil
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return err
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var envelope map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&envelope); err != nil {
		return nil // request schema handling owns malformed application JSON
	}
	trusted := map[string]string{
		"tenant_id": principal.TenantID, "principal_id": principal.Subject,
		"subject": principal.Subject, "subject_id": principal.Subject, "actor_id": principal.Actor,
		"agent_id": principal.AgentID, "agent_version": principal.AgentVersion,
		"run_id": principal.RunID, "workload_id": principal.WorkloadID,
	}
	for name, expected := range trusted {
		raw, exists := envelope[name]
		if !exists {
			continue
		}
		var supplied string
		if json.Unmarshal(raw, &supplied) != nil || expected == "" || supplied != expected {
			return errors.New("body identity hint conflicts with authenticated principal")
		}
	}
	return nil
}

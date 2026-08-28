package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthenticationAdversarialTokenVerification(t *testing.T) {
	idp := newIdentityServer(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	provider, err := NewOIDCProvider(idp.profile(&now))
	if err != nil {
		t.Fatal(err)
	}
	valid := func() map[string]any {
		return map[string]any{
			"iss": "https://issuer.example", "sub": "subject-1", "aud": "thinkpixeltg",
			"resource": "urn:thinkpixel:tg", "exp": now.Add(time.Minute).Unix(),
			"nbf": now.Add(-time.Second).Unix(), "tenant_id": "tenant-1", "agent_id": "agent-1",
			"agent_version": "v1", "run_id": "run-1",
		}
	}
	if _, err := provider.Verify(context.Background(), signRSA(t, idp.key, idp.kid, "RS256", valid())); err != nil {
		t.Fatalf("valid control token: %v", err)
	}
	attackerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		algorithm string
		key       *rsa.PrivateKey
		mutate    func(map[string]any)
		code      ErrorCode
	}{
		{name: "bad signature", algorithm: "RS256", key: attackerKey, mutate: func(map[string]any) {}, code: CodeInvalidToken},
		{name: "unconfigured issuer", algorithm: "RS256", key: idp.key, mutate: func(claims map[string]any) { claims["iss"] = "https://attacker.example" }, code: CodeUnsupportedIssuer},
		{name: "wrong audience", algorithm: "RS256", key: idp.key, mutate: func(claims map[string]any) { claims["aud"] = "attacker" }, code: CodeInvalidToken},
		{name: "wrong resource", algorithm: "RS256", key: idp.key, mutate: func(claims map[string]any) { claims["resource"] = "urn:attacker" }, code: CodeInvalidToken},
		{name: "disallowed algorithm", algorithm: "PS256", key: idp.key, mutate: func(map[string]any) {}, code: CodeInvalidToken},
		{name: "expired", algorithm: "RS256", key: idp.key, mutate: func(claims map[string]any) { claims["exp"] = now.Add(-6 * time.Second).Unix() }, code: CodeInvalidToken},
		{name: "not yet valid", algorithm: "RS256", key: idp.key, mutate: func(claims map[string]any) { claims["nbf"] = now.Add(6 * time.Second).Unix() }, code: CodeInvalidToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := valid()
			test.mutate(claims)
			_, verifyErr := provider.Verify(context.Background(), signRSA(t, test.key, idp.kid, test.algorithm, claims))
			assertCode(t, verifyErr, test.code)
		})
	}
}

func TestAuthenticationAdversarialKeyRotation(t *testing.T) {
	idp := newIdentityServer(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	provider, err := NewOIDCProvider(idp.profile(&now))
	if err != nil {
		t.Fatal(err)
	}
	claims := func() map[string]any {
		return map[string]any{"iss": "https://issuer.example", "sub": "subject", "aud": "thinkpixeltg", "resource": "urn:thinkpixel:tg", "exp": now.Add(10 * time.Minute).Unix()}
	}
	if _, err := provider.Verify(context.Background(), signRSA(t, idp.key, idp.kid, "RS256", claims())); err != nil {
		t.Fatal(err)
	}
	rotated, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp.mu.Lock()
	idp.key, idp.kid = rotated, "key-rotated"
	idp.mu.Unlock()
	if _, err := provider.Verify(context.Background(), signRSA(t, rotated, "key-rotated", "RS256", claims())); err != nil {
		t.Fatalf("rotated signing key rejected: %v", err)
	}
	_, err = provider.Verify(context.Background(), signRSA(t, idp.key, "retired-unknown", "RS256", claims()))
	assertCode(t, err, CodeInvalidToken)
}

func TestAuthenticationAdversarialGovernedIdentity(t *testing.T) {
	complete := testClaims()
	tests := []struct {
		name   string
		claims Claims
		body   string
	}{
		{name: "missing agent", claims: claimsWithout(complete, "agent", "agent_id")},
		{name: "missing agent version", claims: claimsWithout(complete, "agent_version")},
		{name: "missing run", claims: claimsWithout(complete, "run", "run_id")},
		{name: "cross tenant claim substitution", claims: withRawClaim(complete, "tenant", "tenant-2")},
		{name: "cross run claim substitution", claims: withRawClaim(complete, "run_id", "run-2")},
		{name: "cross tenant body substitution", claims: complete, body: `{"tenant_id":"tenant-2"}`},
		{name: "cross run body substitution", claims: complete, body: `{"run_id":"run-2"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authenticator, _ := NewHTTPAuthenticator(verifierFunc(func(context.Context, string) (Claims, error) { return test.claims, nil }))
			request := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer token")
			request.Header.Set("Content-Type", "application/json")
			ctx, authenticateErr := authenticator.Authenticate(request.Context(), request)
			if authenticateErr == nil {
				_, authenticateErr = DeriveGovernedContext(ctx)
			}
			if authenticateErr == nil {
				t.Fatal("forged or incomplete governed identity accepted")
			}
		})
	}
}

func TestAuthenticationAdversarialForgedProxyHeaders(t *testing.T) {
	authenticator, _ := NewHTTPAuthenticator(verifierFunc(func(context.Context, string) (Claims, error) { return testClaims(), nil }))
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Client-Cert", "X-Tenant-ID", "X-Run-ID", "X-Workload-ID"} {
		t.Run(header, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/tools", nil)
			request.Header.Set("Authorization", "Bearer token")
			request.Header.Set(header, "attacker-controlled")
			if _, err := authenticator.Authenticate(request.Context(), request); err == nil {
				t.Fatal("forged proxy/governance header accepted")
			}
		})
	}
}

func claimsWithout(original Claims, names ...string) Claims {
	result := cloneClaims(original)
	for _, name := range names {
		delete(result.Raw, name)
	}
	return result
}

func withRawClaim(original Claims, name string, value any) Claims {
	result := cloneClaims(original)
	result.Raw[name] = value
	return result
}

func cloneClaims(original Claims) Claims {
	result := original
	result.Raw = make(map[string]any, len(original.Raw))
	for name, value := range original.Raw {
		result.Raw[name] = value
	}
	return result
}

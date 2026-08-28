package authn

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

type identityServer struct {
	mu              sync.Mutex
	key             *rsa.PrivateKey
	kid             string
	outage          bool
	discoveryIssuer string
	requests        int
}

func newIdentityServer(t *testing.T) *identityServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &identityServer{key: key, kid: "key-1"}
}

func (idp *identityServer) roundTrip(request *http.Request) (*http.Response, error) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	idp.requests++
	if idp.outage {
		return testResponse(http.StatusServiceUnavailable, nil), nil
	}
	if request.URL.Path == "/.well-known/openid-configuration" {
		issuer := "https://issuer.example"
		if idp.discoveryIssuer != "" {
			issuer = idp.discoveryIssuer
		}
		body, _ := json.Marshal(map[string]any{"issuer": issuer, "jwks_uri": "https://issuer.example/jwks"})
		return testResponse(http.StatusOK, body), nil
	}
	if request.URL.Path != "/jwks" {
		return testResponse(http.StatusNotFound, nil), nil
	}
	body, _ := json.Marshal(map[string]any{"keys": []any{rsaJWK(idp.kid, &idp.key.PublicKey)}})
	return testResponse(http.StatusOK, body), nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testResponse(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
}

func (idp *identityServer) profile(now *time.Time) Config {
	return Config{HTTPClient: &http.Client{Timeout: time.Second, Transport: roundTripFunc(idp.roundTrip)}, Clock: func() time.Time { return *now }, Issuers: []IssuerConfig{{
		Issuer: "https://issuer.example", Audiences: []string{"thinkpixeltg"}, Resources: []string{"urn:thinkpixel:tg"}, Algorithms: []string{"RS256"},
		RefreshAfter: time.Minute, MaxStale: time.Minute, ClockSkew: 5 * time.Second, MaxKeys: 2,
	}}}
}

func TestOIDCProviderVerifiesConfiguredProfileAndCachesKeys(t *testing.T) {
	idp := newIdentityServer(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	provider, err := NewOIDCProvider(idp.profile(&now))
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{"iss": "https://issuer.example", "sub": "subject-1", "aud": []string{"other", "thinkpixeltg"}, "resource": "urn:thinkpixel:tg", "exp": now.Add(time.Minute).Unix(), "nbf": now.Add(3 * time.Second).Unix()}
	token := signRSA(t, idp.key, idp.kid, "RS256", claims)
	verified, err := provider.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Subject != "subject-1" || verified.Issuer != "https://issuer.example" {
		t.Fatalf("unexpected claims: %+v", verified)
	}
	if _, err := provider.Verify(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	idp.mu.Lock()
	requests := idp.requests
	idp.mu.Unlock()
	if requests != 2 {
		t.Fatalf("discovery/JWKS requests = %d, want 2", requests)
	}
}

func TestOIDCProviderRejectsProfileAndTimeViolations(t *testing.T) {
	idp := newIdentityServer(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	provider, err := NewOIDCProvider(idp.profile(&now))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		claims    map[string]any
		algorithm string
		code      ErrorCode
	}{
		{"issuer", map[string]any{"iss": "https://attacker.example", "aud": "thinkpixeltg", "exp": now.Add(time.Minute).Unix()}, "RS256", CodeUnsupportedIssuer},
		{"audience", map[string]any{"iss": "https://issuer.example", "aud": "somewhere-else", "resource": "urn:thinkpixel:tg", "exp": now.Add(time.Minute).Unix()}, "RS256", CodeInvalidToken},
		{"resource", map[string]any{"iss": "https://issuer.example", "aud": "thinkpixeltg", "resource": "urn:other", "exp": now.Add(time.Minute).Unix()}, "RS256", CodeInvalidToken},
		{"expiry", map[string]any{"iss": "https://issuer.example", "aud": "thinkpixeltg", "resource": "urn:thinkpixel:tg", "exp": now.Add(-6 * time.Second).Unix()}, "RS256", CodeInvalidToken},
		{"not-before", map[string]any{"iss": "https://issuer.example", "aud": "thinkpixeltg", "resource": "urn:thinkpixel:tg", "exp": now.Add(time.Minute).Unix(), "nbf": now.Add(6 * time.Second).Unix()}, "RS256", CodeInvalidToken},
		{"algorithm", map[string]any{"iss": "https://issuer.example", "aud": "thinkpixeltg", "resource": "urn:thinkpixel:tg", "exp": now.Add(time.Minute).Unix()}, "PS256", CodeInvalidToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := provider.Verify(context.Background(), signRSA(t, idp.key, idp.kid, test.algorithm, test.claims))
			assertCode(t, err, test.code)
		})
	}
}

func TestOIDCProviderRefreshesRotationAndHasBoundedOutageBehavior(t *testing.T) {
	idp := newIdentityServer(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	provider, err := NewOIDCProvider(idp.profile(&now))
	if err != nil {
		t.Fatal(err)
	}
	claims := func() map[string]any {
		return map[string]any{"iss": "https://issuer.example", "aud": "thinkpixeltg", "resource": "urn:thinkpixel:tg", "exp": now.Add(10 * time.Minute).Unix()}
	}
	if _, err := provider.Verify(context.Background(), signRSA(t, idp.key, idp.kid, "RS256", claims())); err != nil {
		t.Fatal(err)
	}
	rotated, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp.mu.Lock()
	idp.key, idp.kid = rotated, "key-2"
	idp.mu.Unlock()
	if _, err := provider.Verify(context.Background(), signRSA(t, rotated, "key-2", "RS256", claims())); err != nil {
		t.Fatalf("rotation: %v", err)
	}
	now = now.Add(61 * time.Second)
	idp.mu.Lock()
	idp.outage = true
	idp.mu.Unlock()
	if _, err := provider.Verify(context.Background(), signRSA(t, rotated, "key-2", "RS256", claims())); err != nil {
		t.Fatalf("bounded stale key: %v", err)
	}
	unknown, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Verify(context.Background(), signRSA(t, unknown, "unknown", "RS256", claims()))
	assertCode(t, err, CodeDependencyUnavailable)
	now = now.Add(61 * time.Second)
	_, err = provider.Verify(context.Background(), signRSA(t, rotated, "key-2", "RS256", claims()))
	assertCode(t, err, CodeDependencyUnavailable)
}

func TestOIDCProviderRejectsDiscoveryMismatch(t *testing.T) {
	idp := newIdentityServer(t)
	now := time.Now().UTC()
	idp.discoveryIssuer = "https://attacker.example"
	provider, err := NewOIDCProvider(idp.profile(&now))
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{"iss": "https://issuer.example", "aud": "thinkpixeltg", "resource": "urn:thinkpixel:tg", "exp": now.Add(time.Minute).Unix()}
	_, err = provider.Verify(context.Background(), signRSA(t, idp.key, idp.kid, "RS256", claims))
	assertCode(t, err, CodeDependencyUnavailable)
}

func TestOIDCProviderRequiresSecureExplicitBoundedConfiguration(t *testing.T) {
	client := &http.Client{Timeout: time.Second}
	base := IssuerConfig{Issuer: "http://issuer.example", Audiences: []string{"tg"}, Algorithms: []string{"RS256"}, RefreshAfter: time.Minute, MaxKeys: 1}
	if _, err := NewOIDCProvider(Config{HTTPClient: client, Issuers: []IssuerConfig{base}}); err == nil {
		t.Fatal("expected insecure issuer rejection")
	}
	base.Issuer = "https://issuer.example"
	base.MaxKeys = 0
	if _, err := NewOIDCProvider(Config{HTTPClient: client, Issuers: []IssuerConfig{base}}); err == nil {
		t.Fatal("expected key bound rejection")
	}
}

func rsaJWK(kid string, key *rsa.PublicKey) map[string]string {
	exponent := []byte{byte(key.E >> 16), byte(key.E >> 8), byte(key.E)}
	return map[string]string{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(exponent)}
}

func signRSA(t *testing.T, key *rsa.PrivateKey, kid, algorithm string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": algorithm, "kid": kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func assertCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var verification *VerificationError
	if !errors.As(err, &verification) || verification.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func ExampleProvider_Verify() {
	fmt.Println(CodeInvalidToken) /* transport calls Verify with its request context */ // Output: invalid_token
}

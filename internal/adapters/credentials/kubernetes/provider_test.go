package kubernetes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/config"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestProviderResolvesAllowlistedProjectedTokenInProduction(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	path := "/var/run/secrets/thinkpixeltg/github-token"
	loaded := projectedToken(t, map[string]any{"iss": "https://kubernetes.default.svc", "aud": []string{"github-api", "secondary"}, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(30 * time.Minute).Unix()})
	provider := newTestProvider(t, now, path, func(got string) ([]byte, error) {
		if got != path {
			t.Fatalf("path = %q", got)
		}
		return loaded, nil
	})
	capability, err := provider.Resolve(t.Context(), projectedBinding(t, "projected:"+path, domain.CapabilityAPIToken, domain.DelegationNone, true), providerContext("governed-subject"))
	if err != nil {
		t.Fatal(err)
	}
	defer capability.Release()
	if !bytes.Equal(loaded, make([]byte, len(loaded))) {
		t.Fatal("provider retained projected token buffer")
	}
	metadata := capability.Metadata()
	if metadata.Kind != domain.CapabilityAPIToken || metadata.Issuer != "https://kubernetes.default.svc" || metadata.ExpiresAt != now.Add(10*time.Minute) || metadata.Audiences[0] != "github-api" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if err := capability.UseSecret(func(secret []byte) error {
		if bytes.Count(secret, []byte{'.'}) != 2 {
			t.Fatal("projected token was not preserved")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderFailsClosedForUntrustedClaimsAndBindings(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	allowedPath := "/var/run/secrets/thinkpixeltg/github-token"
	validClaims := func() map[string]any {
		return map[string]any{"iss": "https://kubernetes.default.svc", "aud": "github-api", "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(30 * time.Minute).Unix()}
	}
	tests := []struct {
		name       string
		path       string
		claims     map[string]any
		raw        []byte
		kind       domain.CredentialCapabilityKind
		delegation domain.SubjectDelegationMode
		enabled    bool
		subject    string
	}{
		{name: "issuer", path: allowedPath, claims: mutateClaims(validClaims(), "iss", "https://attacker.invalid"), kind: domain.CapabilityAPIToken, enabled: true, subject: "subject"},
		{name: "audience", path: allowedPath, claims: mutateClaims(validClaims(), "aud", "other-api"), kind: domain.CapabilityAPIToken, enabled: true, subject: "subject"},
		{name: "expired", path: allowedPath, claims: mutateClaims(validClaims(), "exp", now.Unix()), kind: domain.CapabilityAPIToken, enabled: true, subject: "subject"},
		{name: "future issued", path: allowedPath, claims: mutateClaims(validClaims(), "iat", now.Add(2*time.Minute).Unix()), kind: domain.CapabilityAPIToken, enabled: true, subject: "subject"},
		{name: "long lifetime", path: allowedPath, claims: mutateClaims(validClaims(), "exp", now.Add(2*time.Hour).Unix()), kind: domain.CapabilityAPIToken, enabled: true, subject: "subject"},
		{name: "malformed", path: allowedPath, raw: []byte("not-a-token"), kind: domain.CapabilityAPIToken, enabled: true, subject: "subject"},
		{name: "path", path: "/var/run/secrets/other/token", claims: validClaims(), kind: domain.CapabilityAPIToken, enabled: true, subject: "subject"},
		{name: "capability", path: allowedPath, claims: validClaims(), kind: domain.CapabilityOAuthAccessToken, enabled: true, subject: "subject"},
		{name: "delegation", path: allowedPath, claims: validClaims(), kind: domain.CapabilityAPIToken, delegation: domain.DelegationTokenExchange, enabled: true, subject: "subject"},
		{name: "disabled", path: allowedPath, claims: validClaims(), kind: domain.CapabilityAPIToken, enabled: false, subject: "subject"},
		{name: "subject", path: allowedPath, claims: validClaims(), kind: domain.CapabilityAPIToken, enabled: true, subject: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := test.raw
			if token == nil {
				token = projectedToken(t, test.claims)
			}
			provider := newTestProvider(t, now, allowedPath, func(string) ([]byte, error) { return token, nil })
			binding := projectedBinding(t, "projected:"+test.path, test.kind, test.delegation, test.enabled)
			if _, err := provider.Resolve(t.Context(), binding, providerContext(test.subject)); err == nil {
				t.Fatal("unsafe projected credential accepted")
			}
		})
	}
}

func TestProviderConfigurationAndIOErrorsAreSafe(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	path := "/var/run/secrets/thinkpixeltg/github-token"
	for name, cfg := range map[string]Config{
		"mode":       {Mode: "test", Clock: fixedClock{now}, AllowedTokenPaths: []string{path}, TrustedIssuers: []string{"issuer"}},
		"clock":      {Mode: config.ModeProduction, AllowedTokenPaths: []string{path}, TrustedIssuers: []string{"issuer"}},
		"paths":      {Mode: config.ModeProduction, Clock: fixedClock{now}, TrustedIssuers: []string{"issuer"}},
		"relative":   {Mode: config.ModeProduction, Clock: fixedClock{now}, AllowedTokenPaths: []string{"relative"}, TrustedIssuers: []string{"issuer"}},
		"issuers":    {Mode: config.ModeProduction, Clock: fixedClock{now}, AllowedTokenPaths: []string{path}},
		"lifetime":   {Mode: config.ModeProduction, Clock: fixedClock{now}, AllowedTokenPaths: []string{path}, TrustedIssuers: []string{"issuer"}, MaximumTokenLifetime: 25 * time.Hour},
		"clock skew": {Mode: config.ModeProduction, Clock: fixedClock{now}, AllowedTokenPaths: []string{path}, TrustedIssuers: []string{"issuer"}, ClockSkew: 6 * time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("invalid provider configuration accepted")
			}
		})
	}

	canary := "SYNTHETIC_KUBERNETES_IO_CANARY"
	provider := newTestProvider(t, now, path, func(string) ([]byte, error) { return nil, errors.New(canary) })
	_, err := provider.Resolve(t.Context(), projectedBinding(t, "projected:"+path, domain.CapabilityAPIToken, domain.DelegationNone, true), providerContext("subject"))
	if err == nil || strings.Contains(err.Error(), canary) || strings.Contains(err.Error(), path) {
		t.Fatalf("unsafe provider error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.Resolve(ctx, projectedBinding(t, "projected:"+path, domain.CapabilityAPIToken, domain.DelegationNone, true), providerContext("subject")); err == nil {
		t.Fatal("cancelled resolution accepted")
	}
}

func newTestProvider(t *testing.T, now time.Time, path string, read func(string) ([]byte, error)) *Provider {
	t.Helper()
	provider, err := New(Config{Mode: config.ModeProduction, Clock: fixedClock{now}, AllowedTokenPaths: []string{path}, TrustedIssuers: []string{"https://kubernetes.default.svc"}, ReadFile: read, MaximumTokenLifetime: time.Hour, ClockSkew: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func projectedBinding(t *testing.T, reference string, kind domain.CredentialCapabilityKind, delegation domain.SubjectDelegationMode, enabled bool) domain.CredentialBinding {
	t.Helper()
	if delegation == "" {
		delegation = domain.DelegationNone
	}
	parseID := func(value string) domain.UUID {
		id, err := domain.ParseUUID(value)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	toolID, _ := domain.ParseToolID("github.pull.comment")
	version, _ := domain.ParseSemanticVersion("1.0.0")
	policy := domain.SubjectDelegationPolicy{Mode: delegation}
	if delegation == domain.DelegationTokenExchange {
		policy.TrustedIssuer = "https://identity.example"
	}
	binding, err := domain.NewCredentialBinding(domain.CredentialBindingDefinition{
		ID: parseID("019b0000-0000-7000-8000-000000000001"), TenantID: parseID("019b0000-0000-7000-8000-000000000002"), ConnectorInstanceID: parseID("019b0000-0000-7000-8000-000000000003"),
		ToolSelectors: []domain.CredentialToolSelector{{ToolID: toolID, Version: version, CredentialSelector: "github_writer"}},
		Provider:      domain.CredentialProviderBinding{ProviderType: "kubernetes_projected", ProviderRef: reference, Capability: kind, Audiences: []string{"github-api"}, Scopes: []string{"pull_request:write"}},
		Delegation:    policy, Cache: domain.CredentialCachePolicy{MaximumTTL: 10 * time.Minute, ExpirySkew: time.Minute}, RevocationEpoch: "deployment-42", Enabled: enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func projectedToken(t *testing.T, claims map[string]any) []byte {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".synthetic-signature")
}

func mutateClaims(claims map[string]any, key string, value any) map[string]any {
	claims[key] = value
	return claims
}

func providerContext(subject string) ports.CredentialProviderContext {
	return ports.CredentialProviderContext{Subject: subject, RunID: "run-1"}
}

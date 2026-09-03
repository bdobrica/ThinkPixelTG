package domain

import (
	"testing"
	"time"
)

func TestCredentialBindingTiesTrustedContextAndIsImmutable(t *testing.T) {
	definition := validCredentialBindingDefinition(t)
	binding, err := NewCredentialBinding(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.ToolSelectors[0].CredentialSelector = "mutated"
	definition.Provider.Scopes[0] = "admin"
	definition.PolicyTags[0] = "caller-controlled"
	got := binding.Definition()
	if got.ToolSelectors[0].CredentialSelector != "github_user_delegated" || got.Provider.Scopes[0] != "pull_request:write" || got.PolicyTags[0] != "interactive-write" {
		t.Fatal("constructor retained mutable credential policy")
	}
	got.Provider.Audiences[0] = "attacker"
	if binding.Definition().Provider.Audiences[0] != "api.github.com" {
		t.Fatal("definition exposed mutable credential policy")
	}
}

func TestCredentialBindingRejectsIncompleteOrUnsafePolicy(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CredentialBindingDefinition)
	}{
		{"missing tenant", func(d *CredentialBindingDefinition) { d.TenantID = UUID{} }},
		{"missing connector", func(d *CredentialBindingDefinition) { d.ConnectorInstanceID = UUID{} }},
		{"missing tools", func(d *CredentialBindingDefinition) { d.ToolSelectors = nil }},
		{"unversioned tool", func(d *CredentialBindingDefinition) { d.ToolSelectors[0].Version = SemanticVersion{} }},
		{"unsafe selector", func(d *CredentialBindingDefinition) { d.ToolSelectors[0].CredentialSelector = "caller/secret" }},
		{"duplicate selector", func(d *CredentialBindingDefinition) { d.ToolSelectors = append(d.ToolSelectors, d.ToolSelectors[0]) }},
		{"provider type", func(d *CredentialBindingDefinition) { d.Provider.ProviderType = "https://provider.invalid" }},
		{"provider ref control", func(d *CredentialBindingDefinition) { d.Provider.ProviderRef = "vault:binding\ntoken=secret" }},
		{"capability", func(d *CredentialBindingDefinition) { d.Provider.Capability = "raw_header" }},
		{"oauth target", func(d *CredentialBindingDefinition) { d.Provider.Audiences = nil; d.Provider.Resources = nil }},
		{"duplicate scopes", func(d *CredentialBindingDefinition) { d.Provider.Scopes = []string{"repo", "repo"} }},
		{"delegation mode", func(d *CredentialBindingDefinition) { d.Delegation.Mode = "caller_selected" }},
		{"delegation issuer", func(d *CredentialBindingDefinition) { d.Delegation.TrustedIssuer = "" }},
		{"issuer without delegation", func(d *CredentialBindingDefinition) { d.Delegation.Mode = DelegationNone }},
		{"cache ttl", func(d *CredentialBindingDefinition) { d.Cache.MaximumTTL = 25 * time.Hour }},
		{"cache skew", func(d *CredentialBindingDefinition) { d.Cache.ExpirySkew = d.Cache.MaximumTTL + time.Second }},
		{"duplicate policy tag", func(d *CredentialBindingDefinition) { d.PolicyTags = []string{"write", "write"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validCredentialBindingDefinition(t)
			test.mutate(&definition)
			if _, err := NewCredentialBinding(definition); err == nil {
				t.Fatal("invalid credential binding accepted")
			}
		})
	}
}

func TestCredentialBindingAllowsNonDelegatedNonBearerCapability(t *testing.T) {
	definition := validCredentialBindingDefinition(t)
	definition.Provider.Capability = CapabilityMTLS
	definition.Provider.Audiences = nil
	definition.Delegation = SubjectDelegationPolicy{Mode: DelegationNone}
	definition.Cache = CredentialCachePolicy{}
	if _, err := NewCredentialBinding(definition); err != nil {
		t.Fatal(err)
	}
}

func validCredentialBindingDefinition(t *testing.T) CredentialBindingDefinition {
	t.Helper()
	parseID := func(value string) UUID {
		id, err := ParseUUID(value)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	toolID, err := ParseToolID("github.pull.comment")
	if err != nil {
		t.Fatal(err)
	}
	version, err := ParseSemanticVersion("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	return CredentialBindingDefinition{
		ID:                  parseID("019b0000-0000-7000-8000-000000000001"),
		TenantID:            parseID("019b0000-0000-7000-8000-000000000002"),
		ConnectorInstanceID: parseID("019b0000-0000-7000-8000-000000000003"),
		ToolSelectors:       []CredentialToolSelector{{ToolID: toolID, Version: version, CredentialSelector: "github_user_delegated"}},
		Provider: CredentialProviderBinding{ProviderType: "oauth_exchange", ProviderRef: "provider:github-primary", Capability: CapabilityOAuthAccessToken,
			Audiences: []string{"api.github.com"}, Resources: []string{"repository:owner/name"}, Scopes: []string{"pull_request:write"}},
		Delegation:      SubjectDelegationPolicy{Mode: DelegationTokenExchange, TrustedIssuer: "https://identity.example"},
		Cache:           CredentialCachePolicy{MaximumTTL: 10 * time.Minute, ExpirySkew: time.Minute},
		RevocationEpoch: "github-primary:42",
		PolicyTags:      []string{"interactive-write"},
		Enabled:         true,
	}
}

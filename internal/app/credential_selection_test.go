package app

import (
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

func TestSelectCredentialBindingUsesOnlyTrustedContext(t *testing.T) {
	selection, binding := credentialSelectionFixture(t)
	otherTenant := credentialBindingForSelection(t, selection, "019b0000-0000-7000-8000-000000000011", "019b0000-0000-7000-8000-000000000021", "019b0000-0000-7000-8000-000000000002", true)
	otherConnector := credentialBindingForSelection(t, selection, "019b0000-0000-7000-8000-000000000012", selection.TenantID.String(), "019b0000-0000-7000-8000-000000000022", true)
	disabled := credentialBindingForSelection(t, selection, "019b0000-0000-7000-8000-000000000013", selection.TenantID.String(), "019b0000-0000-7000-8000-000000000002", false)

	selected, err := SelectCredentialBinding(selection, []domain.CredentialBinding{otherTenant, otherConnector, disabled, binding})
	if err != nil {
		t.Fatal(err)
	}
	got := selected.Definition()
	if got.ID != binding.Definition().ID || got.Provider.ProviderRef != "provider:trusted" || got.Provider.Scopes[0] != "pull_request:write" {
		t.Fatalf("selected binding = %#v", got)
	}
}

func TestSelectCredentialBindingRequiresExactImmutableToolSelector(t *testing.T) {
	selection, binding := credentialSelectionFixture(t)
	for name, mutate := range map[string]func(*domain.ToolVersionDefinition){
		"tool":       func(tool *domain.ToolVersionDefinition) { tool.ToolID, _ = domain.ParseToolID("github.issue.comment") },
		"version":    func(tool *domain.ToolVersionDefinition) { tool.Version, _ = domain.ParseSemanticVersion("1.0.1") },
		"credential": func(tool *domain.ToolVersionDefinition) { tool.CredentialSelector = "github_machine" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := selection
			mutate(&changed.Tool)
			if _, err := SelectCredentialBinding(changed, []domain.CredentialBinding{binding}); err == nil {
				t.Fatal("mismatched trusted selector accepted")
			}
		})
	}
}

func TestSelectCredentialBindingFailsClosed(t *testing.T) {
	selection, binding := credentialSelectionFixture(t)
	for name, mutate := range map[string]func(*CredentialSelectionContext, *[]domain.CredentialBinding){
		"missing tenant": func(context *CredentialSelectionContext, _ *[]domain.CredentialBinding) {
			context.TenantID = domain.UUID{}
		},
		"missing connector": func(context *CredentialSelectionContext, _ *[]domain.CredentialBinding) {
			context.ConnectorInstanceID = domain.UUID{}
		},
		"no match": func(_ *CredentialSelectionContext, bindings *[]domain.CredentialBinding) { *bindings = nil },
		"ambiguous": func(_ *CredentialSelectionContext, bindings *[]domain.CredentialBinding) {
			*bindings = append(*bindings, credentialBindingForSelection(t, selection, "019b0000-0000-7000-8000-000000000099", selection.TenantID.String(), "019b0000-0000-7000-8000-000000000002", true))
		},
	} {
		t.Run(name, func(t *testing.T) {
			context := selection
			bindings := []domain.CredentialBinding{binding}
			mutate(&context, &bindings)
			if _, err := SelectCredentialBinding(context, bindings); err == nil {
				t.Fatal("unsafe credential selection accepted")
			}
		})
	}
}

func credentialSelectionFixture(t *testing.T) (CredentialSelectionContext, domain.CredentialBinding) {
	t.Helper()
	tenant := mustCredentialUUID(t, "019b0000-0000-7000-8000-000000000001")
	connector := mustCredentialUUID(t, "019b0000-0000-7000-8000-000000000002")
	tool := invocationTestTool(t)
	selection := CredentialSelectionContext{TenantID: tenant, ConnectorInstanceID: connector, Tool: tool}
	return selection, credentialBindingForSelection(t, selection, "019b0000-0000-7000-8000-000000000003", tenant.String(), connector.String(), true)
}

func credentialBindingForSelection(t *testing.T, selection CredentialSelectionContext, id, tenantID, connectorID string, enabled bool) domain.CredentialBinding {
	t.Helper()
	binding, err := domain.NewCredentialBinding(domain.CredentialBindingDefinition{
		ID:                  mustCredentialUUID(t, id),
		TenantID:            mustCredentialUUID(t, tenantID),
		ConnectorInstanceID: mustCredentialUUID(t, connectorID),
		ToolSelectors: []domain.CredentialToolSelector{{ToolID: selection.Tool.ToolID, Version: selection.Tool.Version,
			CredentialSelector: selection.Tool.CredentialSelector}},
		Provider: domain.CredentialProviderBinding{ProviderType: "oauth_exchange", ProviderRef: "provider:trusted",
			Capability: domain.CapabilityOAuthAccessToken, Audiences: []string{"api.github.com"}, Scopes: []string{"pull_request:write"}},
		Delegation: domain.SubjectDelegationPolicy{Mode: domain.DelegationNone},
		Enabled:    enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func mustCredentialUUID(t *testing.T, value string) domain.UUID {
	t.Helper()
	id, err := domain.ParseUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

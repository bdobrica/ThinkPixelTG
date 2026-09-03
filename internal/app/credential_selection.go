package app

import (
	"errors"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

// CredentialSelectionContext contains only identities resolved from trusted TG
// configuration. Public invocation documents must never populate this type.
type CredentialSelectionContext struct {
	TenantID            domain.UUID
	ConnectorInstanceID domain.UUID
	Tool                domain.ToolVersionDefinition
}

// SelectCredentialBinding resolves exactly one enabled administrator-authored
// binding. Provider, scope, audience, resource, secret, and destination values
// are outputs of this selection and therefore cannot be caller overrides.
func SelectCredentialBinding(context CredentialSelectionContext, candidates []domain.CredentialBinding) (domain.CredentialBinding, error) {
	if context.TenantID == (domain.UUID{}) || context.ConnectorInstanceID == (domain.UUID{}) {
		return domain.CredentialBinding{}, errors.New("trusted credential selection context is incomplete")
	}
	if err := domain.ValidateToolVersionDefinition(context.Tool); err != nil {
		return domain.CredentialBinding{}, errors.New("trusted credential selection tool is invalid")
	}

	var selected domain.CredentialBinding
	matches := 0
	for _, candidate := range candidates {
		definition := candidate.Definition()
		if !definition.Enabled || definition.TenantID != context.TenantID || definition.ConnectorInstanceID != context.ConnectorInstanceID {
			continue
		}
		if !bindingSelectsTool(definition.ToolSelectors, context.Tool) {
			continue
		}
		selected = candidate
		matches++
	}
	if matches == 0 {
		return domain.CredentialBinding{}, errors.New("no enabled credential binding matches trusted context")
	}
	if matches != 1 {
		return domain.CredentialBinding{}, errors.New("credential binding selection is ambiguous")
	}
	return selected, nil
}

func bindingSelectsTool(selectors []domain.CredentialToolSelector, tool domain.ToolVersionDefinition) bool {
	for _, selector := range selectors {
		if selector.ToolID == tool.ToolID && selector.Version.String() == tool.Version.String() && selector.CredentialSelector == tool.CredentialSelector {
			return true
		}
	}
	return false
}

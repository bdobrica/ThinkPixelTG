package domain

import (
	"errors"
	"strings"
	"time"
)

// CredentialCapabilityKind identifies the reviewed form of authority a provider
// may return. Secret material is deliberately not part of the binding model.
type CredentialCapabilityKind string

const (
	CapabilityOAuthAccessToken CredentialCapabilityKind = "oauth_access_token"
	CapabilityAPIToken         CredentialCapabilityKind = "api_token"
	CapabilityMTLS             CredentialCapabilityKind = "mtls"
	CapabilitySignedRequest    CredentialCapabilityKind = "signed_request"
)

type SubjectDelegationMode string

const (
	DelegationNone          SubjectDelegationMode = "none"
	DelegationTokenExchange SubjectDelegationMode = "token_exchange"
)

// CredentialToolSelector binds a credential to an exact immutable tool version
// and its administrator-authored credential selector.
type CredentialToolSelector struct {
	ToolID             ToolID
	Version            SemanticVersion
	CredentialSelector string
}

type CredentialProviderBinding struct {
	ProviderType string
	ProviderRef  string
	Capability   CredentialCapabilityKind
	Audiences    []string
	Resources    []string
	Scopes       []string
}

type SubjectDelegationPolicy struct {
	Mode          SubjectDelegationMode
	TrustedIssuer string
}

type CredentialCachePolicy struct {
	MaximumTTL time.Duration
	ExpirySkew time.Duration
}

// CredentialBindingDefinition contains only administrator-controlled references
// and policy metadata. It cannot contain credential plaintext.
type CredentialBindingDefinition struct {
	ID                  UUID
	TenantID            UUID
	ConnectorInstanceID UUID
	ToolSelectors       []CredentialToolSelector
	Provider            CredentialProviderBinding
	Delegation          SubjectDelegationPolicy
	Cache               CredentialCachePolicy
	RevocationEpoch     string
	PolicyTags          []string
	Enabled             bool
}

// CredentialBinding is an immutable, tenant-scoped credential selection rule.
type CredentialBinding struct{ definition CredentialBindingDefinition }

func NewCredentialBinding(definition CredentialBindingDefinition) (CredentialBinding, error) {
	if err := ValidateCredentialBindingDefinition(definition); err != nil {
		return CredentialBinding{}, err
	}
	return CredentialBinding{definition: cloneCredentialBindingDefinition(definition)}, nil
}

func (binding CredentialBinding) Definition() CredentialBindingDefinition {
	return cloneCredentialBindingDefinition(binding.definition)
}

func ValidateCredentialBindingDefinition(definition CredentialBindingDefinition) error {
	if definition.ID == (UUID{}) || definition.TenantID == (UUID{}) || definition.ConnectorInstanceID == (UUID{}) {
		return errors.New("credential binding, tenant, and connector instance IDs are required")
	}
	if len(definition.ToolSelectors) == 0 || len(definition.ToolSelectors) > 64 {
		return errors.New("credential binding requires between 1 and 64 tool selectors")
	}
	seenSelectors := make(map[string]struct{}, len(definition.ToolSelectors))
	for _, selector := range definition.ToolSelectors {
		if _, err := ParseToolID(string(selector.ToolID)); err != nil {
			return errors.New("credential binding contains an invalid tool selector")
		}
		if _, err := ParseSemanticVersion(selector.Version.String()); err != nil || !validRegistryKey(selector.CredentialSelector) {
			return errors.New("credential binding contains an invalid tool selector")
		}
		key := string(selector.ToolID) + "\x00" + selector.Version.String() + "\x00" + selector.CredentialSelector
		if _, exists := seenSelectors[key]; exists {
			return errors.New("credential binding contains a duplicate tool selector")
		}
		seenSelectors[key] = struct{}{}
	}
	if !validRegistryKey(definition.Provider.ProviderType) || !validSafeReference(definition.Provider.ProviderRef, 512) || !validCapabilityKind(definition.Provider.Capability) {
		return errors.New("credential provider binding is invalid")
	}
	if err := validateStringSet("audiences", definition.Provider.Audiences, 32, 512); err != nil {
		return err
	}
	if err := validateStringSet("resources", definition.Provider.Resources, 32, 512); err != nil {
		return err
	}
	if err := validateStringSet("scopes", definition.Provider.Scopes, 64, 256); err != nil {
		return err
	}
	if definition.Provider.Capability == CapabilityOAuthAccessToken && len(definition.Provider.Audiences) == 0 && len(definition.Provider.Resources) == 0 {
		return errors.New("OAuth capability requires an audience or resource")
	}
	if definition.Delegation.Mode != DelegationNone && definition.Delegation.Mode != DelegationTokenExchange {
		return errors.New("subject delegation mode is invalid")
	}
	if definition.Delegation.Mode == DelegationTokenExchange && !validSafeReference(definition.Delegation.TrustedIssuer, 512) {
		return errors.New("delegated credentials require a trusted subject issuer")
	}
	if definition.Delegation.Mode == DelegationNone && definition.Delegation.TrustedIssuer != "" {
		return errors.New("non-delegated credentials cannot declare a subject issuer")
	}
	if definition.Cache.MaximumTTL < 0 || definition.Cache.MaximumTTL > 24*time.Hour || definition.Cache.ExpirySkew < 0 || definition.Cache.ExpirySkew > definition.Cache.MaximumTTL {
		return errors.New("credential cache policy is invalid")
	}
	if !optionalSafeReference(definition.RevocationEpoch, 256) {
		return errors.New("credential revocation epoch is invalid")
	}
	if err := validateStringSet("policy tags", definition.PolicyTags, 32, 128); err != nil {
		return err
	}
	return nil
}

func validCapabilityKind(kind CredentialCapabilityKind) bool {
	return kind == CapabilityOAuthAccessToken || kind == CapabilityAPIToken || kind == CapabilityMTLS || kind == CapabilitySignedRequest
}

func validateStringSet(name string, values []string, maximumCount, maximumLength int) error {
	if len(values) > maximumCount {
		return errors.New(name + " exceed the configured bound")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validSafeReference(value, maximumLength) {
			return errors.New(name + " contain an invalid value")
		}
		if _, exists := seen[value]; exists {
			return errors.New(name + " contain a duplicate value")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validSafeReference(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func optionalSafeReference(value string, maximum int) bool {
	return value == "" || validSafeReference(value, maximum)
}

func cloneCredentialBindingDefinition(definition CredentialBindingDefinition) CredentialBindingDefinition {
	definition.ToolSelectors = append([]CredentialToolSelector(nil), definition.ToolSelectors...)
	definition.Provider.Audiences = append([]string(nil), definition.Provider.Audiences...)
	definition.Provider.Resources = append([]string(nil), definition.Provider.Resources...)
	definition.Provider.Scopes = append([]string(nil), definition.Provider.Scopes...)
	definition.PolicyTags = append([]string(nil), definition.PolicyTags...)
	return definition
}

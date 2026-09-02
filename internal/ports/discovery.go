package ports

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

// DiscoveryCatalog exposes only tenant-scoped, currently enabled catalog rows.
// Implementations bind the authoritative tenant at construction time.
type DiscoveryCatalog interface {
	ListExposedForDiscovery(context.Context) ([]CatalogToolVersion, error)
}

type CatalogToolVersion struct {
	ToolID, Version  string
	Definition       json.RawMessage
	DefinitionDigest []byte
}

type ToolVersionKey struct {
	ToolID, Version string
}

// DiscoveryAuthorizer is the application-owned boundary for visibility policy.
// A decision can only remove candidates supplied by TG; it cannot add tools.
type DiscoveryAuthorizer interface {
	AuthorizeToolDiscovery(context.Context, DiscoveryAuthorizationRequest) (DiscoveryAuthorizationDecision, error)
}

type DiscoveryAuthorizationRequest struct {
	TenantID, Subject, Actor, AgentID, AgentVersion, RunID, WorkloadID string
	Candidates                                                         []ToolVersionKey
}

type DiscoveryAuthorizationDecision struct {
	Allowed []ToolVersionKey
}

func (request DiscoveryAuthorizationRequest) Validate() error {
	required := []string{request.TenantID, request.Subject, request.AgentID, request.AgentVersion, request.RunID, request.WorkloadID}
	for _, value := range required {
		if !validAuthorizationText(value) {
			return errors.New("discovery authorization request has missing or invalid identity")
		}
	}
	if request.Actor != "" && !validAuthorizationText(request.Actor) {
		return errors.New("discovery authorization request has invalid actor")
	}
	return validateToolVersionKeys(request.Candidates)
}

func (decision DiscoveryAuthorizationDecision) ValidateFor(request DiscoveryAuthorizationRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := validateToolVersionKeys(decision.Allowed); err != nil {
		return errors.New("discovery authorization decision is invalid")
	}
	candidates := make(map[ToolVersionKey]struct{}, len(request.Candidates))
	for _, key := range request.Candidates {
		candidates[key] = struct{}{}
	}
	for _, key := range decision.Allowed {
		if _, ok := candidates[key]; !ok {
			return errors.New("discovery authorization decision expands candidate visibility")
		}
	}
	return nil
}

func validateToolVersionKeys(keys []ToolVersionKey) error {
	seen := make(map[ToolVersionKey]struct{}, len(keys))
	for _, key := range keys {
		if _, err := domain.ParseToolID(key.ToolID); err != nil {
			return errors.New("discovery tool key is invalid")
		}
		if _, err := domain.ParseSemanticVersion(key.Version); err != nil {
			return errors.New("discovery tool key is invalid")
		}
		if _, exists := seen[key]; exists {
			return errors.New("discovery tool keys contain a duplicate")
		}
		seen[key] = struct{}{}
	}
	return nil
}

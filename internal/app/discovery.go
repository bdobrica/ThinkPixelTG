package app

import (
	"context"
	"errors"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

// DiscoveryService intersects tenant exposure with the current governed
// context's authorization. It never returns the unfiltered catalog on an
// authorization failure or malformed, visibility-expanding decision.
type DiscoveryService struct {
	catalog    ports.DiscoveryCatalog
	authorizer ports.DiscoveryAuthorizer
}

func NewDiscoveryService(catalog ports.DiscoveryCatalog, authorizer ports.DiscoveryAuthorizer) (*DiscoveryService, error) {
	if catalog == nil || authorizer == nil {
		return nil, errors.New("discovery catalog and authorizer are required")
	}
	return &DiscoveryService{catalog: catalog, authorizer: authorizer}, nil
}

func (service *DiscoveryService) Discover(
	ctx context.Context,
	request ports.DiscoveryAuthorizationRequest,
) ([]ports.CatalogToolVersion, error) {
	if service == nil || service.catalog == nil || service.authorizer == nil {
		return nil, errors.New("discovery service is required")
	}
	// Candidates are always derived from the tenant-bound repository. A caller
	// cannot inject guessed tools into the authorization request.
	request.Candidates = nil
	if err := request.Validate(); err != nil {
		return nil, err
	}
	exposed, err := service.catalog.ListExposedForDiscovery(ctx)
	if err != nil {
		return nil, err
	}
	request.Candidates = make([]ports.ToolVersionKey, len(exposed))
	for index, tool := range exposed {
		key := ports.ToolVersionKey{ToolID: tool.ToolID, Version: tool.Version}
		request.Candidates[index] = key
	}
	if err := request.Validate(); err != nil {
		return nil, errors.New("tenant discovery catalog returned invalid candidates")
	}
	decision, err := service.authorizer.AuthorizeToolDiscovery(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := decision.ValidateFor(request); err != nil {
		return nil, err
	}
	allowed := make(map[ports.ToolVersionKey]struct{}, len(decision.Allowed))
	for _, key := range decision.Allowed {
		allowed[key] = struct{}{}
	}
	visible := make([]ports.CatalogToolVersion, 0, len(allowed))
	for _, tool := range exposed {
		key := ports.ToolVersionKey{ToolID: tool.ToolID, Version: tool.Version}
		if _, ok := allowed[key]; ok {
			visible = append(visible, tool)
		}
	}
	return visible, nil
}

// Describe returns one exact immutable version only when it is both exposed to
// the tenant and authorized for the complete governed discovery context.
func (service *DiscoveryService) Describe(
	ctx context.Context,
	request ports.DiscoveryAuthorizationRequest,
	toolID, version string,
) (ports.CatalogToolVersion, error) {
	if _, err := domain.ParseToolID(toolID); err != nil {
		return ports.CatalogToolVersion{}, domain.NewError(domain.CodeNotFound, "tool version is not available", nil)
	}
	if _, err := domain.ParseSemanticVersion(version); err != nil {
		return ports.CatalogToolVersion{}, domain.NewError(domain.CodeNotFound, "tool version is not available", nil)
	}
	visible, err := service.Discover(ctx, request)
	if err != nil {
		return ports.CatalogToolVersion{}, err
	}
	for _, candidate := range visible {
		if candidate.ToolID == toolID && candidate.Version == version {
			return candidate, nil
		}
	}
	return ports.CatalogToolVersion{}, domain.NewError(domain.CodeNotFound, "tool version is not available", nil)
}

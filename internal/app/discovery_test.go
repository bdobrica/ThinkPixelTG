package app

import (
	"context"
	"errors"
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestDiscoveryIntersectsTenantExposureWithAuthorization(t *testing.T) {
	t.Parallel()
	exposed := []ports.CatalogToolVersion{
		{ToolID: "github.pull.comment", Version: "1.0.0"},
		{ToolID: "slack.message.send", Version: "2.0.0"},
	}
	catalog := discoveryCatalogFunc(func(context.Context) ([]ports.CatalogToolVersion, error) { return exposed, nil })
	authorizer := discoveryAuthorizerFunc(func(_ context.Context, request ports.DiscoveryAuthorizationRequest) (ports.DiscoveryAuthorizationDecision, error) {
		if len(request.Candidates) != 2 {
			t.Fatalf("authorization candidates = %v", request.Candidates)
		}
		return ports.DiscoveryAuthorizationDecision{Allowed: request.Candidates[:1]}, nil
	})
	service, err := NewDiscoveryService(catalog, authorizer)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := service.Discover(t.Context(), discoveryRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || visible[0].ToolID != "github.pull.comment" {
		t.Fatalf("visible tools = %#v", visible)
	}
}

func TestDiscoveryFailsClosedOnAuthorizationFailureOrExpansion(t *testing.T) {
	t.Parallel()
	catalog := discoveryCatalogFunc(func(context.Context) ([]ports.CatalogToolVersion, error) {
		return []ports.CatalogToolVersion{{ToolID: "github.pull.comment", Version: "1.0.0"}}, nil
	})
	for name, authorizer := range map[string]ports.DiscoveryAuthorizer{
		"failure": discoveryAuthorizerFunc(func(context.Context, ports.DiscoveryAuthorizationRequest) (ports.DiscoveryAuthorizationDecision, error) {
			return ports.DiscoveryAuthorizationDecision{}, errors.New("policy unavailable")
		}),
		"expansion": discoveryAuthorizerFunc(func(context.Context, ports.DiscoveryAuthorizationRequest) (ports.DiscoveryAuthorizationDecision, error) {
			return ports.DiscoveryAuthorizationDecision{Allowed: []ports.ToolVersionKey{{ToolID: "slack.message.send", Version: "1.0.0"}}}, nil
		}),
	} {
		t.Run(name, func(t *testing.T) {
			service, _ := NewDiscoveryService(catalog, authorizer)
			visible, err := service.Discover(t.Context(), discoveryRequest())
			if err == nil || visible != nil {
				t.Fatalf("Discover() = %#v, %v; want nil error result and failure", visible, err)
			}
		})
	}
}

func TestDiscoveryRejectsInvalidCatalogBeforeAuthorization(t *testing.T) {
	t.Parallel()
	called := false
	service, _ := NewDiscoveryService(
		discoveryCatalogFunc(func(context.Context) ([]ports.CatalogToolVersion, error) {
			return []ports.CatalogToolVersion{{ToolID: "not-valid", Version: "latest"}}, nil
		}),
		discoveryAuthorizerFunc(func(context.Context, ports.DiscoveryAuthorizationRequest) (ports.DiscoveryAuthorizationDecision, error) {
			called = true
			return ports.DiscoveryAuthorizationDecision{}, nil
		}),
	)
	if _, err := service.Discover(t.Context(), discoveryRequest()); err == nil || called {
		t.Fatalf("invalid catalog error = %v, authorizer called = %t", err, called)
	}
}

func TestDescribeRequiresExactAuthorizedVersion(t *testing.T) {
	t.Parallel()
	catalog := discoveryCatalogFunc(func(context.Context) ([]ports.CatalogToolVersion, error) {
		return []ports.CatalogToolVersion{
			{ToolID: "github.pull.comment", Version: "1.0.0"},
			{ToolID: "github.pull.comment", Version: "2.0.0"},
		}, nil
	})
	authorizer := discoveryAuthorizerFunc(func(_ context.Context, request ports.DiscoveryAuthorizationRequest) (ports.DiscoveryAuthorizationDecision, error) {
		return ports.DiscoveryAuthorizationDecision{Allowed: request.Candidates[:1]}, nil
	})
	service, _ := NewDiscoveryService(catalog, authorizer)
	tool, err := service.Describe(t.Context(), discoveryRequest(), "github.pull.comment", "1.0.0")
	if err != nil || tool.Version != "1.0.0" {
		t.Fatalf("Describe() = %#v, %v", tool, err)
	}
	for _, key := range [][2]string{{"github.pull.comment", "2.0.0"}, {"private.tool.read", "1.0.0"}, {"not-valid", "latest"}} {
		_, err := service.Describe(t.Context(), discoveryRequest(), key[0], key[1])
		var domainErr *domain.Error
		if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeNotFound {
			t.Fatalf("Describe(%q, %q) error = %v", key[0], key[1], err)
		}
	}
}

func TestTenantIsolationUsesOnlyBoundCatalogCandidates(t *testing.T) {
	t.Parallel()
	tenantATool := ports.CatalogToolVersion{ToolID: "tenant.a.read", Version: "1.0.0"}
	tenantBTool := ports.CatalogToolVersion{ToolID: "tenant.b.read", Version: "1.0.0"}
	for _, test := range []struct {
		name     string
		identity ports.DiscoveryAuthorizationRequest
		exposed  ports.CatalogToolVersion
		hidden   ports.CatalogToolVersion
	}{
		{name: "tenant a", identity: discoveryRequest(), exposed: tenantATool, hidden: tenantBTool},
		{name: "tenant b", identity: func() ports.DiscoveryAuthorizationRequest {
			request := discoveryRequest()
			request.TenantID = "019b0000-0000-7000-8000-000000000002"
			return request
		}(), exposed: tenantBTool, hidden: tenantATool},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _ := NewDiscoveryService(
				discoveryCatalogFunc(func(context.Context) ([]ports.CatalogToolVersion, error) {
					return []ports.CatalogToolVersion{test.exposed}, nil
				}),
				discoveryAuthorizerFunc(func(_ context.Context, request ports.DiscoveryAuthorizationRequest) (ports.DiscoveryAuthorizationDecision, error) {
					if request.TenantID != test.identity.TenantID || len(request.Candidates) != 1 || request.Candidates[0].ToolID != test.exposed.ToolID {
						t.Fatalf("authorization request = %#v", request)
					}
					return ports.DiscoveryAuthorizationDecision{Allowed: request.Candidates}, nil
				}),
			)
			visible, err := service.Discover(t.Context(), test.identity)
			if err != nil || len(visible) != 1 || visible[0].ToolID != test.exposed.ToolID || visible[0].ToolID == test.hidden.ToolID {
				t.Fatalf("Discover() = %#v, %v", visible, err)
			}
		})
	}
}

func discoveryRequest() ports.DiscoveryAuthorizationRequest {
	return ports.DiscoveryAuthorizationRequest{
		TenantID: "019b0000-0000-7000-8000-000000000001", Subject: "subject-1",
		AgentID: "agent-1", AgentVersion: "1.0.0", RunID: "run-1", WorkloadID: "spiffe://example.test/tg",
	}
}

type discoveryCatalogFunc func(context.Context) ([]ports.CatalogToolVersion, error)

func (function discoveryCatalogFunc) ListExposedForDiscovery(ctx context.Context) ([]ports.CatalogToolVersion, error) {
	return function(ctx)
}

type discoveryAuthorizerFunc func(context.Context, ports.DiscoveryAuthorizationRequest) (ports.DiscoveryAuthorizationDecision, error)

func (function discoveryAuthorizerFunc) AuthorizeToolDiscovery(ctx context.Context, request ports.DiscoveryAuthorizationRequest) (ports.DiscoveryAuthorizationDecision, error) {
	return function(ctx, request)
}

package authn

import (
	"context"
	"testing"
)

func TestDeriveGovernedContextUsesOnlyAuthenticatedPrincipal(t *testing.T) {
	principal := Principal{
		TenantID: "tenant-1", Subject: "subject-1", Actor: "actor-1",
		AgentID: "agent-1", AgentVersion: "v1", RunID: "run-1",
	}
	governed, err := DeriveGovernedContext(withPrincipal(context.Background(), principal))
	if err != nil {
		t.Fatal(err)
	}
	if governed.TenantID != principal.TenantID || governed.Subject != principal.Subject ||
		governed.Actor != principal.Actor || governed.AgentID != principal.AgentID ||
		governed.AgentVersion != principal.AgentVersion || governed.RunID != principal.RunID {
		t.Fatalf("governed context = %#v", governed)
	}
}

func TestDeriveGovernedContextFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		principal *Principal
	}{
		{name: "no authenticated principal"},
		{name: "missing tenant", principal: &Principal{Subject: "subject", AgentID: "agent", AgentVersion: "v1", RunID: "run"}},
		{name: "missing subject", principal: &Principal{TenantID: "tenant", AgentID: "agent", AgentVersion: "v1", RunID: "run"}},
		{name: "missing agent", principal: &Principal{TenantID: "tenant", Subject: "subject", AgentVersion: "v1", RunID: "run"}},
		{name: "missing agent version", principal: &Principal{TenantID: "tenant", Subject: "subject", AgentID: "agent", RunID: "run"}},
		{name: "missing run", principal: &Principal{TenantID: "tenant", Subject: "subject", AgentID: "agent", AgentVersion: "v1"}},
		{name: "malformed value", principal: &Principal{TenantID: " tenant", Subject: "subject", AgentID: "agent", AgentVersion: "v1", RunID: "run"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.principal != nil {
				ctx = withPrincipal(ctx, *test.principal)
			}
			if _, err := DeriveGovernedContext(ctx); err == nil {
				t.Fatal("DeriveGovernedContext() error = nil")
			}
		})
	}
}

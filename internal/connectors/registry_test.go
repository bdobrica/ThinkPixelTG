package connectors

import (
	"context"
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type executorStub struct{ name string }

func (*executorStub) Execute(context.Context, ports.ConnectorRequest) (ports.ConnectorResult, error) {
	return ports.ConnectorResult{}, nil
}

func TestRegistryResolvesTenantInstanceAndCompiledOperation(t *testing.T) {
	instance := registryInstance(t, "0001", "0010", "primary", "github", true)
	executor := &executorStub{name: "comment"}
	registry, err := NewRegistry(Config{Instances: []domain.ConnectorInstance{instance}, Registrations: []Registration{{ConnectorType: "github", Operations: map[string]ports.ConnectorExecutor{"pull.comment": executor}}}})
	if err != nil {
		t.Fatal(err)
	}
	tool := registryTool(t, "github", "pull.comment", "primary")
	resolved, err := registry.Resolve(instance.Definition().TenantID, tool)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Executor != executor || resolved.Instance.Definition().ID != instance.Definition().ID {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestRegistryFailsClosedAcrossTenantTypeSelectorAndOperation(t *testing.T) {
	instance := registryInstance(t, "0001", "0010", "primary", "github", true)
	registry, err := NewRegistry(Config{Instances: []domain.ConnectorInstance{instance}, Registrations: []Registration{{ConnectorType: "github", Operations: map[string]ports.ConnectorExecutor{"pull.comment": &executorStub{}}}}})
	if err != nil {
		t.Fatal(err)
	}
	otherTenant := registryInstance(t, "0002", "0020", "other", "github", true).Definition().TenantID
	for name, test := range map[string]struct {
		tenant domain.UUID
		tool   domain.ToolVersionDefinition
	}{
		"tenant":    {otherTenant, registryTool(t, "github", "pull.comment", "primary")},
		"selector":  {instance.Definition().TenantID, registryTool(t, "github", "pull.comment", "other")},
		"type":      {instance.Definition().TenantID, registryTool(t, "slack", "pull.comment", "primary")},
		"operation": {instance.Definition().TenantID, registryTool(t, "github", "pull.delete", "primary")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := registry.Resolve(test.tenant, test.tool); err == nil {
				t.Fatal("untrusted connector resolution accepted")
			}
		})
	}
}

func TestRegistryRejectsAmbiguousOrUncompiledConfiguration(t *testing.T) {
	first := registryInstance(t, "0001", "0010", "primary", "github", true)
	duplicateSelector := registryInstance(t, "0002", "0010", "primary", "github", true)
	uncompiled := registryInstance(t, "0003", "0010", "slack", "slack", true)
	registration := Registration{ConnectorType: "github", Operations: map[string]ports.ConnectorExecutor{"pull.comment": &executorStub{}}}
	for name, config := range map[string]Config{
		"empty":              {},
		"duplicate selector": {Instances: []domain.ConnectorInstance{first, duplicateSelector}, Registrations: []Registration{registration}},
		"uncompiled type":    {Instances: []domain.ConnectorInstance{uncompiled}, Registrations: []Registration{registration}},
		"duplicate type":     {Instances: []domain.ConnectorInstance{first}, Registrations: []Registration{registration, registration}},
		"nil operation":      {Instances: []domain.ConnectorInstance{first}, Registrations: []Registration{{ConnectorType: "github", Operations: map[string]ports.ConnectorExecutor{"pull.comment": nil}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewRegistry(config); err == nil {
				t.Fatal("unsafe registry configuration accepted")
			}
		})
	}
}

func TestRegistryDefensivelyOwnsConfiguration(t *testing.T) {
	instance := registryInstance(t, "0001", "0010", "primary", "github", true)
	executor := &executorStub{}
	operations := map[string]ports.ConnectorExecutor{"pull.comment": executor}
	instances := []domain.ConnectorInstance{instance}
	registry, err := NewRegistry(Config{Instances: instances, Registrations: []Registration{{ConnectorType: "github", Operations: operations}}})
	if err != nil {
		t.Fatal(err)
	}
	delete(operations, "pull.comment")
	instances[0] = domain.ConnectorInstance{}
	if _, err := registry.Resolve(instance.Definition().TenantID, registryTool(t, "github", "pull.comment", "primary")); err != nil {
		t.Fatal(err)
	}
}

func registryInstance(t *testing.T, idSuffix, tenantSuffix, selector, connectorType string, enabled bool) domain.ConnectorInstance {
	t.Helper()
	parseID := func(value string) domain.UUID {
		id, err := domain.ParseUUID(value)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	destination := []byte(`{"base_url":"https://api.example"}`)
	instance, err := domain.NewConnectorInstance(domain.ConnectorInstanceDefinition{ID: parseID("019b0000-0000-7000-8000-00000000" + idSuffix), TenantID: parseID("019b0000-0000-7000-8000-00000000" + tenantSuffix), Selector: selector, ConnectorType: connectorType, DestinationConfig: destination, ConfigDigest: domain.DigestBytes(destination), Enabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func registryTool(t *testing.T, connectorType, operation, selector string) domain.ToolVersionDefinition {
	t.Helper()
	tool := invocationLikeTool(t)
	tool.Connector = domain.ConnectorBinding{ConnectorType: connectorType, Operation: operation, InstanceSelector: selector}
	return tool
}

func invocationLikeTool(t *testing.T) domain.ToolVersionDefinition {
	t.Helper()
	toolID, _ := domain.ParseToolID("github.pull.comment")
	version, _ := domain.ParseSemanticVersion("1.0.0")
	return domain.ToolVersionDefinition{ToolID: toolID, Version: version, Risk: domain.RiskRead, Retry: domain.RetrySafe, Approval: domain.ApprovalNever,
		Connector: domain.ConnectorBinding{ConnectorType: "github", Operation: "pull.comment", InstanceSelector: "primary"}, CredentialSelector: "github_reader",
		ResourceProjection: domain.ResourceProjectionDefinition{Fields: []domain.ResourceProjectionField{{Name: "repository", Pointer: "/repository", Required: true, Type: domain.ProjectionString}}},
		Limits:             domain.ToolLimits{RequestBytes: 4096, ResultBytes: 4096, Deadline: 1, Concurrency: 1, MaxAttempts: 1}}
}

// Package connectors resolves immutable administrator-owned connector instances
// to compiled connector operations.
package connectors

import (
	"errors"
	"regexp"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

var registryKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

type Registration struct {
	ConnectorType string
	Operations    map[string]ports.ConnectorExecutor
}

type Config struct {
	Instances     []domain.ConnectorInstance
	Registrations []Registration
}

type instanceKey struct {
	tenant   domain.UUID
	selector string
}

type operationKey struct{ connectorType, operation string }

type Registry struct {
	instances  map[instanceKey]domain.ConnectorInstance
	operations map[operationKey]ports.ConnectorExecutor
}

type Resolved struct {
	Instance domain.ConnectorInstance
	Executor ports.ConnectorExecutor
}

func NewRegistry(config Config) (*Registry, error) {
	if len(config.Instances) == 0 || len(config.Registrations) == 0 {
		return nil, errors.New("connector registry configuration is incomplete")
	}
	operations := make(map[operationKey]ports.ConnectorExecutor)
	registeredTypes := make(map[string]struct{})
	for _, registration := range config.Registrations {
		if !validRegistryKey(registration.ConnectorType) || len(registration.Operations) == 0 {
			return nil, errors.New("connector registration is invalid")
		}
		if _, duplicate := registeredTypes[registration.ConnectorType]; duplicate {
			return nil, errors.New("connector type is registered more than once")
		}
		registeredTypes[registration.ConnectorType] = struct{}{}
		for operation, executor := range registration.Operations {
			if !validRegistryKey(operation) || executor == nil {
				return nil, errors.New("connector operation registration is invalid")
			}
			operations[operationKey{registration.ConnectorType, operation}] = executor
		}
	}
	instances := make(map[instanceKey]domain.ConnectorInstance, len(config.Instances))
	identities := make(map[[2]string]struct{}, len(config.Instances))
	for _, instance := range config.Instances {
		definition := instance.Definition()
		if err := domain.ValidateConnectorInstanceDefinition(definition); err != nil {
			return nil, errors.New("connector registry contains an invalid instance")
		}
		identity := [2]string{definition.TenantID.String(), definition.ID.String()}
		if _, duplicate := identities[identity]; duplicate {
			return nil, errors.New("connector instance identity is duplicated")
		}
		identities[identity] = struct{}{}
		key := instanceKey{definition.TenantID, definition.Selector}
		if _, duplicate := instances[key]; duplicate {
			return nil, errors.New("connector instance selector is ambiguous")
		}
		if _, registered := registeredTypes[definition.ConnectorType]; !registered {
			return nil, errors.New("connector instance type is not compiled")
		}
		instances[key] = instance
	}
	return &Registry{instances: instances, operations: operations}, nil
}

// Resolve uses only authenticated tenant identity and immutable catalog tool
// metadata. No argument or caller-selected destination enters this boundary.
func (registry *Registry) Resolve(tenantID domain.UUID, tool domain.ToolVersionDefinition) (Resolved, error) {
	if registry == nil || tenantID == (domain.UUID{}) || domain.ValidateToolVersionDefinition(tool) != nil {
		return Resolved{}, errors.New("connector resolution context is invalid")
	}
	instance, ok := registry.instances[instanceKey{tenantID, tool.Connector.InstanceSelector}]
	if !ok {
		return Resolved{}, errors.New("connector instance is unavailable")
	}
	definition := instance.Definition()
	if !definition.Enabled || definition.ConnectorType != tool.Connector.ConnectorType {
		return Resolved{}, errors.New("connector instance does not match trusted tool metadata")
	}
	executor := registry.operations[operationKey{definition.ConnectorType, tool.Connector.Operation}]
	if executor == nil {
		return Resolved{}, errors.New("connector operation is not compiled")
	}
	return Resolved{Instance: instance, Executor: executor}, nil
}

func validRegistryKey(value string) bool {
	return len(value) <= 128 && registryKeyPattern.MatchString(value)
}

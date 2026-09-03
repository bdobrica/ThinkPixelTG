package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

const maximumDestinationConfigBytes = 16 << 10

// ConnectorInstanceDefinition is administrator-authored routing state. An
// instance identity is immutable; changing its destination requires a new ID.
type ConnectorInstanceDefinition struct {
	ID                UUID
	TenantID          UUID
	Selector          string
	ConnectorType     string
	DestinationConfig json.RawMessage
	ConfigDigest      Digest
	Enabled           bool
}

type ConnectorInstance struct{ definition ConnectorInstanceDefinition }

func NewConnectorInstance(definition ConnectorInstanceDefinition) (ConnectorInstance, error) {
	if err := ValidateConnectorInstanceDefinition(definition); err != nil {
		return ConnectorInstance{}, err
	}
	return ConnectorInstance{definition: cloneConnectorInstanceDefinition(definition)}, nil
}

func (instance ConnectorInstance) Definition() ConnectorInstanceDefinition {
	return cloneConnectorInstanceDefinition(instance.definition)
}

func ValidateConnectorInstanceDefinition(definition ConnectorInstanceDefinition) error {
	if definition.ID == (UUID{}) || definition.TenantID == (UUID{}) {
		return errors.New("connector instance and tenant IDs are required")
	}
	if !validRegistryKey(definition.Selector) || !validRegistryKey(definition.ConnectorType) {
		return errors.New("connector instance selector or type is invalid")
	}
	if len(definition.DestinationConfig) < 2 || len(definition.DestinationConfig) > maximumDestinationConfigBytes || !json.Valid(definition.DestinationConfig) {
		return errors.New("connector destination configuration is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(definition.DestinationConfig))
	decoder.UseNumber()
	var destination map[string]any
	if err := decoder.Decode(&destination); err != nil || destination == nil {
		return errors.New("connector destination configuration must be an object")
	}
	if containsCredentialField(destination) {
		return errors.New("connector destination configuration cannot contain credential material")
	}
	if DigestBytes(definition.DestinationConfig) != definition.ConfigDigest {
		return errors.New("connector destination configuration digest does not match")
	}
	return nil
}

func containsCredentialField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(strings.ToLower(key))
			switch normalized {
			case "authorization", "cookie", "setcookie", "token", "accesstoken", "refreshtoken", "clientsecret", "privatekey", "password", "apikey", "credential", "secret":
				return true
			}
			if containsCredentialField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsCredentialField(child) {
				return true
			}
		}
	}
	return false
}

func cloneConnectorInstanceDefinition(definition ConnectorInstanceDefinition) ConnectorInstanceDefinition {
	definition.DestinationConfig = append(json.RawMessage(nil), definition.DestinationConfig...)
	return definition
}

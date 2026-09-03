package domain

import (
	"strings"
	"testing"
)

func TestConnectorInstanceIsImmutable(t *testing.T) {
	definition := validConnectorInstanceDefinition(t)
	instance, err := NewConnectorInstance(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.DestinationConfig[2] = 'X'
	if string(instance.Definition().DestinationConfig) != `{"base_url":"https://api.github.com"}` {
		t.Fatal("constructor retained mutable destination")
	}
	returned := instance.Definition()
	returned.DestinationConfig[2] = 'Y'
	if string(instance.Definition().DestinationConfig) != `{"base_url":"https://api.github.com"}` {
		t.Fatal("getter exposed mutable destination")
	}
}

func TestConnectorInstanceRejectsUnsafeConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ConnectorInstanceDefinition)
	}{
		{"identity", func(value *ConnectorInstanceDefinition) { value.ID = UUID{} }},
		{"tenant", func(value *ConnectorInstanceDefinition) { value.TenantID = UUID{} }},
		{"selector", func(value *ConnectorInstanceDefinition) { value.Selector = "caller/path" }},
		{"type", func(value *ConnectorInstanceDefinition) { value.ConnectorType = "https://connector" }},
		{"invalid JSON", func(value *ConnectorInstanceDefinition) { value.DestinationConfig = []byte(`{"base_url":`) }},
		{"non-object", func(value *ConnectorInstanceDefinition) { value.DestinationConfig = []byte(`[]`) }},
		{"oversized", func(value *ConnectorInstanceDefinition) {
			value.DestinationConfig = []byte(`{"value":"` + strings.Repeat("x", maximumDestinationConfigBytes) + `"}`)
		}},
		{"digest", func(value *ConnectorInstanceDefinition) { value.ConfigDigest = DigestBytes([]byte("other")) }},
		{"authorization", func(value *ConnectorInstanceDefinition) {
			value.DestinationConfig = []byte(`{"headers":{"Authorization":"synthetic-canary"}}`)
		}},
		{"nested token", func(value *ConnectorInstanceDefinition) {
			value.DestinationConfig = []byte(`{"options":[{"access-token":"synthetic-canary"}]}`)
		}},
		{"camel-case secret", func(value *ConnectorInstanceDefinition) {
			value.DestinationConfig = []byte(`{"clientSecret":"synthetic-canary"}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validConnectorInstanceDefinition(t)
			test.mutate(&definition)
			if test.name != "digest" {
				definition.ConfigDigest = DigestBytes(definition.DestinationConfig)
			}
			if _, err := NewConnectorInstance(definition); err == nil {
				t.Fatal("unsafe connector instance accepted")
			}
		})
	}
}

func validConnectorInstanceDefinition(t *testing.T) ConnectorInstanceDefinition {
	t.Helper()
	parseID := func(value string) UUID {
		id, err := ParseUUID(value)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	destination := []byte(`{"base_url":"https://api.github.com"}`)
	return ConnectorInstanceDefinition{ID: parseID("019b0000-0000-7000-8000-000000000001"), TenantID: parseID("019b0000-0000-7000-8000-000000000002"), Selector: "github_primary", ConnectorType: "github", DestinationConfig: destination, ConfigDigest: DigestBytes(destination), Enabled: true}
}

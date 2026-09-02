package mock

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestConnectorDeterministicReadAndRetrySequence(t *testing.T) {
	connector := mustConnector(t, Config{Operations: map[string]Operation{"lookup": {
		Kind: Read, Outcomes: []Outcome{{Classification: "transient_safe"}, {Classification: "confirmed_success", Result: json.RawMessage(`{"value":7}`)}},
	}}})
	request := connectorRequest("invocation-1", "lookup")
	first, err := connector.Execute(t.Context(), request)
	if err != nil || first.Classification != "transient_safe" {
		t.Fatalf("first Execute() = %#v, %v", first, err)
	}
	second, err := connector.Execute(t.Context(), request)
	if err != nil || second.Classification != "confirmed_success" || string(second.Result) != `{"value":7}` {
		t.Fatalf("second Execute() = %#v, %v", second, err)
	}
	third, _ := connector.Execute(t.Context(), request)
	if third.Classification != second.Classification || connector.Calls("lookup") != 3 {
		t.Fatalf("terminal scripted outcome was not deterministic: %#v", third)
	}
}

func TestConnectorDelayHonorsCancellation(t *testing.T) {
	connector := mustConnector(t, Config{Operations: map[string]Operation{"slow": {Kind: Read, Delay: time.Hour, Outcomes: []Outcome{{Classification: "confirmed_success"}}}}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := connector.Execute(ctx, connectorRequest("invocation-1", "slow"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestConnectorInjectedTransportError(t *testing.T) {
	connector := mustConnector(t, Config{Operations: map[string]Operation{"fail": {Kind: Read, Outcomes: []Outcome{{Classification: "not_sent", TransportError: true}}}}})
	_, err := connector.Execute(t.Context(), connectorRequest("invocation-1", "fail"))
	var transport *TransportError
	if !errors.As(err, &transport) {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestConnectorAmbiguousWriteReconciliation(t *testing.T) {
	connector := mustConnector(t, Config{Operations: map[string]Operation{"create": {
		Kind: Write, Reconciliation: "confirmed_success", Outcomes: []Outcome{{Classification: "unknown", Applied: true, Result: json.RawMessage(`{"id":"created-1"}`)}},
	}}})
	request := connectorRequest("invocation-1", "create")
	result, err := connector.Execute(t.Context(), request)
	if err != nil || result.Classification != "unknown" {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	reconciled, err := connector.Reconcile(t.Context(), ports.ConnectorReconciliationRequest{InvocationID: request.InvocationID, Tool: request.Tool, Evidence: json.RawMessage(`{"request_ref":"opaque"}`)})
	if err != nil || reconciled.Outcome != "confirmed_success" || string(reconciled.Result) != `{"id":"created-1"}` || !json.Valid(reconciled.Evidence) {
		t.Fatalf("Reconcile() = %#v, %v", reconciled, err)
	}
}

func TestConnectorAmbiguousUnappliedWriteReconciliation(t *testing.T) {
	connector := mustConnector(t, Config{Operations: map[string]Operation{"create": {
		Kind: Write, Reconciliation: "confirmed_success", Outcomes: []Outcome{{Classification: "unknown"}},
	}}})
	request := connectorRequest("invocation-1", "create")
	_, _ = connector.Execute(t.Context(), request)
	result, err := connector.Reconcile(t.Context(), ports.ConnectorReconciliationRequest{InvocationID: request.InvocationID, Tool: request.Tool})
	if err != nil || result.Outcome != "confirmed_not_applied" {
		t.Fatalf("Reconcile() = %#v, %v", result, err)
	}
}

func mustConnector(t *testing.T, config Config) *Connector {
	t.Helper()
	connector, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return connector
}

func connectorRequest(invocationID, operation string) ports.ConnectorRequest {
	return ports.ConnectorRequest{InvocationID: invocationID, Tool: domain.ToolVersionDefinition{Connector: domain.ConnectorBinding{ConnectorType: "mock", Operation: operation}}, CanonicalArguments: json.RawMessage(`{}`)}
}
